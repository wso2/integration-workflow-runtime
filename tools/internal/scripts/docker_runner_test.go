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

package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeTool records its name and arguments in $TOOL_LOG and succeeds.
const fakeTool = "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> \"$TOOL_LOG\"\n"

// dockerRunner runs tests/docker/run.sh outside Docker: its source is a linked worktree of a fresh repository, and
// make and go are fakes, so the test covers the runner's own logic.
type dockerRunner struct {
	t                      *testing.T
	worktree, work, out    string
	toolLog, home, fakeBin string
	env                    []string
}

func newDockerRunner(t *testing.T) *dockerRunner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("tests/docker/run.sh is a bash script")
	}
	root := t.TempDir()
	d := &dockerRunner{t: t, worktree: filepath.Join(root, "worktree"), work: filepath.Join(root, "work"),
		out: filepath.Join(root, "out"), toolLog: filepath.Join(root, "tools.log"), home: filepath.Join(root, "home"),
		fakeBin: filepath.Join(root, "bin"), env: cleanEnv(t)}
	for _, dir := range []string{d.home, d.fakeBin} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"make", "go"} {
		if err := os.WriteFile(filepath.Join(d.fakeBin, tool), []byte(fakeTool), 0o700); err != nil { //nolint:gosec // a test fake
			t.Fatal(err)
		}
	}
	main := filepath.Join(root, "main")
	d.git(root, "init", "-q", "-b", "main", main)
	d.git(main, "config", "user.email", "runner@example.com")
	d.git(main, "config", "user.name", "Runner Test")
	d.git(main, "config", "commit.gpgsign", "false")
	for name, content := range map[string]string{"tracked.txt": "tracked\n", "engine/go.mod": "module engine\n"} {
		path := filepath.Join(main, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d.git(main, "add", "tracked.txt", "engine/go.mod")
	d.git(main, "commit", "-q", "-m", "chore: start")
	d.git(main, "worktree", "add", "-q", "-b", "feature", d.worktree)
	if err := os.WriteFile(filepath.Join(d.worktree, "untracked.txt"), []byte("local change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d *dockerRunner) git(dir string, args ...string) string {
	d.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, append(d.env, "HOME="+d.home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (d *dockerRunner) run(src string, args ...string) (code int, output, tools string) {
	d.t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "tests", "docker", "run.sh"))
	if err != nil {
		d.t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{script}, args...)...) //nolint:gosec // the script under test
	cmd.Env = append(d.env, "HOME="+d.home, "PATH="+d.fakeBin+":"+os.Getenv("PATH"), "TOOL_LOG="+d.toolLog,
		"RUNNER_SRC="+src, "RUNNER_WORK="+d.work, "RUNNER_OUT="+d.out)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		d.t.Fatal(err)
	}
	log, _ := os.ReadFile(d.toolLog)
	return code, string(out), string(log)
}

func TestDockerRunnerWorksFromALinkedWorktree(t *testing.T) {
	d := newDockerRunner(t)
	code, out, tools := d.run(d.worktree, "gates")
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if !strings.Contains(tools, "make check sbom") {
		t.Errorf("tools = %q, want make check sbom", tools)
	}
	if info, err := os.Stat(filepath.Join(d.work, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("the work copy has no Git directory of its own: %v", err)
	}
	if got, want := d.git(d.work, "rev-parse", "HEAD"), d.git(d.worktree, "rev-parse", "HEAD"); got != want {
		t.Errorf("work HEAD = %s, want the worktree's %s", got, want)
	}
	if status := d.git(d.work, "status", "--porcelain", "tracked.txt"); status != "" {
		t.Errorf("tracked file differs from HEAD in the work copy: %q", status)
	}
	if _, err := os.Stat(filepath.Join(d.work, "untracked.txt")); err != nil {
		t.Errorf("the untracked file was not copied: %v", err)
	}
}

func TestDockerRunnerTargets(t *testing.T) {
	d := newDockerRunner(t)
	code, out, tools := d.run(d.worktree, "smoke", "sqlite mysql")
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{"make build", "go run ./tools/cmd/balsuite smoke --database sqlite",
		"go run ./tools/cmd/balsuite smoke --database mysql"} {
		if !strings.Contains(tools, want) {
			t.Errorf("tools lack %q:\n%s", want, tools)
		}
	}
	if strings.Contains(tools, "make check") || strings.Contains(tools, "balsuite full") {
		t.Errorf("smoke ran more than the smoke suite:\n%s", tools)
	}
}

func TestDockerRunnerRefusals(t *testing.T) {
	d := newDockerRunner(t)
	if code, out, _ := d.run(d.worktree, "everything"); code != 2 || !strings.Contains(out, "unknown target: everything") {
		t.Errorf("unknown target: exit %d\n%s", code, out)
	}
	noGit := t.TempDir()
	if code, out, _ := d.run(noGit, "gates"); code != 1 || !strings.Contains(out, "cannot read the Git metadata") {
		t.Errorf("source without Git metadata: exit %d\n%s", code, out)
	}
}
