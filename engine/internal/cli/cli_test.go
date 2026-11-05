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

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/temporal"

	"github.com/wso2/integration-workflow-runtime/engine/internal/runtimeconfig"
	"github.com/wso2/integration-workflow-runtime/engine/plugins/jwtclaims"
)

const minimalConfig = "testdata/minimal.yaml"

type fakeServer struct {
	started  bool
	startErr error
}

func (f *fakeServer) Start() error {
	f.started = true
	return f.startErr
}

func (f *fakeServer) Stop() error { return nil }

func newTestCommand(server temporal.Server, factoryErr error) (command, *bytes.Buffer, *bytes.Buffer) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	c := command{
		version: "9.9.9-test",
		stdout:  stdout,
		stderr:  stderr,
		newServer: func([]string, ...temporal.ServerOption) (temporal.Server, error) {
			return server, factoryErr
		},
		prepareSchema: func(config.Persistence, log.Logger) error { return nil },
		devNamespaces: func(config.Persistence, []string) error { return nil },
	}
	return c, stdout, stderr
}

func TestRunWithoutArgumentsPrintsUsage(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: workflow-engine") {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"serve"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: workflow-engine") {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestVersionPrintsProductAndTemporalVersions(t *testing.T) {
	c, stdout, _ := newTestCommand(nil, nil)
	if code := c.run([]string{"version"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "workflow-engine 9.9.9-test (Temporal server ") {
		t.Fatalf("stdout = %q, want the product and Temporal versions", out)
	}
}

func TestServiceListCollectsRepeatedFlags(t *testing.T) {
	var s serviceList
	for _, v := range []string{"frontend", "history"} {
		if err := s.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if got := s.String(); got != "frontend,history" {
		t.Fatalf("String() = %q, want %q", got, "frontend,history")
	}
}

func TestStartRequiresConfigFile(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"start"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--config-file is required") {
		t.Fatalf("stderr = %q, want the missing --config-file error", stderr.String())
	}
}

func TestStartHelpIsNotAnError(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"start", "-h"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "-config-file") {
		t.Fatalf("stderr = %q, want the start flags", stderr.String())
	}
}

func TestStartRejectsUnknownFlag(t *testing.T) {
	c, _, _ := newTestCommand(nil, nil)
	if err := c.start([]string{"--no-such-flag"}); err == nil {
		t.Fatal("start accepted an unknown flag")
	}
}

func TestStartRejectsMissingConfigFile(t *testing.T) {
	c, _, _ := newTestCommand(nil, nil)
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	err := c.start([]string{"--config-file", missing})
	if err == nil || !strings.Contains(err.Error(), "unable to load configuration") {
		t.Fatalf("start with %s = %v, want a configuration load error", missing, err)
	}
}

func TestStartRefusesNoAuthorizer(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	err := c.start([]string{"--config-file", minimalConfig})
	if err == nil || !strings.Contains(err.Error(), "no authorizer configured") {
		t.Fatalf("start without an authorizer = %v, want the no-authorizer refusal", err)
	}
	if server.started {
		t.Fatal("the server started without an authorizer")
	}
}

func TestStartWithAllowNoAuthStartsServer(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--config-file", minimalConfig, "--allow-no-auth", "--service", "frontend"}); err != nil {
		t.Fatalf("start = %v, want nil", err)
	}
	if !server.started {
		t.Fatal("the server was not started")
	}
}

func TestStartReportsServerCreationError(t *testing.T) {
	c, _, _ := newTestCommand(nil, errors.New("boom"))
	err := c.start([]string{"--config-file", minimalConfig, "--allow-no-auth"})
	if err == nil || !strings.Contains(err.Error(), "unable to create the server: boom") {
		t.Fatalf("start = %v, want the server creation error", err)
	}
}

