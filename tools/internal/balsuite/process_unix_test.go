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

//go:build !windows

package balsuite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunBalTestKillsTheProcessGroupOnTimeout(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child.pid")
	bal := script(t, dir, "bal", "#!/bin/sh\nsleep 30 &\necho $! > "+marker+"\nwait\n")
	var stderr bytes.Buffer
	if code := runBalTest(context.Background(), bal, dir, 3*time.Second, &stderr); code != exitCodeTimeout {
		t.Fatalf("runBalTest = %d, want %d", code, exitCodeTimeout)
	}
	if !strings.Contains(stderr.String(), "did not finish within 3s") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	waitUntilGone(t, marker)
}

func TestRunBalTestKillsTheProcessGroupWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child.pid")
	bal := script(t, dir, "bal", "#!/bin/sh\nsleep 30 &\necho $! > "+marker+"\nwait\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	if code := runBalTest(ctx, bal, dir, time.Minute, &stderr); code != exitCodeCanceled {
		t.Fatalf("runBalTest = %d, want %d", code, exitCodeCanceled)
	}
	if !strings.Contains(stderr.String(), "the run was canceled") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	waitUntilGone(t, marker)
}

// waitUntilGone fails unless the process whose ID the fake bal wrote to marker ends.
func waitUntilGone(t *testing.T, marker string) {
	t.Helper()
	pid, err := os.ReadFile(marker) //nolint:gosec // a test file
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(pid)))
	if err != nil {
		t.Fatal(err)
	}
	for range 50 { // the killed child is reaped by init, not instantly
		if err := syscall.Kill(child, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("child %d of the stopped bal test is still running", child)
}

func TestDockerRunRemovesTheContainerWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	docker := script(t, dir, "docker", "#!/bin/sh\necho \"$*\" >> "+calls+"\ncase \"$1\" in run) sleep 30;; esac\n")
	r := &upgradeRun{o: UpgradeOptions{Docker: docker}, container: "workflow-upgrade-old-test", stderr: &bytes.Buffer{}}
	ctx, cancel := context.WithCancel(context.Background()) // as SIGINT does; a deadline reads as a timeout
	defer time.AfterFunc(time.Second, cancel).Stop()
	if res := r.dockerRun(ctx, nil, "busybox", "true"); res.code == 0 {
		t.Fatal("dockerRun = 0 for a canceled run")
	}
	got, err := os.ReadFile(calls) //nolint:gosec // a test file
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "rm -f workflow-upgrade-old-test-1") {
		t.Fatalf("docker calls = %q, want the canceled container removed", got)
	}
}
