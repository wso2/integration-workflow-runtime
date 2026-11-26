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
	"runtime"
	"strings"
	"testing"
)

// TestSQLiteFileSurvivesRestart starts the engine on a new SQLite file (which creates the schema), records a
// namespace and a waiting workflow, restarts the engine on the same file, and finds both still there.
func TestSQLiteFileSurvivesRestart(t *testing.T) {
	binary := engineBinary(t)
	dir := t.TempDir()
	// URI delimiters in the name must not change which file SQLite opens; Windows file names cannot hold "?".
	name := "engine #1 ?v=2 100%.db"
	if runtime.GOOS == "windows" {
		name = "engine #1 100%.db"
	}
	db := filepath.Join(dir, name)
	base := freeBase(t)
	config := writeConfig(t, dir, ports(base)+"[database]\ntype = \"sqlite\"\npath = "+tomlString(db)+"\n")

	first := start(t, binary, base, nil, "start", "--runtime-config", config, "--allow-no-auth")
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("the first start did not create the database file: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if n := e.Name(); n != "runtime.toml" && !strings.HasPrefix(n, name) {
			t.Errorf("SQLite created %q, not only %q and its journal files", n, name)
		}
	}
	first.must("operator", "namespace", "create", "--namespace", "persisted", "--retention", "24h")
	first.must("workflow", "start", "--namespace", "persisted", "--type", "Waiter", "--task-queue", "nobody",
		"--workflow-id", "waiting-1")
	before := first.workflow("persisted", "waiting-1")
	lockPorts(t) // keep the range until the restarted engine reports SERVING
	first.stop()

	second := start(t, binary, base, nil, "start", "--runtime-config", config, "--allow-no-auth")
	if out := second.must("operator", "namespace", "describe", "--namespace", "persisted"); !strings.Contains(out, "persisted") {
		t.Fatalf("the namespace did not survive the restart:\n%s", out)
	}
	after := second.workflow("persisted", "waiting-1")
	if after.RunID != before.RunID || after.Status != "WORKFLOW_EXECUTION_STATUS_RUNNING" {
		t.Fatalf("after the restart: run %s, status %s; want run %s still running", after.RunID, after.Status, before.RunID)
	}
	second.must("workflow", "terminate", "--namespace", "persisted", "--workflow-id", "waiting-1", "--reason", "done")
	if closed := second.workflow("persisted", "waiting-1"); closed.Status != "WORKFLOW_EXECUTION_STATUS_TERMINATED" {
		t.Fatalf("the restarted engine did not close the workflow: status %s", closed.Status)
	}
	history := second.must("workflow", "show", "--namespace", "persisted", "--workflow-id", "waiting-1")
	for _, event := range []string{"WorkflowExecutionStarted", "WorkflowExecutionTerminated"} {
		if !strings.Contains(history, event) {
			t.Errorf("history lacks %s:\n%s", event, history)
		}
	}
}