func TestStartReturnsServerStartError(t *testing.T) {
	server := &fakeServer{startErr: errors.New("port in use")}
	c, _, _ := newTestCommand(server, nil)
	if code := c.run([]string{"start", "--config-file", minimalConfig, "--allow-no-auth"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestMainReturnsUsageExitCode(t *testing.T) {
	if code := Main("9.9.9-test", nil); code != 2 {
		t.Fatalf("Main(nil) = %d, want 2", code)
	}
}

func TestStartRefusesAuthorizerWithoutClaimMapper(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	err := c.start([]string{"--config-file", "testdata/authorizer-only.yaml"})
	if err == nil || !strings.Contains(err.Error(), "no claim mapper configured") {
		t.Fatalf("start with an authorizer and no claim mapper = %v, want the claim mapper refusal", err)
	}
	if server.started {
		t.Fatal("the server started with an authorizer that admits every caller")
	}
}

func TestStartAcceptsAuthorizerWithClaimMapper(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--config-file", "testdata/authorizer-and-claim-mapper.yaml"}); err != nil {
		t.Fatalf("start with an authorizer and a claim mapper = %v, want nil", err)
	}
	if !server.started {
		t.Fatal("the server was not started")
	}
}

func TestStartAllowsMissingClaimMapperWhenNoAuthIsAllowed(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--config-file", "testdata/authorizer-only.yaml", "--allow-no-auth"}); err != nil {
		t.Fatalf("start with --allow-no-auth = %v, want nil", err)
	}
}

func TestStartRefusesAuthorizerWithoutInternalFrontend(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	err := c.start([]string{"--config-file", "testdata/authorizer-without-internal-frontend.yaml"})
	if err == nil || !strings.Contains(err.Error(), "an authorizer needs the internal frontend") {
		t.Fatalf("start with an authorizer and no internal frontend = %v, want the internal frontend refusal", err)
	}
	if server.started {
		t.Fatal("the server started without the internal frontend its authorizer needs")
	}
}

func TestDefaultServicesAddTheInternalFrontendWhenConfigured(t *testing.T) {
	plain := &config.Config{Services: map[string]config.Service{"frontend": {}}}
	if got := defaultServices(plain); !slices.Equal(got, temporal.DefaultServices) {
		t.Fatalf("defaultServices without an internal frontend = %v, want %v", got, temporal.DefaultServices)
	}
	withInternal := &config.Config{Services: map[string]config.Service{"frontend": {}, "internal-frontend": {}}}
	if got := defaultServices(withInternal); !slices.Contains(got, "internal-frontend") || len(got) != len(temporal.DefaultServices)+1 {
		t.Fatalf("defaultServices with an internal frontend = %v, want the defaults plus internal-frontend", got)
	}
}

func TestStartFromRuntimeConfig(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--runtime-config", "testdata/runtime-sqlite.toml", "--allow-no-auth"}); err != nil {
		t.Fatalf("start = %v, want nil", err)
	}
	if !server.started {
		t.Fatal("the server was not started")
	}
}

func TestStartFromRuntimeConfigKeepsTheAuthorizerCheck(t *testing.T) {
	c, _, _ := newTestCommand(&fakeServer{}, nil)
	err := c.start([]string{"--runtime-config", "testdata/runtime-sqlite.toml"})
	if err == nil || !strings.Contains(err.Error(), "no authorizer configured") {
		t.Fatalf("start = %v, want the no-authorizer refusal", err)
	}
}

func TestStartNeedsExactlyOneConfiguration(t *testing.T) {
	c, _, _ := newTestCommand(&fakeServer{}, nil)
	both := c.start([]string{"--runtime-config", "testdata/runtime-sqlite.toml", "--config-file", minimalConfig})
	if both == nil || !strings.Contains(both.Error(), "not both") {
		t.Fatalf("start with both = %v, want a conflict error", both)
	}
	neither := c.start(nil)
	if neither == nil || !strings.Contains(neither.Error(), "--runtime-config or --config-file is required") {
		t.Fatalf("start with neither = %v, want the required error", neither)
	}
}

