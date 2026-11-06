// Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package runtimeconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.temporal.io/server/common/dynamicconfig"
)

const sqliteDB = "[database]\ntype = \"sqlite\"\ninMemory = true\n"

func mustParseRuntime(t *testing.T, toml string) *Runtime {
	t.Helper()
	rt, err := parse([]byte(toml), noEnv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rt
}

func TestProfilesChooseServices(t *testing.T) {
	all := []string{"frontend", "internal-frontend", "history", "matching", "worker"}
	tests := map[string][]string{
		"":                                   all,
		`profiles = ["all"]`:                 all,
		`profiles = ["engine", "api"]`:       all,
		`profiles = ["frontend"]`:            {"frontend", "internal-frontend"},
		`profiles = ["matching", "history"]`: {"history", "matching"},
		`profiles = ["worker"]`:              {"worker"},
		`profiles = ["api"]`:                 nil,
	}
	for profiles, want := range tests {
		rt := mustParseRuntime(t, "[runtime]\n"+profiles+"\n"+sqliteDB)
		if !slices.Equal(rt.Services, want) {
			t.Errorf("%q: services = %v, want %v", profiles, rt.Services, want)
		}
	}
}

func TestProfileErrors(t *testing.T) {
	for profiles, want := range map[string]string{
		`profiles = []`:          "at least one profile",
		`profiles = ["manager"]`: `unknown profile "manager"`,
	} {
		_, err := parse([]byte("[runtime]\n"+profiles+"\n"+sqliteDB), noEnv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", profiles, err, want)
		}
	}
}

// certFiles writes a certificate for engine.internal and placeholder key and CA files, which the parser only checks
// exist.
func certFiles(t *testing.T) (cert, key, ca string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"engine.key", "ca.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return certificate(t, dir, "engine.crt", "engine.internal"), filepath.Join(dir, "engine.key"),
		filepath.Join(dir, "ca.pem")
}

// certificate writes a self-signed certificate for the names given: IP addresses become IP SANs, others DNS names.
func certificate(t *testing.T, dir, name string, names ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "engine"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInternodeServerNameDefaultsToTheCertificatesOnlyDNSName(t *testing.T) {
	cert, key, ca := certFiles(t)
	tls := "[engine.tls]\ncertFile = \"" + cert + "\"\nkeyFile = \"" + key + "\"\n"
	internode := "[engine.tls.internode]\nenabled = true\ncaFile = \"" + ca + "\"\n"
	got := mustParseRuntime(t, tls+internode+sqliteDB).Temporal.Global.TLS
	if got.Internode.Client.ServerName != "engine.internal" || got.SystemWorker.Client.ServerName != "engine.internal" {
		t.Fatalf("internode server name = %q, system worker %q; want the certificate's engine.internal",
			got.Internode.Client.ServerName, got.SystemWorker.Client.ServerName)
	}
	dir := t.TempDir()
	own := func(certFile, serverName string) string {
		toml := internode + "certFile = \"" + certFile + "\"\nkeyFile = \"" + key + "\"\n"
		if serverName != "" {
			toml += "serverName = \"" + serverName + "\"\n"
		}
		return tls + toml + sqliteDB
	}
	several := certificate(t, dir, "several.crt", "node-1.internal", "nodes.internal")
	ipOnly := certificate(t, dir, "ip.crt", "10.0.1.21")
	for name, tt := range map[string]struct{ toml, want string }{
		"one of several DNS names": {own(several, "nodes.internal"), "nodes.internal"},
		"an IP address it carries": {own(ipOnly, "10.0.1.21"), "10.0.1.21"},
		"a wildcard it carries":    {own(certificate(t, dir, "wild.crt", "*.nodes.internal"), "a.nodes.internal"), "a.nodes.internal"},
	} {
		rt := mustParseRuntime(t, tt.toml)
		if got := rt.Temporal.Global.TLS.Internode.Client.ServerName; got != tt.want {
			t.Errorf("%s: server name %q, want %q", name, got, tt.want)
		}
	}
	for name, tt := range map[string]struct{ toml, want string }{
		"several DNS names":          {own(several, ""), "several DNS names (node-1.internal, nodes.internal)"},
		"an IP address only":         {own(ipOnly, ""), "has no DNS name"},
		"no names at all":            {own(certificate(t, dir, "none.crt"), ""), "has no DNS name"},
		"a name it does not carry":   {own(several, "other.internal"), `serverName "other.internal" is not a name in`},
		"an IP it does not carry":    {own(ipOnly, "10.0.1.22"), `serverName "10.0.1.22" is not a name in`},
		"not a PEM file":             {own(ca, ""), "holds no PEM certificate"},
		"a file that does not exist": {own(filepath.Join(dir, "gone.crt"), ""), "internode.certFile"},
	} {
		if _, err := parse([]byte(tt.toml), noEnv); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tt.want)
		}
	}
}

