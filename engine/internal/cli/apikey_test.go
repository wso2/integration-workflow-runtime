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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/log"

	"github.com/wso2/integration-workflow-runtime/engine/internal/runtimeconfig"
	"github.com/wso2/integration-workflow-runtime/engine/plugins/apikeys"
)

// tomlPath writes a path as a TOML literal string, which keeps Windows backslashes as they are.
func tomlPath(p string) string { return "'" + p + "'" }

// apiKeyRuntime writes a runtime.toml with TLS and the given auth section.
func apiKeyRuntime(t *testing.T, auth string) string {
	t.Helper()
	dir := t.TempDir()
	cert, _ := filepath.Abs("testdata/certs/engine.crt")
	key, _ := filepath.Abs("testdata/certs/engine.key")
	toml := "[engine.tls]\ncertFile = " + tomlPath(cert) + "\nkeyFile = " + tomlPath(key) + "\n" + auth +
		"[database]\ntype = \"sqlite\"\ninMemory = true\n"
	path := filepath.Join(dir, "runtime.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func apiKeysOnly(dir string) string {
	return "[engine.auth]\ntype = \"apiKeys\"\n[engine.auth.apiKeys]\nsigningKey = " +
		tomlPath(filepath.Join(dir, "security", "api-key-signing.pem")) + "\n"
}

func TestAPIKeyCreateListRevoke(t *testing.T) {
	dir := t.TempDir()
	path := apiKeyRuntime(t, apiKeysOnly(dir))
	run := func(args ...string) (int, string, string) {
		c, stdout, stderr := newTestCommand(&fakeServer{}, nil)
		code := c.run(append([]string{"api-key"}, args...))
		return code, stdout.String(), stderr.String()
	}
	code, token, info := run("create", "--runtime-config", path, "--name", "orders-app", "--namespace", "orders",
		"--namespace", "billing", "--role", "write", "--expires", "90d")
	if code != 0 || !strings.Contains(info, "Created the signing key") ||
		!strings.Contains(info, "for orders-app: write in orders, billing, expires") {
		t.Fatalf("create = %d %q", code, info)
	}
	token = strings.TrimSpace(token)
	if !apikeys.IsAPIKey(token) {
		t.Fatalf("create printed %q, want the key alone", token)
	}

	rt, err := runtimeconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newClaimMapper(&rt.Temporal, rt, log.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := m.GetClaims(&authorization.AuthInfo{AuthToken: "Bearer " + token})
	if err != nil || claims.Namespaces["orders"] != authorization.RoleWriter ||
		claims.Namespaces["billing"] != authorization.RoleWriter {
		t.Fatalf("claims = %+v, %v", claims, err)
	}

	if code, _, info := run("create", "--runtime-config", path, "--name", "ops", "--namespace", "*", "--role",
		"admin"); code != 0 || strings.Contains(info, "Created the signing key") {
		t.Fatalf("second create = %d %q, want the existing signing key", code, info)
	}
	code, out, _ := run("list", "--runtime-config", path)
	if code != 0 || !strings.Contains(out, "orders-app") || !strings.Contains(out, "orders:write,billing:write") ||
		!strings.Contains(out, "*:admin") || strings.Count(out, "active") != 2 || strings.Contains(out, token) {
		t.Fatalf("list = %d\n%s", code, out)
	}
	if code, _, info := run("revoke", "--runtime-config", path, "--name", "orders-app", "--reason", "rotated"); code != 0 ||
		!strings.Contains(info, "Revoked") {
		t.Fatalf("revoke = %d %q", code, info)
	}
	if _, out, _ := run("list", "--runtime-config", path); !strings.Contains(out, "revoked") ||
		strings.Count(out, "active") != 1 {
		t.Fatalf("list after revoke:\n%s", out)
	}
	// A fresh mapper reads the list at once; a running one within apikeys' check interval.
	m, _ = newClaimMapper(&rt.Temporal, rt, log.NewNoopLogger())
	if _, err := m.GetClaims(&authorization.AuthInfo{AuthToken: "Bearer " + token}); err == nil ||
		!strings.Contains(err.Error(), "revoked") {
		t.Fatalf("a revoked key = %v", err)
	}
}

func TestAPIKeyErrors(t *testing.T) {
	dir := t.TempDir()
	path := apiKeyRuntime(t, apiKeysOnly(dir))
	noKeys := apiKeyRuntime(t, "")
	// Arguments stay slices, so a temporary directory with a space in it stays one argument.
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "usage: workflow-engine api-key"},
		{[]string{"rotate"}, "usage: workflow-engine api-key"},
		{[]string{"create", "--name", "a"}, "--runtime-config is required"},
		{[]string{"create", "--runtime-config", noKeys, "--name", "a", "--namespace", "b", "--role", "write"}, "has no [engine.auth.apiKeys]"},
		{[]string{"create", "--runtime-config", path, "--name", "a", "--role", "write"}, "give at least one --namespace"},
		{[]string{"create", "--runtime-config", path, "--name", "a", "--namespace", "b", "--role", "owner"}, `role "owner"`},
		{[]string{"create", "--runtime-config", path, "--namespace", "b", "--role", "write"}, "needs a name"},
		{[]string{"create", "--runtime-config", path, "--name", "a", "--namespace", "b", "--role", "write", "--expires", "soon"}, "--expires"},
		{[]string{"create", "--bogus"}, "flag provided but not defined"},
		{[]string{"revoke", "--runtime-config", path}, "give --id or --name"},
		{[]string{"revoke", "--runtime-config", path, "--name", "nobody"}, "no API key named"},
		{[]string{"revoke", "--runtime-config", path, "--id", "has#hash"}, "is not an API key ID"},
		{[]string{"list", "--runtime-config", filepath.Join(dir, "gone")}, "no such file"},
	} {
		cmd, _, stderr := newTestCommand(&fakeServer{}, nil)
		if code := cmd.run(append([]string{"api-key"}, c.args...)); code == 0 || !strings.Contains(stderr.String(), c.want) {
			t.Errorf("api-key %q = %d %q, want %q", c.args, code, stderr.String(), c.want)
		}
	}
	c, stdout, _ := newTestCommand(&fakeServer{}, nil)
	if code := c.run([]string{"api-key", "list", "--runtime-config", path}); code != 0 ||
		stdout.String() != "No API key has been created.\n" {
		t.Fatalf("empty list = %d %q", code, stdout.String())
	}
}

