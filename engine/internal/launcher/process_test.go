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

package launcher

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// forwardedEnv is set for the launcher process and must reach the engine.
const forwardedEnv = "LAUNCHER_TEST_FORWARDED"

// TestMain lets copies of the test binary named workflow-runtime and workflow-engine act as the real launcher and a
// fake engine, so the handoff between two processes runs for real.
func TestMain(m *testing.M) {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "workflow-runtime":
		os.Exit(Main("test", os.Args[1:]))
	case "workflow-engine":
		os.Exit(fakeEngine(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeEngine reports what it received; "exit N" sets its exit code and "wait" blocks until it is signalled.
func fakeEngine(args []string) int {
	stdin, _ := io.ReadAll(os.Stdin)
	dir, _ := os.Getwd()
	fmt.Printf("args=%s\nenv=%s\nstdin=%s\ndir=%s\npid=%d\n", strings.Join(args, " "), os.Getenv(forwardedEnv),
		stdin, dir, os.Getpid())
	if len(args) > 1 && args[0] == "exit" {
		code, _ := strconv.Atoi(args[1])
		return code
	}
	if len(args) > 0 && args[0] == "wait" {
		return waitForSignal()
	}
	return 0
}

// processHome is a distribution whose bin holds copies of this test binary as the launcher and the engine.
func processHome(t *testing.T) (home, launcher string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, name := range []string{"workflow-runtime", "workflow-engine"} {
		copyExecutable(t, self, filepath.Join(bin, name+ext))
	}
	return home, filepath.Join(bin, "workflow-runtime"+ext)
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from) //nolint:gosec // this test binary
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o700); err != nil { //nolint:gosec // an executable copy
		t.Fatal(err)
	}
}

func launcherCommand(launcher, stdin string, args ...string) (*exec.Cmd, *bytes.Buffer) {
	cmd := exec.Command(launcher, args...) //nolint:gosec // the test's own launcher copy
	cmd.Env = append(os.Environ(), forwardedEnv+"=forwarded")
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	return cmd, &out
}

func TestLauncherHandsOverToTheEngineProcess(t *testing.T) {
	home, launcher := processHome(t)
	cmd, out := launcherCommand(launcher, "from the caller", "exit", "7")
	err := cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Fatalf("exit code = %d (%v), want the engine's 7:\n%s", code, err, out)
	}
	for _, want := range []string{"args=exit 7\n", "env=forwarded\n", "stdin=from the caller\n", "dir=" + home + "\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the engine did not receive %q:\n%s", want, out)
		}
	}
}

func TestLauncherReportsAMissingEngine(t *testing.T) {
	home, launcher := processHome(t)
	engine := filepath.Join(home, "bin", "workflow-engine")
	if runtime.GOOS == "windows" {
		engine += ".exe"
	}
	if err := os.Remove(engine); err != nil {
		t.Fatal(err)
	}
	cmd, out := launcherCommand(launcher, "", "exit", "7")
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 || !strings.Contains(out.String(), "unable to run "+engine) {
		t.Fatalf("exit code = %d, want 1 and the engine named:\n%s", code, out)
	}
}
