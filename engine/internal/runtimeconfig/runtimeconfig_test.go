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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.temporal.io/server/common/config"
)

func noEnv(string) (string, bool) { return "", false }

func mustParse(t *testing.T, toml string) *config.Config {
	t.Helper()
	cfg, err := parse([]byte(toml), noEnv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}

func TestDatabaseTypesMapToTemporalPlugins(t *testing.T) {
	tests := []struct {
		name, toml, plugin, defaultDB, visibilityDB, addr string
		shards                                            int32
	}{
		{
			name:   "postgresql",
			toml:   "[database]\ntype = \"postgresql\"\nhost = \"db.example.com\"\nuser = \"wf\"\npassword = \"secret\"\n",
			plugin: "postgres12", defaultDB: "temporal", visibilityDB: "temporal_visibility",
			addr: "db.example.com:5432", shards: 512,
		},
		{
			name:   "mysql",
			toml:   "[database]\ntype = \"mysql\"\nhost = \"db\"\nport = 3307\nname = \"t\"\nvisibilityName = \"v\"\n",
			plugin: "mysql8", defaultDB: "t", visibilityDB: "v", addr: "db:3307", shards: 512,
		},
		{
			name:   "sqlite file",
			toml:   "[database]\ntype = \"sqlite\"\npath = \"data/workflow.db\"\n",
			plugin: "sqlite", defaultDB: "data/workflow.db", visibilityDB: "data/workflow.db", addr: "localhost", shards: 1,
		},
		{
			name:   "sqlite in memory",
			toml:   "[database]\ntype = \"sqlite\"\ninMemory = true\n",
			plugin: "sqlite", defaultDB: "default", visibilityDB: "default", addr: "localhost", shards: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mustParse(t, tt.toml)
			stores := cfg.Persistence.DataStores
			def, vis := stores[defaultStore].SQL, stores[visibilityStore].SQL
			if def.PluginName != tt.plugin || vis.PluginName != tt.plugin {
				t.Errorf("plugins = %q, %q, want %q", def.PluginName, vis.PluginName, tt.plugin)
			}
			if def.DatabaseName != tt.defaultDB || vis.DatabaseName != tt.visibilityDB {
				t.Errorf("databases = %q, %q, want %q, %q", def.DatabaseName, vis.DatabaseName, tt.defaultDB, tt.visibilityDB)
			}
			if def.ConnectAddr != tt.addr {
				t.Errorf("connectAddr = %q, want %q", def.ConnectAddr, tt.addr)
			}
			if cfg.Persistence.NumHistoryShards != tt.shards {
				t.Errorf("numHistoryShards = %d, want %d", cfg.Persistence.NumHistoryShards, tt.shards)
			}
		})
	}
}

func TestDefaultsFollowTemporalPorts(t *testing.T) {
	cfg := mustParse(t, "[database]\ntype = \"sqlite\"\ninMemory = true\n")
	want := map[string][2]int{"frontend": {7233, 6933}, "internal-frontend": {7236, 6936}, "history": {7234, 6934},
		"matching": {7235, 6935}, "worker": {7239, 6939}}
	for name, ports := range want {
		rpc := cfg.Services[name].RPC
		if rpc.GRPCPort != ports[0] || rpc.MembershipPort != ports[1] || rpc.BindOnIP != "127.0.0.1" {
			t.Errorf("%s rpc = %+v, want gRPC %d, membership %d on 127.0.0.1", name, rpc, ports[0], ports[1])
		}
	}
	if http := cfg.Services["frontend"].RPC.HTTPPort; http != 7243 {
		t.Errorf("frontend HTTP port = %d, want 7243", http)
	}
	info := cfg.ClusterMetadata.ClusterInformation[clusterName]
	if info.RPCAddress != "127.0.0.1:7233" || info.HTTPAddress != "127.0.0.1:7243" {
		t.Errorf("cluster addresses = %q, %q", info.RPCAddress, info.HTTPAddress)
	}
	if cfg.Global.Membership.BroadcastAddress != "127.0.0.1" || cfg.Log.Level != "info" {
		t.Errorf("broadcast = %q, log level = %q", cfg.Global.Membership.BroadcastAddress, cfg.Log.Level)
	}
}

func TestEngineSettingsOverrideDefaults(t *testing.T) {
	cfg := mustParse(t, `
[engine]
bindAddress = "0.0.0.0"
broadcastAddress = "10.0.0.5"
historyShards = 1024
logLevel = "warn"

[engine.ports]
frontend = 17233
frontendHttp = 17243
internalFrontend = 17236
history = 17234
matching = 17235
worker = 17239

[database]
type = "postgresql"
host = "db"
`)
	if rpc := cfg.Services["frontend"].RPC; rpc.GRPCPort != 17233 || rpc.MembershipPort != 16933 || rpc.BindOnIP != "0.0.0.0" {
		t.Errorf("frontend rpc = %+v", rpc)
	}
	if rpc := cfg.Services["internal-frontend"].RPC; rpc.GRPCPort != 17236 || rpc.MembershipPort != 16936 {
		t.Errorf("internal-frontend rpc = %+v", rpc)
	}
	if got := cfg.ClusterMetadata.ClusterInformation[clusterName].RPCAddress; got != "10.0.0.5:17233" {
		t.Errorf("rpcAddress = %q, want the broadcast address", got)
	}
	if cfg.Persistence.NumHistoryShards != 1024 || cfg.Log.Level != "warn" {
		t.Errorf("shards = %d, level = %q", cfg.Persistence.NumHistoryShards, cfg.Log.Level)
	}
}