func TestInternodeTLSTurnsOnMembershipTLS(t *testing.T) {
	cert, key, ca := certFiles(t)
	tls := "[engine.tls]\ncertFile = \"" + cert + "\"\nkeyFile = \"" + key + "\"\n"
	on := mustParseRuntime(t, tls+"[engine.tls.internode]\nenabled = true\ncaFile = \""+ca+"\"\n"+sqliteDB)
	if v, ok := on.DynamicConfig[dynamicconfig.EnableRingpopTLS.Key()].(bool); !ok || !v {
		t.Fatalf("dynamic config = %v, want %s on", on.DynamicConfig, dynamicconfig.EnableRingpopTLS.Key())
	}
	if off := mustParseRuntime(t, tls+sqliteDB); off.DynamicConfig != nil {
		t.Fatalf("dynamic config without internode TLS = %v, want none", off.DynamicConfig)
	}
}

func TestTLSMapsToTemporal(t *testing.T) {
	cert, key, ca := certFiles(t)
	toml := "[engine.tls]\ncertFile = \"" + cert + "\"\nkeyFile = \"" + key + "\"\nclientCaFile = \"" + ca + "\"\n" +
		"[engine.tls.internode]\nenabled = true\ncaFile = \"" + ca + "\"\nserverName = \"engine.internal\"\n" + sqliteDB
	tls := mustParseRuntime(t, toml).Temporal.Global.TLS
	f := tls.Frontend.Server
	if f.CertFile != cert || f.KeyFile != key || !f.RequireClientAuth || !slices.Equal(f.ClientCAFiles, []string{ca}) {
		t.Errorf("frontend = %+v", f)
	}
	in := tls.Internode
	if in.Server.CertFile != cert || !in.Server.RequireClientAuth || in.Client.ServerName != "engine.internal" ||
		!slices.Equal(in.Client.RootCAFiles, []string{ca}) {
		t.Errorf("internode = %+v", in)
	}
	if tls.SystemWorker.CertFile != cert || tls.SystemWorker.Client.ServerName != "engine.internal" {
		t.Errorf("system worker = %+v", tls.SystemWorker)
	}
}

func TestTLSWithoutClientCertificates(t *testing.T) {
	cert, key, _ := certFiles(t)
	tls := mustParseRuntime(t, "[engine.tls]\ncertFile = \""+cert+"\"\nkeyFile = \""+key+"\"\n"+sqliteDB).Temporal.Global.TLS
	if tls.Frontend.Server.RequireClientAuth || tls.Internode.Server.CertFile != "" {
		t.Errorf("tls = %+v", tls)
	}
	if off := mustParseRuntime(t, sqliteDB).Temporal.Global.TLS; off.Frontend.Server.CertFile != "" {
		t.Errorf("tls without settings = %+v", off)
	}
}

func TestTLSErrors(t *testing.T) {
	cert, key, ca := certFiles(t)
	for toml, want := range map[string]string{
		"[engine.tls]\ncertFile = \"" + cert + "\"\n":                                                    "both certFile and keyFile",
		"[engine.tls]\nclientCaFile = \"" + ca + "\"\n":                                                  "clientCaFile needs",
		"[engine.tls]\ncertFile = \"/no/cert\"\nkeyFile = \"" + key + "\"\n":                             "engine.tls.certFile",
		"[engine.tls.internode]\nenabled = true\n":                                                       "internode.caFile is required",
		"[engine.tls.internode]\nenabled = true\ncaFile = \"" + ca + "\"\n":                              "its own certFile and keyFile",
		"[engine.tls.internode]\ncaFile = \"" + ca + "\"\n":                                              "internode.enabled is false",
		"[engine.tls.internode]\nenabled = true\ncaFile = \"" + ca + "\"\ncertFile = \"" + cert + "\"\n": "internode needs both",
	} {
		_, err := parse([]byte(toml+sqliteDB), noEnv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", toml, err, want)
		}
	}
}

