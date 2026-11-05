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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const startTimeout = 90 * time.Second

// engine is a running workflow-engine process.
type engine struct {
	t       *testing.T
	cmd     *exec.Cmd
	log     *bytes.Buffer
	address string
	done    chan struct{} // closed when the process exits
	// flags go on every Temporal CLI call, such as the CA and a token for a secured engine.
	flags []string
}

// engineBinary returns the engine binary, or skips the test when it or the temporal CLI is missing.
func engineBinary(t *testing.T) string {
	t.Helper()
	path := os.Getenv("WORKFLOW_ENGINE")
	if path == "" {
		t.Skip("WORKFLOW_ENGINE does not name the engine binary")
	}
	if _, err := exec.LookPath("temporal"); err != nil {
		t.Skip("the temporal CLI is not on PATH")
	}
	return path
}

// freeBase finds a frontend port whose service ports (+1 to +10) and membership ports (-300) are free. It holds the
// host-wide port lock until an engine on that range reports SERVING, so concurrent runs never pick the same range.
func freeBase(t *testing.T) int {
	t.Helper()
	lockPorts(t)
	for base := 20000 + os.Getpid()%5000; base < 32000; base += 20 {
		ok := true
		for _, offset := range []int{0, 1, 2, 3, 6, 10, -300, -299, -298, -297, -294} {
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+offset))
			if err != nil {
				ok = false
				break
			}
			_ = l.Close()
		}
		if ok {
			return base
		}
	}
	t.Fatal("no free port range")
	return 0
}

// ports is the [engine.ports] table for a frontend port, with the default offsets.
func ports(base int) string {
	return fmt.Sprintf("[engine.ports]\nfrontend = %d\nfrontendHttp = %d\ninternalFrontend = %d\nhistory = %d\n"+
		"matching = %d\nworker = %d\n", base, base+10, base+3, base+1, base+2, base+6)
}

func writeConfig(t *testing.T, dir, toml string) string {
	t.Helper()
	path := filepath.Join(dir, "runtime.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// start runs the engine with args and waits until its frontend reports SERVING to a CLI call with flags.
func start(t *testing.T, binary string, base int, flags []string, args ...string) *engine {
	t.Helper()
	e, err := launch(t, binary, base, flags, args...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// launch is start, returning an engine that stopped or never reported SERVING as an error.
func launch(t *testing.T, binary string, base int, flags []string, args ...string) (*engine, error) {
	t.Helper()
	lockPorts(t) // already held when freeBase or a restart took it
	e := &engine{t: t, log: &bytes.Buffer{}, address: fmt.Sprintf("127.0.0.1:%d", base), flags: flags}
	e.cmd = exec.Command(binary, args...) //nolint:gosec // the engine under test
	e.cmd.Stdout, e.cmd.Stderr = e.log, e.log
	if err := e.cmd.Start(); err != nil {
		return nil, err
	}
	e.done = make(chan struct{})
	go func() { _ = e.cmd.Wait(); close(e.done) }()
	t.Cleanup(e.stop)
	deadline := time.Now().Add(startTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-e.done:
			return nil, fmt.Errorf("the engine exited before it reported SERVING:\n%s", e.log)
		default:
		}
		if out, err := e.temporal("operator", "cluster", "health"); err == nil && strings.Contains(out, "SERVING") {
			unlockPorts()
			return e, nil
		}
		time.Sleep(time.Second)
	}
	e.stop() // free its ports before a caller starts another engine on them
	return nil, fmt.Errorf("the engine did not report SERVING within %s:\n%s", startTimeout, e.log)
}

// stop ends the engine with SIGTERM, as a service manager does, and waits for it.
func (e *engine) stop() {
	select {
	case <-e.done:
		return
	default:
	}
	if err := e.cmd.Process.Signal(syscall.SIGTERM); err != nil { // Windows cannot send SIGTERM
		_ = e.cmd.Process.Kill()
		<-e.done
		return
	}
	select {
	case <-e.done:
	case <-time.After(30 * time.Second):
		_ = e.cmd.Process.Kill()
		<-e.done
	}
}

// temporal runs the Temporal CLI against this engine's frontend with its flags.
func (e *engine) temporal(args ...string) (string, error) {
	return e.temporalAt(e.address, e.flags, args...)
}

// temporalAt runs the Temporal CLI against any address with the given flags.
func (e *engine) temporalAt(address string, flags []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	all := append(append(append([]string{}, args...), "--address", address), flags...)
	cmd := exec.CommandContext(ctx, "temporal", all...) //nolint:gosec // test calls
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// workflowInfo is the part of `temporal workflow describe -o json` the tests read.
type workflowInfo struct {
	RunID  string
	Status string
}

// workflow describes a workflow through the Temporal CLI.
func (e *engine) workflow(namespace, id string) workflowInfo {
	e.t.Helper()
	out := e.must("workflow", "describe", "--namespace", namespace, "--workflow-id", id, "-o", "json")
	var doc struct {
		WorkflowExecutionInfo struct {
			Execution struct {
				RunID string `json:"runId"`
			} `json:"execution"`
			Status string `json:"status"`
		} `json:"workflowExecutionInfo"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		e.t.Fatalf("unexpected describe output: %v\n%s", err, out)
	}
	return workflowInfo{RunID: doc.WorkflowExecutionInfo.Execution.RunID, Status: doc.WorkflowExecutionInfo.Status}
}

// must runs the Temporal CLI and fails the test on an error.
func (e *engine) must(args ...string) string {
	e.t.Helper()
	out, err := e.temporal(args...)
	if err != nil {
		e.t.Fatalf("temporal %s: %v\n%s\nengine log:\n%s", strings.Join(args, " "), err, out, e.log)
	}
	return out
}

// tomlString quotes s as a TOML basic string; a Windows path's backslashes would otherwise be escapes.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestTOMLString(t *testing.T) {
	if got, want := tomlString("C:\\tmp\\\"a\"\a\x7fé"), `"C:\\tmp\\\"a\"\u0007\u007Fé"`; got != want {
		t.Fatalf("tomlString = %s, want %s", got, want)
	}
}