func TestRenderConfigHidesThePassword(t *testing.T) {
	c, stdout, _ := newTestCommand(nil, nil)
	if code := c.run([]string{"render-config", "--runtime-config", "testdata/runtime-postgresql.toml"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := stdout.String()
	if strings.Contains(out, "do-not-print") || !strings.Contains(out, "pluginName: postgres12") {
		t.Fatalf("render-config output leaks the password or lacks the plugin:\n%s", out)
	}
}

func TestRenderConfigErrors(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"render-config"}); code != 1 || !strings.Contains(stderr.String(), "--runtime-config is required") {
		t.Fatalf("render-config without a file: code %d, stderr %q", code, stderr.String())
	}
	if err := c.renderConfig([]string{"--runtime-config", filepath.Join(t.TempDir(), "absent.toml")}); err == nil {
		t.Fatal("render-config accepted a missing file")
	}
	if err := c.renderConfig([]string{"--no-such-flag"}); err == nil {
		t.Fatal("render-config accepted an unknown flag")
	}
}

func TestStartWithIdentityProviderAuth(t *testing.T) {
	server := &fakeServer{}
	c, _, stderr := newTestCommand(server, nil)
	if code := c.run([]string{"start", "--runtime-config", "testdata/runtime-auth.toml"}); code != 0 || !server.started {
		t.Fatalf("exit code = %d, started = %v, stderr = %s", code, server.started, stderr.String())
	}
}

