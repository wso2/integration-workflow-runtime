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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type launched struct {
	engine, dir string
	args        []string
}

// fakeHome is a distribution directory, with the launcher reached through a symbolic link where the OS allows it.
func fakeHome(t *testing.T) (home string, executable func() (string, error)) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "bin", "workflow-runtime")
	if err := os.WriteFile(exe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workflow-runtime")
	if runtime.GOOS != "windows" && os.Symlink(exe, link) == nil {
		exe = link
	}
	return home, func() (string, error) { return exe, nil }
}

func runLauncher(t *testing.T, env map[string]string, args ...string) (launched, int, string) {
	t.Helper()
	t.Chdir(".") // the launcher changes directory; restore it after the test
	home, executable := fakeHome(t)
	var got launched
	saved := launch
	t.Cleanup(func() { launch = saved })
	launch = func(engine string, a []string) (int, error) {
		d, _ := os.Getwd()
		got = launched{engine: engine, dir: d, args: a}
		return 3, nil
	}
	var out bytes.Buffer
	code := run("9.9.9", args, executable, func(k string) string { return env[k] }, &out, &out)
	if got.engine != "" && got.dir != home {
		t.Errorf("working directory = %q, want the home %q", got.dir, home)
	}
	if got.engine != "" && !strings.HasPrefix(got.engine, filepath.Join(home, "bin", "workflow-engine")) {
		t.Errorf("engine = %q", got.engine)
	}
	return got, code, out.String()
}

func TestLauncherCommands(t *testing.T) {
	for name, tt := range map[string]struct {
		args []string
		want []string
	}{
		"no arguments":  {nil, []string{"start", "--runtime-config", "CONFIG"}},
		"start flags":   {[]string{"--allow-no-auth"}, []string{"start", "--runtime-config", "CONFIG", "--allow-no-auth"}},
		"start":         {[]string{"start", "--service", "frontend"}, []string{"start", "--runtime-config", "CONFIG", "--service", "frontend"}},
		"render-config": {[]string{"render-config"}, []string{"render-config", "--runtime-config", "CONFIG"}},
		"other":         {[]string{"start-dev", "--port", "8233"}, []string{"start-dev", "--port", "8233"}},
	} {
		got, code, _ := runLauncher(t, nil, tt.args...)
		home := filepath.Dir(filepath.Dir(got.engine))
		want := slices.Clone(tt.want)
		for i, a := range want {
			if a == "CONFIG" {
				want[i] = filepath.Join(home, "conf", "runtime.toml")
			}
		}
		if !slices.Equal(got.args, want) || code != 3 {
			t.Errorf("%s: engine arguments = %q (exit %d), want %q", name, got.args, code, want)
		}
	}
}

func TestLauncherTakesAnotherConfiguration(t *testing.T) {
	got, _, _ := runLauncher(t, map[string]string{ConfigEnv: "/etc/workflow/runtime.toml"})
	if !slices.Equal(got.args, []string{"start", "--runtime-config", "/etc/workflow/runtime.toml"}) {
		t.Fatalf("engine arguments = %q", got.args)
	}
}

func TestLauncherResolvesARelativeConfigurationFirst(t *testing.T) {
	caller := t.TempDir()
	t.Chdir(caller)
	want, err := filepath.Abs("custom.toml")
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := runLauncher(t, map[string]string{ConfigEnv: "custom.toml"})
	if !slices.Equal(got.args, []string{"start", "--runtime-config", want}) {
		t.Fatalf("engine arguments = %q, want the caller's %s", got.args, want)
	}
}

func TestLauncherVersionAndHelp(t *testing.T) {
	got, _, out := runLauncher(t, nil, "version")
	if !strings.Contains(out, "workflow-runtime 9.9.9") || !slices.Equal(got.args, []string{"version"}) {
		t.Fatalf("version: out = %q, args = %q", out, got.args)
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"start", "-h"}} {
		got, code, out := runLauncher(t, nil, args...)
		if code != 0 || got.engine != "" || !strings.Contains(out, "usage: workflow-runtime") {
			t.Errorf("%q: code = %d, engine = %q, out = %q", args, code, got.engine, out)
		}
	}
}

func TestLauncherErrors(t *testing.T) {
	var out bytes.Buffer
	if code := run("v", nil, func() (string, error) { return "", errors.New("gone") }, os.Getenv, &out, &out); code != 1 ||
		!strings.Contains(out.String(), "unable to find this executable") {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	home, executable := fakeHome(t)
	saved := launch
	t.Cleanup(func() { launch = saved })
	launch = func(string, []string) (int, error) { return 0, errors.New("permission denied") }
	t.Chdir(".")
	out.Reset()
	if code := run("v", nil, executable, func(string) string { return "" }, &out, &out); code != 1 ||
		!strings.Contains(out.String(), "permission denied") {
		t.Fatalf("code = %d, out = %q (home %s)", code, out.String(), home)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run("v", nil, executable, func(string) string { return "" }, &out, &out); code != 1 {
		t.Fatalf("a missing home: code = %d", code)
	}
}
