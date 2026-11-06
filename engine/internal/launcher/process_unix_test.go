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

//go:build unix

package launcher

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitForSignal() int {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	fmt.Println("ready")
	<-signals
	return 42
}

func TestLauncherBecomesTheEngineProcess(t *testing.T) {
	_, launcher := processHome(t)
	cmd, _ := launcherCommand(launcher, "", "wait")
	cmd.Stdout = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines, pid := bufio.NewScanner(stdout), ""
	for lines.Scan() && lines.Text() != "ready" {
		if after, ok := strings.CutPrefix(lines.Text(), "pid="); ok {
			pid = after
		}
	}
	// The engine replaced the launcher, so the caller's signal goes straight to the engine.
	if pid != fmt.Sprint(cmd.Process.Pid) {
		t.Errorf("engine pid = %s, want the launcher's %d", pid, cmd.Process.Pid)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the engine did not exit on SIGTERM")
	}
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("exit code = %d, want the engine's 42", code)
	}
}
