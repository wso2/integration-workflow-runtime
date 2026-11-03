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
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
		`profiles = ["api"]`:     "names no engine service",
		`profiles = []`:          "at least one profile",
		`profiles = ["manager"]`: `unknown profile "manager"`,
	} {
		_, err := parse([]byte("[runtime]\n"+profiles+"\n"+sqliteDB), noEnv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", profiles, err, want)
		}
	}
}

func certFiles(t *testing.T) (cert, key, ca string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"engine.crt", "engine.key", "ca.pem"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "engine.crt"), filepath.Join(dir, "engine.key"), filepath.Join(dir, "ca.pem")
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

func TestDev(t *testing.T) {
	rt, err := Dev(DevOptions{IP: "127.0.0.1", Port: 8233, LogLevel: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.Services, engineServices) || rt.Auth != nil {
		t.Errorf("services = %v, auth = %v", rt.Services, rt.Auth)
	}
	c := rt.Temporal
	if c.Services["frontend"].RPC.GRPCPort != 8233 || c.Services["history"].RPC.GRPCPort != 8234 ||
		c.Services["frontend"].RPC.HTTPPort != 8243 || c.Log.Level != "warn" {
		t.Errorf("services = %+v, log = %+v", c.Services, c.Log)
	}
	if db := c.Persistence.DataStores[defaultStore].SQL; db.PluginName != "sqlite" || db.ConnectAttributes["mode"] != "memory" {
		t.Errorf("in-memory store = %+v", db)
	}
	file, err := Dev(DevOptions{IP: "127.0.0.1", Port: 7233, DBFile: "data/dev.db", LogLevel: "info"})
	if err != nil || file.Temporal.Persistence.DataStores[defaultStore].SQL.DatabaseName != "data/dev.db" {
		t.Fatalf("file store: %v, %v", file, err)
	}
	all, err := Dev(DevOptions{IP: "0.0.0.0", Port: 7233, LogLevel: "info"})
	if err != nil || net.ParseIP(all.Temporal.Global.Membership.BroadcastAddress) == nil {
		t.Fatalf("every interface: %v, %v", all, err)
	}
	if _, err := Dev(DevOptions{IP: "localhost", Port: 7233, LogLevel: "info"}); err == nil {
		t.Error("a host name was accepted as the IP")
	}
	if _, err := Dev(DevOptions{IP: "127.0.0.1", Port: 70000, LogLevel: "info"}); err == nil {
		t.Error("an invalid port was accepted")
	}
}

func TestMembershipPortsFitTheSQLSchemas(t *testing.T) {
	ports := "[engine.ports]\nfrontend = 33233\nfrontendHttp = 33243\ninternalFrontend = 33236\nhistory = 33234\n" +
		"matching = 33235\nworker = 33239\n"
	_, err := parse([]byte(ports+"[database]\ntype = \"postgresql\"\nhost = \"db\"\n"), noEnv)
	if err == nil || !strings.Contains(err.Error(), "engine.ports.history 33234: its membership port 32934 is above 32767") {
		t.Fatalf("postgresql: err = %v", err)
	}
	if _, err := parse([]byte(ports+sqliteDB), noEnv); err != nil {
		t.Fatalf("sqlite does not limit the port: %v", err)
	}
}
