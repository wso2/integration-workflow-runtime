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
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDevModeNeedsNoSetup starts start-dev on a file in a directory that does not exist yet, with no namespace or
// search attribute created outside it: default and --namespace exist with WorkflowKind, a first workflow tagged with
// WorkflowKind starts and is found by it, and all of it survives a restart on the same file.
func TestDevModeNeedsNoSetup(t *testing.T) {
	binary := engineBinary(t)
	db := filepath.Join(t.TempDir(), "data", "dev.db")
	base := freeBase(t)
	args := []string{"start-dev", "--port", strconv.Itoa(base), "--db-file", db, "--namespace", "orders"}

	first := start(t, binary, base, nil, args...)
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("start-dev did not create the database file: %v", err)
	}
	for _, ns := range []string{"default", "orders"} {
		first.must("operator", "namespace", "describe", "--namespace", ns)
		if out := first.must("operator", "search-attribute", "list", "--namespace", ns); !strings.Contains(out, "WorkflowKind") {
			t.Fatalf("namespace %s has no WorkflowKind search attribute:\n%s", ns, out)
		}
	}
	first.must("workflow", "start", "--namespace", "orders", "--type", "Order", "--task-queue", "nobody",
		"--workflow-id", "order-1", "--search-attribute", `WorkflowKind="orders.Order"`)
	before := first.workflow("orders", "order-1")
	first.findByKind("orders", "orders.Order", "order-1")
	first.stop()

	second := start(t, binary, base, nil, args...)
	for _, ns := range []string{"default", "orders"} {
		if out := second.must("operator", "search-attribute", "list", "--namespace", ns); !strings.Contains(out, "WorkflowKind") {
			t.Fatalf("after the restart, namespace %s has no WorkflowKind search attribute:\n%s", ns, out)
		}
	}
	after := second.workflow("orders", "order-1")
	if after.RunID != before.RunID || after.Status != "WORKFLOW_EXECUTION_STATUS_RUNNING" {
		t.Fatalf("after the restart: run %s, status %s; want run %s still running", after.RunID, after.Status, before.RunID)
	}
	second.findByKind("orders", "orders.Order", "order-1")
	second.must("workflow", "terminate", "--namespace", "orders", "--workflow-id", "order-1", "--reason", "done")
	if closed := second.workflow("orders", "order-1"); closed.Status != "WORKFLOW_EXECUTION_STATUS_TERMINATED" {
		t.Fatalf("the restarted engine did not close the workflow: status %s", closed.Status)
	}
}

// findByKind waits until a visibility query on WorkflowKind lists the workflow; visibility is updated asynchronously.
func (e *engine) findByKind(namespace, kind, id string) {
	e.t.Helper()
	query := `WorkflowKind="` + kind + `"`
	var out string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		out = e.must("workflow", "list", "--namespace", namespace, "--query", query)
		if strings.Contains(out, id) {
			return
		}
	}
	e.t.Fatalf("a query on %s did not list %s:\n%s", query, id, out)
}
