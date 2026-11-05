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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTwoSecuredNodesFormOneEngine runs the secured configuration as two nodes on one SQLite file: the first runs the
// frontend, matching and worker, the second the frontend and history. A workflow started through the first node needs
// the second node's history, so it runs only if the nodes find each other over TLS-protected membership and call each
// other over internode TLS.
// startBusy is start, tried again when the engine stopped on SQLITE_BUSY: two processes on one SQLite file, which
// only this test does, can collide on the start-up write of the cluster metadata.
func startBusy(t *testing.T, binary string, base int, flags []string, args ...string) *engine {
	t.Helper()
	for attempt := 1; ; attempt++ {
		e, err := launch(t, binary, base, flags, args...)
		if err == nil {
			return e
		}
		if attempt == 3 || !strings.Contains(err.Error(), "SQLITE_BUSY") {
			t.Fatal(err)
		}
		t.Logf("attempt %d stopped on SQLITE_BUSY; starting the node again", attempt)
	}
}

func TestTwoSecuredNodesFormOneEngine(t *testing.T) {
	binary := engineBinary(t)
	se := newSecuredEngine(t)
	data, err := os.ReadFile(se.config) //nolint:gosec // a test file
	if err != nil {
		t.Fatal(err)
	}
	memory := "[database]\ntype = \"sqlite\"\ninMemory = true\n"
	if !strings.Contains(string(data), memory) {
		t.Fatal("the secured configuration no longer uses an in-memory database")
	}
	shared := strings.Replace(string(data), memory,
		"[database]\ntype = \"sqlite\"\npath = "+tomlString(filepath.Join(t.TempDir(), "engine.db"))+"\n", 1)
	node := func(base int, profiles string) string {
		toml := "[runtime]\nprofiles = " + profiles + "\n" + strings.Replace(shared, ports(se.base), ports(base), 1)
		return writeConfig(t, t.TempDir(), toml)
	}
	admin := []string{"--tls-ca-path", se.frontend.caFile("frontend-ca"),
		"--api-key", se.idp.token(map[string]any{"sub": "ops", "groups": []any{"workflow-admins"}}, nil)}

	first := start(t, binary, se.base, admin, "start", "--runtime-config",
		node(se.base, `["frontend", "matching", "worker"]`))
	base := freeBase(t)
	second := startBusy(t, binary, base, admin, "start", "--runtime-config", node(base, `["frontend", "history"]`))
	first.must("operator", "namespace", "create", "--namespace", "orders", "--retention", "24h")
	first.must("workflow", "start", "--namespace", "orders", "--type", "Order", "--task-queue", "orders",
		"--workflow-id", "order-1")
	if got := second.workflow("orders", "order-1"); got.Status != "WORKFLOW_EXECUTION_STATUS_RUNNING" {
		t.Fatalf("the second node sees order-1 as %q, want it running", got.Status)
	}
	for _, e := range []*engine{first, second} {
		if strings.Contains(e.log.String(), "either ServerName or InsecureSkipVerify") {
			t.Errorf("an internode connection had no server name:\n%s", e.log)
		}
	}
}