func TestAuthMapsToTemporalAndClaims(t *testing.T) {
	cert, key, _ := certFiles(t)
	rt := mustParseRuntime(t, "[engine.tls]\ncertFile = \""+cert+"\"\nkeyFile = \""+key+"\"\n"+`[engine.auth]
type = "jwt"
jwksUrls = ["https://idp.example.com/jwks"]
keyRefreshInterval = "5m"
issuer = "https://idp.example.com"
audience = "workflow-engine"
rolesClaim = "realm_access.roles"
leewaySeconds = 30

[[engine.auth.rules]]
value = "workflow-admins"
namespace = "*"
role = "admin"
`+sqliteDB)
	a := rt.Temporal.Global.Authorization
	if a.Authorizer != "default" || a.ClaimMapper != ClaimMapperName || a.JWTKeyProvider.KeySourceURIs[0] != "https://idp.example.com/jwks" ||
		a.JWTKeyProvider.RefreshInterval.Minutes() != 5 {
		t.Errorf("authorization = %+v", a)
	}
	if rt.Auth == nil || rt.Auth.Issuer != "https://idp.example.com" || rt.Auth.RolesClaim != "realm_access.roles" ||
		len(rt.Auth.Rules) != 1 || rt.Auth.Leeway.Seconds() != 30 {
		t.Errorf("claim settings = %+v", rt.Auth)
	}
	if off := mustParseRuntime(t, sqliteDB); off.Auth != nil || off.Temporal.Global.Authorization.Authorizer != "" {
		t.Errorf("auth without settings = %+v", off.Temporal.Global.Authorization)
	}
}

func TestAuthErrors(t *testing.T) {
	for toml, want := range map[string]string{
		"[engine.auth]\ntype = \"oauth\"\n":                                                                                              `must be "jwt" or "none"`,
		"[engine.auth]\ntype = \"jwt\"\n":                                                                                                "needs the identity provider's issuer",
		"[engine.auth]\njwksUrls = [\"https://idp\"]\n":                                                                                  `type is not "jwt"`,
		"[engine.auth]\ntype = \"jwt\"\njwksUrls = [\"x\"]\nkeyRefreshInterval = \"soon\"\n":                                             "keyRefreshInterval",
		"[engine.auth]\ntype = \"jwt\"\njwksUrls = [\"http://idp.example/jwks\"]\n":                                                      "jwksUrls: \"http://idp.example/jwks\" must use https",
		"[engine.auth]\ntype = \"jwt\"\nissuer = \"http://idp.example\"\n":                                                               "issuer (its key set is discovered from it)",
		"[engine.auth]\ntype = \"jwt\"\njwksUrls = [\"x\"]\nleewaySeconds = -1\n":                                                        "leewaySeconds",
		"[engine.auth]\ntype = \"jwt\"\njwksUrls = [\"x\"]\n[[engine.auth.rules]]\nvalue = \"a\"\nnamespace = \"b\"\nrole = \"owner\"\n": `role "owner"`,
	} {
		_, err := parse([]byte(toml+sqliteDB), noEnv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", toml, err, want)
		}
	}
}

func TestMetrics(t *testing.T) {
	m := mustParseRuntime(t, "[engine.metrics]\nprometheusAddress = \"0.0.0.0:9464\"\n"+sqliteDB).Temporal.Global.Metrics
	if m == nil || m.Prometheus.ListenAddress != "0.0.0.0:9464" || m.Prometheus.HandlerPath != "/metrics" {
		t.Errorf("metrics = %+v", m)
	}
	if off := mustParseRuntime(t, sqliteDB).Temporal.Global.Metrics; off != nil {
		t.Errorf("metrics without settings = %+v", off)
	}
	for address, want := range map[string]string{"9464": "must be host:port", "0.0.0.0:70000": "invalid port",
		"0.0.0.0:metrics": "invalid port"} {
		_, err := parse([]byte("[engine.metrics]\nprometheusAddress = \""+address+"\"\n"+sqliteDB), noEnv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", address, err, want)
		}
	}
}