func TestParseLifetime(t *testing.T) {
	for in, want := range map[string]time.Duration{"90d": 90 * 24 * time.Hour, "720h": 720 * time.Hour, "1h30m": 90 * time.Minute} {
		if got, err := parseLifetime(in); err != nil || got != want {
			t.Errorf("parseLifetime(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"0d", "-1h", "d", "1y", ""} {
		if _, err := parseLifetime(in); err == nil {
			t.Errorf("parseLifetime(%q) was accepted", in)
		}
	}
}

func TestClaimMapperWithBothProviderAndAPIKeys(t *testing.T) {
	dir := t.TempDir()
	path := apiKeyRuntime(t, "[engine.auth]\ntype = \"jwt\"\njwksUrls = [\"https://idp.example/jwks\"]\n"+
		"issuer = \"https://idp.example\"\n[engine.auth.apiKeys]\nsigningKey = "+
		tomlPath(filepath.Join(dir, "k.pem"))+"\n")
	rt, err := runtimeconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newClaimMapper(&rt.Temporal, rt, log.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.(*apikeys.Mapper); !ok {
		t.Fatalf("mapper = %T, want *apikeys.Mapper wrapping the identity provider's", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "k.pem")); err != nil {
		t.Fatalf("the start did not create the signing key: %v", err)
	}
}
