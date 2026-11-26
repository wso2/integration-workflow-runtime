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

//go:build windows

package launcher

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitForSignal blocks until the engine is ended from outside, which the test does by killing the launcher.
func waitForSignal() int {
	fmt.Println("ready")
	time.Sleep(time.Minute)
	return 1
}

func TestTheEngineEndsWithAKilledLauncher(t *testing.T) {
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
	lines, pid := bufio.NewScanner(stdout), 0
	for lines.Scan() && lines.Text() != "ready" {
		if after, ok := strings.CutPrefix(lines.Text(), "pid="); ok {
			pid, _ = strconv.Atoi(after)
		}
	}
	if pid == 0 {
		t.Fatal("the engine did not report its pid")
	}
	// TerminateProcess ends only the launcher, as a service wrapper or taskkill without /T does.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	const synchronize = 0x00100000
	engine, err := syscall.OpenProcess(synchronize, false, uint32(pid)) //nolint:gosec // a pid
	if err != nil {
		return // already gone
	}
	defer syscall.CloseHandle(engine) //nolint:errcheck // a test handle
	if event, _ := syscall.WaitForSingleObject(engine, 10_000); event != syscall.WAIT_OBJECT_0 {
		t.Fatal("the engine kept running after the launcher was killed")
	}
}
