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

// fakeEngine prints its working directory and arguments, one per line, in place of workflow-engine.
const fakeEngine = "#!/bin/sh\npwd -P\nfor a in \"$@\"; do echo \"$a\"; done\n"

// runtimeHome is a distribution directory with the real start script and a fake engine.
func runtimeHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("workflow-runtime.sh is a POSIX shell script")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "distribution", "bin", "workflow-runtime.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"workflow-runtime.sh": script, "workflow-engine": []byte(fakeEngine)} {
		if err := os.WriteFile(filepath.Join(home, "bin", name), content, 0o700); err != nil { //nolint:gosec // test fakes
			t.Fatal(err)
		}
	}
	return home
}

func startScript(t *testing.T, home string, env []string, args ...string) []string {
	t.Helper()
	cmd := exec.Command(filepath.Join(home, "bin", "workflow-runtime.sh"), args...) //nolint:gosec // the script under test
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workflow-runtime.sh %v: %v\n%s", args, err, out)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func TestStartScriptStartsFromTheRuntimeHome(t *testing.T) {
	home := runtimeHome(t)
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(real, "conf", "runtime.toml")
	tests := map[string]struct {
		args []string
		want []string
	}{
		"no arguments":  {nil, []string{"start", "--runtime-config", config}},
		"start flags":   {[]string{"start", "--allow-no-auth"}, []string{"start", "--runtime-config", config, "--allow-no-auth"}},
		"render-config": {[]string{"render-config"}, []string{"render-config", "--runtime-config", config}},
		"version":       {[]string{"version"}, []string{"version"}},
	}
	for name, tt := range tests {
		got := startScript(t, home, nil, tt.args...)
		if got[0] != real {
			t.Errorf("%s: working directory = %q, want the runtime home %q", name, got[0], real)
		}
		if strings.Join(got[1:], " ") != strings.Join(tt.want, " ") {
			t.Errorf("%s: engine arguments = %q, want %q", name, got[1:], tt.want)
		}
	}
}

func TestStartScriptTakesAnotherConfiguration(t *testing.T) {
	home := runtimeHome(t)
	got := startScript(t, home, []string{"WORKFLOW_RUNTIME_CONFIG=/etc/workflow/runtime.toml"})
	if strings.Join(got[1:], " ") != "start --runtime-config /etc/workflow/runtime.toml" {
		t.Fatalf("engine arguments = %q", got[1:])
	}
}