func TestJWTNeedsTLSAndProtectedInternalPorts(t *testing.T) {
	cert, key, ca := certFiles(t)
	tls := "[engine.tls]\ncertFile = \"" + cert + "\"\nkeyFile = \"" + key + "\"\n"
	internode := "[engine.tls.internode]\nenabled = true\ncaFile = \"" + ca + "\"\n"
	auth := "[engine.auth]\ntype = \"jwt\"\nissuer = \"https://idp.example.com\"\n"
	everywhere := "[engine]\nbindAddress = \"0.0.0.0\"\nbroadcastAddress = \"10.0.0.5\"\n"
	for name, tt := range map[string]struct{ toml, want string }{
		"jwt without tls":               {auth + sqliteDB, `needs engine.tls (certFile and keyFile)`},
		"jwt on all interfaces":         {everywhere + tls + auth + sqliteDB, "needs engine.tls.internode when engine.bindAddress is 0.0.0.0"},
		"jwt on another machine's view": {"[engine]\nbindAddress = \"10.0.0.5\"\n" + tls + auth + sqliteDB, "needs engine.tls.internode"},
	} {
		if _, err := parse([]byte(tt.toml), noEnv); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tt.want)
		}
	}
	for name, toml := range map[string]string{
		"loopback with tls":             tls + auth + sqliteDB,
		"all interfaces with internode": everywhere + tls + internode + auth + sqliteDB,
		"no auth on all interfaces":     everywhere + sqliteDB,
	} {
		if _, err := parse([]byte(toml), noEnv); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLogFile(t *testing.T) {
	if l := mustParseRuntime(t, sqliteDB).Temporal.Log; !l.Stdout || l.OutputFile != "" {
		t.Errorf("default log = %+v", l)
	}
	l := mustParseRuntime(t, "[engine]\nlogFile = \"logs/engine.log\"\n"+sqliteDB).Temporal.Log
	if l.Stdout || l.OutputFile != "logs/engine.log" || l.Level != "info" {
		t.Errorf("file log = %+v", l)
	}
}

func TestBroadcastAddressAuto(t *testing.T) {
	saved := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = saved })
	ipnet := func(s string) net.Addr { ip, n, _ := net.ParseCIDR(s); n.IP = ip; return n }
	toml := "[engine]\nbindAddress = \"0.0.0.0\"\nbroadcastAddress = \"auto\"\n" + sqliteDB
	for addrs, want := range map[string]struct {
		list []net.Addr
		ip   string
	}{
		"ipv4": {[]net.Addr{ipnet("127.0.0.1/8"), ipnet("fe80::1/64"), ipnet("10.1.2.3/16")}, "10.1.2.3"},
		"ipv6": {[]net.Addr{ipnet("127.0.0.1/8"), ipnet("2001:db8::5/64")}, "2001:db8::5"},
	} {
		interfaceAddrs = func() ([]net.Addr, error) { return want.list, nil }
		if got := mustParseRuntime(t, toml).Temporal.Global.Membership.BroadcastAddress; got != want.ip {
			t.Errorf("%s: broadcast = %q, want %q", addrs, got, want.ip)
		}
	}
	interfaceAddrs = func() ([]net.Addr, error) { return []net.Addr{ipnet("127.0.0.1/8")}, nil }
	if _, err := parse([]byte(toml), noEnv); err == nil || !strings.Contains(err.Error(), "no non-loopback address") {
		t.Errorf("err = %v", err)
	}
	interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("boom") }
	if _, err := parse([]byte(toml), noEnv); err == nil {
		t.Error("an interface error was ignored")
	}
	if _, err := parse([]byte("[engine]\nbroadcastAddress = \"runtime\"\n"+sqliteDB), noEnv); err == nil ||
		!strings.Contains(err.Error(), `must be an IP address or "auto"`) {
		t.Errorf("hostname: err = %v", err)
	}
}
