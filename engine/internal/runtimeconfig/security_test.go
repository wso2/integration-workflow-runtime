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
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

// certificate writes a self-signed certificate with the DNS names given.
func certificate(t *testing.T, dir, name string, dnsNames ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "engine"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), DNSNames: dnsNames}
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

func TestInternodeServerNameDefaultsToTheCertificatesDNSName(t *testing.T) {
	cert, key, ca := certFiles(t)
	tls := "[engine.tls]\ncertFile = \"" + cert + "\"\nkeyFile = \"" + key + "\"\n"
	internode := "[engine.tls.internode]\nenabled = true\ncaFile = \"" + ca + "\"\n"
	got := mustParseRuntime(t, tls+internode+sqliteDB).Temporal.Global.TLS
	if got.Internode.Client.ServerName != "engine.internal" || got.SystemWorker.Client.ServerName != "engine.internal" {
		t.Fatalf("internode server name = %q, system worker %q; want the certificate's engine.internal",
			got.Internode.Client.ServerName, got.SystemWorker.Client.ServerName)
	}
	explicit := mustParseRuntime(t, tls+internode+"serverName = \"nodes.example\"\n"+sqliteDB).Temporal.Global.TLS
	if explicit.Internode.Client.ServerName != "nodes.example" {
		t.Fatalf("an explicit server name was replaced: %q", explicit.Internode.Client.ServerName)
	}
	dir := t.TempDir()
	ipOnly := certificate(t, dir, "ip-only.crt")
	own := func(certFile string) string {
		return internode + "certFile = \"" + certFile + "\"\nkeyFile = \"" + key + "\"\n"
	}
	if got := mustParseRuntime(t, tls+own(certificate(t, dir, "node.crt", "nodes.internal", "other"))+sqliteDB).
		Temporal.Global.TLS.Internode.Client.ServerName; got != "nodes.internal" {
		t.Fatalf("with an internode certificate of its own: %q, want nodes.internal", got)
	}
	for name, tt := range map[string]struct{ toml, want string }{
		"no DNS name":      {tls + own(ipOnly), "has no DNS name"},
		"not a PEM file":   {tls + own(ca), "holds no PEM certificate"},
		"not readable yet": {tls + own(filepath.Join(dir, "gone.crt")), "internode.certFile"},
	} {
		if _, err := parse([]byte(tt.toml+sqliteDB), noEnv); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tt.want)
		}
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
