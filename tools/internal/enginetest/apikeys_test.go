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

package enginetest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// apiKeyConfig is a single-node engine on this machine with TLS and the runtime's own API keys only.
func apiKeyConfig(t *testing.T, base int) (config, caFile string) {
	t.Helper()
	dir := t.TempDir()
	frontend := newPKI(t, dir, "frontend-ca")
	cert, key := frontend.issue("engine")
	config = writeConfig(t, dir, ports(base)+fmt.Sprintf(`
[engine.tls]
certFile = %s
keyFile = %s

[engine.auth]
type = "apiKeys"

[engine.auth.apiKeys]
signingKey = %s

[database]
type = "sqlite"
inMemory = true
`, tomlString(cert), tomlString(key), tomlString(filepath.Join(dir, "security", "api-key-signing.pem"))))
	return config, frontend.caFile("frontend-ca")
}

// apiKey runs workflow-engine api-key and returns its standard output.
func apiKey(t *testing.T, binary string, args ...string) string {
	t.Helper()
	out, err := exec.Command(binary, append([]string{"api-key"}, args...)...).Output() //nolint:gosec // the engine
	if err != nil {
		t.Fatalf("api-key %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestAPIKeysOnASecuredEngine runs the engine with only its own API keys: keys created with workflow-engine api-key
// grant their roles to the Temporal CLI and workflow-cli, a key from another runtime is refused, and a revoked key
// stops working while the engine runs.
func TestAPIKeysOnASecuredEngine(t *testing.T) {
	binary := engineBinary(t)
	base := freeBase(t)
	config, caFile := apiKeyConfig(t, base)
	create := func(name, role string, namespaces ...string) []string {
		args := []string{"create", "--runtime-config", config, "--name", name, "--role", role}
		for _, ns := range namespaces {
			args = append(args, "--namespace", ns)
		}
		return []string{"--tls-ca-path", caFile, "--api-key", apiKey(t, binary, args...)}
	}
	admin := create("ops", "admin", "*")
	app := create("orders-app", "write", "orders")
	auditor := create("audit", "read", "orders")
	otherConfig, _ := apiKeyConfig(t, base)
	foreign := []string{"--tls-ca-path", caFile, "--api-key", apiKey(t, binary, "create", "--runtime-config",
		otherConfig, "--name", "elsewhere", "--role", "admin", "--namespace", "*")}

	e := start(t, binary, base, admin, "start", "--runtime-config", config)
	for _, ns := range []string{"orders", "billing"} {
		e.must("operator", "namespace", "create", "--namespace", ns, "--retention", "24h")
	}
	startIn := func(ns string) []string {
		return []string{"workflow", "start", "--namespace", ns, "--type", "Order", "--task-queue", "orders",
			"--workflow-id", fmt.Sprintf("order-%d", time.Now().UnixNano())}
	}
	list := []string{"workflow", "list", "--namespace", "orders"}
	for name, c := range map[string]struct {
		flags []string
		args  []string
	}{"the integration starts": {app, startIn("orders")}, "the integration lists": {app, list},
		"the reader lists": {auditor, list}} {
		if out, err := e.temporalAt(e.address, c.flags, c.args...); err != nil {
			t.Fatalf("%s: %v\n%s\n%s", name, err, out, e.log)
		}
	}
	for name, c := range map[string]struct {
		flags []string
		args  []string
	}{
		"the reader starts":          {auditor, startIn("orders")},
		"the integration in billing": {app, startIn("billing")},
		"the integration as admin":   {app, []string{"operator", "namespace", "create", "--namespace", "x"}},
		"a key from another runtime": {foreign, list},
		"no key":                     {[]string{"--tls-ca-path", caFile}, list},
	} {
		if out, err := e.temporalAt(e.address, c.flags, c.args...); err == nil ||
			!strings.Contains(out, "Request unauthorized") {
			t.Errorf("%s was not refused: %v\n%s", name, err, out)
		}
	}

	if os.Getenv("WORKFLOW_CLI") != "" {
		out, err := newWorkflowCLI(t).run("workflow", "list", "-n", "orders", "--address", e.address, "--tls-ca-path", caFile,
			"--api-key", app[3])
		if err != nil {
			t.Fatalf("workflow-cli with the integration's key: %v\n%s", err, out)
		}
	}

	if out, err := exec.Command(binary, "api-key", "revoke", "--runtime-config", config, "--name", "orders-app", //nolint:gosec // the engine
		"--reason", "test").CombinedOutput(); err != nil {
		t.Fatalf("revoke: %v\n%s", err, out)
	}
	// The running engine reads the revocation list within its check interval.
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := e.temporalAt(e.address, app, list...)
		if err != nil && strings.Contains(out, "Request unauthorized") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the revoked key still works: %v\n%s", err, out)
		}
		time.Sleep(time.Second)
	}
	if out, err := e.temporalAt(e.address, auditor, list...); err != nil {
		t.Fatalf("revoking one key ended another: %v\n%s", err, out)
	}
}