func TestNewClaimMapper(t *testing.T) {
	logger := log.NewNoopLogger()
	rt, err := runtimeconfig.Load("testdata/runtime-auth.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.Services, []string{"frontend", "internal-frontend", "history"}) {
		t.Fatalf("services = %v", rt.Services)
	}
	m, err := newClaimMapper(&rt.Temporal, rt, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.(*jwtclaims.Mapper); !ok {
		t.Fatalf("mapper = %T, want *jwtclaims.Mapper", m)
	}
	if _, err := newClaimMapper(&rt.Temporal, nil, logger); err == nil || !strings.Contains(err.Error(), "only through runtime.toml") {
		t.Fatalf("mapper without runtime settings: %v", err)
	}
	plain := &config.Config{Global: config.Global{Authorization: config.Authorization{ClaimMapper: "default"}}}
	if m, err := newClaimMapper(plain, nil, logger); err != nil || m == nil {
		t.Fatalf("Temporal's default mapper: %v, %v", m, err)
	}
}

func TestNewClaimMapperDiscoversTheKeySet(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q}`, base+"/oauth2/token", base+"/oauth2/jwks")
	}))
	defer srv.Close()
	base = srv.URL
	write := func(issuer string) string {
		p := filepath.Join(t.TempDir(), "runtime.toml")
		cert, _ := filepath.Abs("testdata/certs/engine.crt")
		key, _ := filepath.Abs("testdata/certs/engine.key")
		toml := "[engine.tls]\ncertFile = \"" + cert + "\"\nkeyFile = \"" + key + "\"\n" +
			"[engine.auth]\ntype = \"jwt\"\nissuer = \"" + issuer + "\"\n[database]\ntype = \"sqlite\"\ninMemory = true\n"
		if err := os.WriteFile(p, []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rt, err := runtimeconfig.Load(write(base + "/oauth2/token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClaimMapper(&rt.Temporal, rt, log.NewNoopLogger()); err != nil {
		t.Fatal(err)
	}
	if got := rt.Temporal.Global.Authorization.JWTKeyProvider.KeySourceURIs; !slices.Equal(got, []string{base + "/oauth2/jwks"}) {
		t.Fatalf("key set = %v", got)
	}
	rt, err = runtimeconfig.Load(write("http://127.0.0.1:1/oauth2/token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClaimMapper(&rt.Temporal, rt, log.NewNoopLogger()); err == nil ||
		!strings.Contains(err.Error(), "unable to discover the identity provider's key set") {
		t.Fatalf("unreachable provider: %v", err)
	}
}

func TestServicesReachTheServerFactory(t *testing.T) {
	for name, tt := range map[string]struct {
		args []string
		want []string
	}{
		"profiles":                   {[]string{"--runtime-config", "testdata/runtime-auth.toml"}, []string{"frontend", "internal-frontend", "history"}},
		"service override":           {[]string{"--runtime-config", "testdata/runtime-auth.toml", "--service", "worker"}, []string{"worker"}},
		"default profile":            {[]string{"--runtime-config", "testdata/runtime-sqlite.toml", "--allow-no-auth"}, []string{"frontend", "internal-frontend", "history", "matching", "worker"}},
		"api profile with a service": {[]string{"--runtime-config", "testdata/runtime-api.toml", "--allow-no-auth", "--service", "history"}, []string{"history"}},
	} {
		c, _, stderr := newTestCommand(&fakeServer{}, nil)
		var got []string
		c.newServer = func(services []string, _ ...temporal.ServerOption) (temporal.Server, error) {
			got = services
			return &fakeServer{}, nil
		}
		if err := c.start(tt.args); err != nil {
			t.Fatalf("%s: start = %v (%s)", name, err, stderr.String())
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: services = %v, want %v", name, got, tt.want)
		}
	}
}

func TestStartRefusesAnAPIProfileWithoutAService(t *testing.T) {
	c, _, _ := newTestCommand(&fakeServer{}, nil)
	if err := c.start([]string{"--runtime-config", "testdata/runtime-api.toml", "--allow-no-auth"}); !errors.Is(err, runtimeconfig.ErrNoServices) {
		t.Fatalf("start = %v, want %v", err, runtimeconfig.ErrNoServices)
	}
}

func TestNewTemporalServerBuildsAServer(t *testing.T) {
	rt, err := runtimeconfig.Load("testdata/runtime-sqlite.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTemporalServer([]string{"frontend"}, temporal.WithConfig(&rt.Temporal)); err != nil {
		t.Fatalf("newTemporalServer = %v", err)
	}
}

func TestStartPreparesTheSchemaBeforeTheServer(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	var prepared []string
	c.prepareSchema = func(p config.Persistence, _ log.Logger) error {
		if server.started {
			t.Error("the schema was prepared after the server started")
		}
		prepared = append(prepared, p.DataStores[p.DefaultStore].SQL.DatabaseName)
		return nil
	}
	args := []string{"start", "--runtime-config", "testdata/runtime-postgresql.toml", "--allow-no-auth"}
	if code := c.run(args); code != 0 || !server.started {
		t.Fatalf("exit code = %d, started = %v, want 0 and started", code, server.started)
	}
	if !slices.Equal(prepared, []string{"temporal"}) {
		t.Fatalf("prepared = %v, want the temporal database once", prepared)
	}
}

func TestStartStopsWhenTheSchemaCannotBePrepared(t *testing.T) {
	server := &fakeServer{}
	c, _, stderr := newTestCommand(server, nil)
	c.prepareSchema = func(config.Persistence, log.Logger) error { return errors.New("connection refused") }
	args := []string{"start", "--runtime-config", "testdata/runtime-postgresql.toml", "--allow-no-auth"}
	if code := c.run(args); code != 1 || server.started {
		t.Fatalf("exit code = %d, started = %v, want 1 and not started", code, server.started)
	}
	if !strings.Contains(stderr.String(), "unable to prepare the database schema: connection refused") {
		t.Fatalf("stderr = %q, want the schema error", stderr.String())
	}
}

func TestStartLeavesTheSchemaAloneWhenAsked(t *testing.T) {
	unmanaged := filepath.Join(t.TempDir(), "runtime.toml")
	toml := "[database]\ntype = \"postgresql\"\nhost = \"db\"\nmanageSchema = false\n"
	if err := os.WriteFile(unmanaged, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"start", "--runtime-config", unmanaged, "--allow-no-auth"},
		{"start", "--config-file", minimalConfig, "--allow-no-auth"},
	} {
		server := &fakeServer{}
		c, _, _ := newTestCommand(server, nil)
		c.prepareSchema = func(config.Persistence, log.Logger) error {
			t.Errorf("%v prepared the schema", args)
			return nil
		}
		if code := c.run(args); code != 0 || !server.started {
			t.Fatalf("%v: exit code = %d, started = %v, want 0 and started", args, code, server.started)
		}
	}
}

func startWithLogFile(t *testing.T, logFile string) error {
	t.Helper()
	toml := filepath.Join(t.TempDir(), "runtime.toml")
	content := fmt.Sprintf("[engine]\nlogFile = %q\n\n[database]\ntype = \"sqlite\"\ninMemory = true\n", logFile)
	if err := os.WriteFile(toml, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, _ := newTestCommand(&fakeServer{}, nil)
	return c.start([]string{"--runtime-config", toml, "--allow-no-auth"})
}

func TestStartCreatesTheLogDirectory(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "logs", "engine.log")
	if err := startWithLogFile(t, logFile); err != nil {
		t.Fatalf("start = %v", err)
	}
	if _, err := os.Stat(logFile); err != nil {
		t.Fatalf("the log file was not created: %v", err)
	}
}

func TestStartNamesALogFileItCannotOpen(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"a parent that is a file": filepath.Join(parent, "engine.log")}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		readOnly := filepath.Join(t.TempDir(), "read-only")
		if err := os.Mkdir(readOnly, 0o500); err != nil {
			t.Fatal(err)
		}
		cases["an unwritable directory"] = filepath.Join(readOnly, "engine.log")
	}
	for name, logFile := range cases {
		if err := startWithLogFile(t, logFile); err == nil || !strings.Contains(err.Error(), "engine.logFile "+logFile) {
			t.Errorf("%s: start = %v, want an error naming engine.logFile", name, err)
		}
	}
}

func TestStartDev(t *testing.T) {
	server := &fakeServer{}
	c, _, stderr := newTestCommand(server, nil)
	var created []string
	c.devNamespaces = func(p config.Persistence, names []string) error {
		if server.started {
			t.Error("the namespaces were created after the server started")
		}
		if p.DataStores[p.DefaultStore].SQL.PluginName != "sqlite" {
			t.Errorf("store = %+v", p.DataStores[p.DefaultStore].SQL)
		}
		created = names
		return nil
	}
	if code := c.run([]string{"start-dev", "--port", "18233", "--namespace", "orders"}); code != 0 || !server.started {
		t.Fatalf("exit code = %d, started = %v, stderr = %s", code, server.started, stderr.String())
	}
	if !slices.Equal(created, []string{"default", "orders"}) {
		t.Fatalf("namespaces = %v", created)
	}
	if !strings.Contains(stderr.String(), "Development engine on 127.0.0.1:18233") || !strings.Contains(stderr.String(), "in memory") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestStartDevCreatesTheDatabaseDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "nested")
	c, _, stderr := newTestCommand(&fakeServer{}, nil)
	if code := c.run([]string{"start-dev", "--db-file", filepath.Join(dir, "dev.db")}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("the --db-file directory was not created: %v", err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c, _, stderr = newTestCommand(&fakeServer{}, nil)
	if code := c.run([]string{"start-dev", "--db-file", filepath.Join(blocker, "dev.db")}); code != 1 ||
		!strings.Contains(stderr.String(), "unable to create the directory of --db-file") {
		t.Fatalf("a file as the directory: exit code = %d, stderr = %q", code, stderr.String())
	}
}

func TestStartDevErrors(t *testing.T) {
	c, _, stderr := newTestCommand(&fakeServer{}, nil)
	c.devNamespaces = func(config.Persistence, []string) error { return errors.New("disk full") }
	if code := c.run([]string{"start-dev", "--db-file", filepath.Join(t.TempDir(), "dev.db")}); code != 1 ||
		!strings.Contains(stderr.String(), "unable to create the namespaces: disk full") {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	c, _, _ = newTestCommand(&fakeServer{}, nil)
	if code := c.run([]string{"start-dev", "--ip", "localhost"}); code != 1 {
		t.Fatalf("a host name as the IP: exit code = %d", code)
	}
	if code := c.run([]string{"start-dev", "--bogus"}); code != 1 {
		t.Fatalf("unknown flag: exit code = %d", code)
	}
	if code := c.run([]string{"start-dev", "-h"}); code != 0 {
		t.Fatalf("help: exit code = %d", code)
	}
}