func TestDatabaseTLSIsPassedThrough(t *testing.T) {
	cfg := mustParse(t, `
[database]
type = "postgresql"
host = "db"

[database.tls]
enabled = true
caFile = "/certs/ca.pem"
serverName = "db.internal"
enableHostVerification = true
`)
	tls := cfg.Persistence.DataStores[defaultStore].SQL.TLS
	if tls == nil || !tls.Enabled || tls.CaFile != "/certs/ca.pem" || tls.ServerName != "db.internal" || !tls.EnableHostVerification {
		t.Fatalf("tls = %+v", tls)
	}
	if mustParse(t, "[database]\ntype = \"mysql\"\nhost = \"db\"\n").Persistence.DataStores[defaultStore].SQL.TLS != nil {
		t.Error("tls must be nil when not enabled")
	}
}

func TestPasswordFromEnvironment(t *testing.T) {
	lookup := func(name string) (string, bool) { return "from-env", name == "WF_DB_PASSWORD" }
	cfg, err := parse([]byte("[database]\ntype = \"postgresql\"\nhost = \"db\"\npassword = \"$env{WF_DB_PASSWORD}\"\n"), lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Persistence.DataStores[visibilityStore].SQL.Password; got != "from-env" {
		t.Fatalf("password = %q, want the environment value", got)
	}
}

func TestOtherTablesAreIgnored(t *testing.T) {
	mustParse(t, `
[runtime]
profiles = ["all"]

[users.sso]
issuer = "https://idp.example.com"

[database]
type = "sqlite"
inMemory = true
`)
}

func TestInvalidConfigurationsAreRejected(t *testing.T) {
	tests := []struct{ name, toml, want string }{
		{"not toml", "[database\n", "invalid runtime configuration"},
		{"unknown database type", "[database]\ntype = \"oracle\"\n", `database.type "oracle"`},
		{"missing type", "[engine]\nlogLevel = \"info\"\n", `database.type ""`},
		{"typo in engine table", "[engine]\nbindAdress = \"0.0.0.0\"\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "engine.bindAdress"},
		{"typo in database table", "[database]\ntype = \"sqlite\"\ninMemory = true\nhots = \"x\"\n", "database.hots"},
		{"missing host", "[database]\ntype = \"postgresql\"\n", "database.host is required"},
		{"bad database port", "[database]\ntype = \"mysql\"\nhost = \"db\"\nport = 70000\n", "database.port 70000"},
		{"same database names", "[database]\ntype = \"mysql\"\nhost = \"db\"\nname = \"x\"\nvisibilityName = \"x\"\n", "must differ"},
		{"sqlite without location", "[database]\ntype = \"sqlite\"\n", "exactly one of database.path"},
		{"sqlite with both", "[database]\ntype = \"sqlite\"\npath = \"a.db\"\ninMemory = true\n", "exactly one of database.path"},
		{"bad log level", "[engine]\nlogLevel = \"loud\"\n[database]\ntype = \"sqlite\"\ninMemory = true\n", `engine.logLevel "loud"`},
		{"bind not an ip", "[engine]\nbindAddress = \"host\"\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "must be an IP address"},
		{"bind all without broadcast", "[engine]\nbindAddress = \"0.0.0.0\"\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "engine.broadcastAddress is required"},
		{"negative shards", "[engine]\nhistoryShards = -1\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "historyShards must be at least 1"},
		{"bad max conns", "[database]\ntype = \"sqlite\"\ninMemory = true\nmaxConns = -1\n", "maxConns must be at least 1"},
		{"duplicate ports", "[engine.ports]\nhistory = 7233\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "both use port 7233"},
		{"membership collides", "[engine.ports]\nworker = 6933\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "both use port 6933"},
		{"port without room for membership", "[engine.ports]\nworker = 100\n[database]\ntype = \"sqlite\"\ninMemory = true\n", "engine.ports.worker 100"},
		{"tls files without tls", "[database]\ntype = \"mysql\"\nhost = \"db\"\n[database.tls]\ncaFile = \"ca.pem\"\n", "tls.enabled is false"},
		{"missing env password", "[database]\ntype = \"mysql\"\nhost = \"db\"\npassword = \"$env{NOPE}\"\n", "NOPE, which is not set"},
		{"secret references", "[database]\ntype = \"mysql\"\nhost = \"db\"\npassword = \"$secret{db}\"\n", "not supported yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.toml), noEnv)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parse = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.toml")
	if err := os.WriteFile(path, []byte("[database]\ntype = \"sqlite\"\ninMemory = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load = %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.toml")); err == nil || !strings.Contains(err.Error(), "unable to read") {
		t.Fatalf("Load of a missing file = %v, want a read error", err)
	}
}

func TestRenderHidesPasswords(t *testing.T) {
	cfg := mustParse(t, "[database]\ntype = \"postgresql\"\nhost = \"db\"\npassword = \"s3cret\"\n")
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "s3cret") || !strings.Contains(string(out), redacted) {
		t.Fatalf("rendered config leaks or omits the password:\n%s", out)
	}
	if !strings.Contains(string(out), "pluginName: postgres12") {
		t.Fatalf("rendered config lacks the plugin:\n%s", out)
	}
	if cfg.Persistence.DataStores[defaultStore].SQL.Password != "s3cret" {
		t.Fatal("Render changed the configuration it was given")
	}
}
