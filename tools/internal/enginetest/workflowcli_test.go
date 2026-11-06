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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// workflowCLI runs the workflow-cli binary WORKFLOW_CLI names, with a profile file of its own.
type workflowCLI struct {
	t      *testing.T
	binary string
	config string
}

func newWorkflowCLI(t *testing.T) *workflowCLI {
	t.Helper()
	path := os.Getenv("WORKFLOW_CLI")
	if path == "" {
		t.Skip("WORKFLOW_CLI does not name the workflow-cli binary")
	}
	return &workflowCLI{t: t, binary: path, config: filepath.Join(t.TempDir(), "cli.toml")}
}

func (w *workflowCLI) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.binary, args...) //nolint:gosec // the CLI under test
	cmd.Env = append(os.Environ(), "WORKFLOW_CONFIG_FILE="+w.config)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (w *workflowCLI) must(args ...string) string {
	w.t.Helper()
	out, err := w.run(args...)
	if err != nil {
		w.t.Fatalf("workflow-cli %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (w *workflowCLI) mustContain(out string, want ...string) {
	w.t.Helper()
	for _, s := range want {
		if !strings.Contains(out, s) {
			w.t.Errorf("the output lacks %q:\n%s", s, out)
		}
	}
}

// TestWorkflowCLIOnADevEngine runs every P0 command against start-dev, which has no TLS or authorization.
func TestWorkflowCLIOnADevEngine(t *testing.T) {
	binary := engineBinary(t)
	w := newWorkflowCLI(t)
	base := freeBase(t)
	e := start(t, binary, base, nil, "start-dev", "--port", strconv.Itoa(base))
	addr := []string{"--address", fmt.Sprintf("127.0.0.1:%d", base)}
	cli := func(args ...string) string { return w.must(append(args, addr...)...) }

	w.mustContain(cli("operator", "cluster", "health"), "SERVING")
	w.mustContain(cli("operator", "cluster", "describe"), "History shards", "frontend", "history", "matching", "worker")

	w.mustContain(cli("operator", "namespace", "create", "-n", "orders"), "Namespace orders created.")
	w.mustContain(cli("operator", "namespace", "create", "-n", "orders"), "already exists")
	w.mustContain(cli("operator", "namespace", "describe", "-n", "orders"), "Retention    30d", "Registered")
	w.mustContain(cli("operator", "search-attribute", "list", "-n", "orders"), "WorkflowKind")
	cli("operator", "search-attribute", "create", "-n", "orders", "--name", "Customer", "--type", "Keyword")
	w.mustContain(cli("operator", "search-attribute", "list", "-n", "orders"), "Customer")
	cli("operator", "namespace", "update", "-n", "orders", "--retention", "48h", "--description", "Orders")
	w.mustContain(cli("operator", "namespace", "list"), "default", "orders", "2d", "Orders")
	w.mustContain(cli("operator", "namespace", "list", "-o", "json"), `"name": "orders"`)

	// Workflows waiting on a task queue no worker polls, as an integration's are while it is down.
	for _, id := range []string{"order-1", "order-2", "order-3"} {
		e.startTagged("orders", "Order", "orders", id, `WorkflowKind="orders.Order"`)
	}
	kind := []string{"workflow", "list", "-n", "orders", "-q", `WorkflowKind="orders.Order"`}
	var out string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if out = cli(kind...); strings.Count(out, "Running") == 3 {
			break
		}
	}
	w.mustContain(out, "order-1", "order-2", "order-3", "Order")
	w.mustContain(cli("workflow", "count", "-n", "orders"), "Total: 3")
	w.mustContain(cli("workflow", "describe", "-n", "orders", "-w", "order-1"), "Task queue               orders",
		"Status                   Running")
	cli("workflow", "signal", "-n", "orders", "-w", "order-1", "--name", "approval", "-i", `{"approved":true}`)
	w.mustContain(cli("workflow", "show", "-n", "orders", "-w", "order-1"), "WorkflowExecutionStarted",
		"WorkflowExecutionSignaled")
	cli("workflow", "cancel", "-n", "orders", "-w", "order-2", "--reason", "withdrawn")
	w.mustContain(cli("workflow", "show", "-n", "orders", "-w", "order-2"), "WorkflowExecutionCancelRequested")
	cli("workflow", "terminate", "-n", "orders", "-w", "order-3", "--reason", "stuck")
	w.mustContain(cli("workflow", "describe", "-n", "orders", "-w", "order-3"), "Terminated")
	if out, err := w.run(append([]string{"workflow", "terminate", "-n", "orders", "-w", "order-3"}, addr...)...); err == nil {
		t.Errorf("a closed workflow was terminated again:\n%s", out)
	}
	w.mustContain(cli("task-queue", "describe", "-n", "orders", "-t", "orders"), "No worker has polled")
}

// TestWorkflowCLIOnASecuredEngine reaches a secured engine through a profile, with tokens of different roles, and
// checks the services on the internal ports with a node certificate, as a container health check does.
func TestWorkflowCLIOnASecuredEngine(t *testing.T) {
	binary := engineBinary(t)
	w := newWorkflowCLI(t)
	se := newSecuredEngine(t)
	ca := se.frontend.caFile("frontend-ca")
	tokenFile := func(name string, claims map[string]any) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(se.idp.token(claims, nil)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	admin := tokenFile("admin", map[string]any{"sub": "ops", "groups": []any{"workflow-admins"}})
	app := tokenFile("app", map[string]any{"sub": "orders-app", "groups": []any{"orders-app"}})
	eng := start(t, binary, se.base, []string{"--tls-ca-path", ca, "--api-key", strings.TrimSpace(readFile(t, admin))},
		"start", "--runtime-config", se.config)

	for prop, value := range map[string]string{
		"address": fmt.Sprintf("127.0.0.1:%d", se.base), "tls.caPath": ca, "apiKeyFile": admin,
	} {
		w.must("config", "set", "--profile", "ops", "--prop", prop, "--value", value)
	}
	for prop, value := range map[string]string{
		"address": fmt.Sprintf("127.0.0.1:%d", se.base), "tls.caPath": ca, "apiKeyFile": app, "namespace": "orders",
	} {
		w.must("config", "set", "--profile", "app", "--prop", prop, "--value", value)
	}

	w.mustContain(w.must("operator", "cluster", "health", "--profile", "ops"), "SERVING")
	w.mustContain(w.must("operator", "cluster", "describe", "--profile", "ops"), "internal-frontend", "history",
		"matching", "worker")
	w.mustContain(w.must("operator", "namespace", "create", "--profile", "ops", "-n", "orders"), "created")
	w.must("operator", "namespace", "create", "--profile", "ops", "-n", "billing")
	w.mustContain(w.must("operator", "search-attribute", "list", "--profile", "app"), "WorkflowKind")
	w.mustContain(w.must("operator", "namespace", "describe", "--profile", "app"), "orders")
	// The app reads a workflow the admin started in its namespace, not only an empty one.
	eng.startTagged("orders", "Order", "orders", "secured-1", `WorkflowKind="orders.Order"`)
	var count string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if count = w.must("workflow", "count", "--profile", "app"); strings.Contains(count, "Total: 1") {
			break
		}
	}
	w.mustContain(count, "Total: 1")
	w.mustContain(w.must("task-queue", "describe", "--profile", "app", "-t", "orders"), "No worker has polled")

	refused := map[string][]string{
		"an app creating a namespace":      {"operator", "namespace", "create", "--profile", "app", "-n", "billing"},
		"an app changing retention":        {"operator", "namespace", "update", "--profile", "app", "--retention", "1h"},
		"an app listing another namespace": {"workflow", "list", "--profile", "app", "-n", "billing"},
		"no token":                         {"operator", "namespace", "list", "--address", fmt.Sprintf("127.0.0.1:%d", se.base), "--tls-ca-path", ca},
	}
	for name, args := range refused {
		if out, err := w.run(args...); err == nil || !strings.Contains(out, "Request unauthorized") {
			t.Errorf("%s: %v, want Request unauthorized:\n%s", name, err, out)
		}
	}
	if out, err := w.run("operator", "namespace", "list", "--address", "engine.example.invalid:7233",
		"--api-key", "x"); err == nil || !strings.Contains(out, "only over TLS") {
		t.Errorf("the CLI sent a token in plain text: %v\n%s", err, out)
	}

	// Each internal port answers its own service's health to a node certificate. History reports SERVING only once
	// it holds its shards, so the check is retried as a readiness probe would be.
	node := []string{"--tls-ca-path", se.internode.caFile("internode-ca"), "--tls-cert-path", se.nodeCert,
		"--tls-key-path", se.nodeKey}
	for service, port := range map[string]int{"internal-frontend": se.base + 3, "history": se.base + 1,
		"matching": se.base + 2, "worker": se.base + 6} {
		args := append([]string{"operator", "cluster", "health", "--service", service,
			"--address", fmt.Sprintf("127.0.0.1:%d", port)}, node...)
		var out string
		var err error
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
			if out, err = w.run(args...); err == nil {
				break
			}
		}
		if err != nil || !strings.Contains(out, "SERVING") {
			t.Errorf("%s did not report SERVING: %v\n%s", service, err, out)
		}
	}
	if out, err := w.run("operator", "cluster", "health", "--service", "history", "--address",
		fmt.Sprintf("127.0.0.1:%d", se.base+1), "--tls-ca-path", se.internode.caFile("internode-ca")); err == nil {
		t.Errorf("history answered a caller without a node certificate:\n%s", out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // a test file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// startTagged starts a workflow with a search attribute, retrying while the engine's namespace cache does not yet know
// an attribute just added to the namespace (refreshed every few seconds).
func (e *engine) startTagged(namespace, workflowType, taskQueue, id, attribute string) {
	e.t.Helper()
	args := []string{"workflow", "start", "--namespace", namespace, "--type", workflowType, "--task-queue", taskQueue,
		"--workflow-id", id, "--search-attribute", attribute}
	var out string
	var err error
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if out, err = e.temporal(args...); err == nil || !strings.Contains(out, "no mapping defined for search attribute") {
			break
		}
	}
	if err != nil {
		e.t.Fatalf("temporal %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
