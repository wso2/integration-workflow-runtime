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

package balsuite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// servingTemporal answers every operator call successfully and reports SERVING.
const servingTemporal = "#!/bin/sh\ncase \"$2 $3\" in 'cluster health') echo SERVING;; esac\nexit 0\n"

// idleEngine runs until terminated.
const idleEngine = "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"

func script(t *testing.T, dir, name, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fakes are shell scripts")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil { //nolint:gosec // an executable test fake
		t.Fatal(err)
	}
	return path
}

func options(t *testing.T, temporal, engine, bal string) Options {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests", "ballerina", "smoke"), 0o750); err != nil {
		t.Fatal(err)
	}
	return Options{
		Suite:          "smoke",
		Database:       "sqlite",
		Root:           root,
		Engine:         script(t, root, "engine", engine),
		EngineArgs:     []string{"--allow-no-auth"},
		Bal:            script(t, root, "bal", bal),
		Temporal:       script(t, root, "temporal", temporal),
		LogDir:         filepath.Join(root, "logs"),
		StartupTimeout: 10 * time.Second,
		TestTimeout:    10 * time.Second,
	}
}

func TestRunPassesWhenTheSuitePasses(t *testing.T) {
	o := options(t, servingTemporal, idleEngine, "#!/bin/sh\necho \"$@\" > \"$PWD/ran\"\nexit 0\n")
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), o, &stdout, &stderr); code != 0 {
		t.Fatalf("Run = %d, want 0; stderr: %s", code, stderr.String())
	}
	ran, err := os.ReadFile(filepath.Join(o.Root, "tests", "ballerina", "smoke", "ran"))
	if err != nil || strings.TrimSpace(string(ran)) != "test" {
		t.Fatalf("bal was run with %q (%v), want `test` in the suite directory", ran, err)
	}
	if !strings.Contains(stdout.String(), "SERVING on "+Address+" (sqlite); running the smoke suite") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(o.LogDir, "engine-smoke-sqlite.log")); err != nil {
		t.Fatalf("engine log: %v", err)
	}
}

func TestRunReturnsTheSuiteExitCodeAndPrintsTheLogTail(t *testing.T) {
	engine := "#!/bin/sh\necho engine says hello\n" + idleEngine
	o := options(t, servingTemporal, engine, "#!/bin/sh\nexit 3\n")
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), o, &stdout, &stderr); code != 3 {
		t.Fatalf("Run = %d, want 3", code)
	}
	if !strings.Contains(stderr.String(), "engine says hello") {
		t.Fatalf("stderr = %q, want the engine log tail", stderr.String())
	}
}

func TestRunRemovesTheTemporaryLogDirectory(t *testing.T) {
	for _, tt := range []struct {
		name string
		bal  string
		code int
	}{
		{"passing suite", "#!/bin/sh\nexit 0\n", 0},
		{"failing suite", "#!/bin/sh\nexit 3\n", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			o := options(t, servingTemporal, "#!/bin/sh\necho engine says hello\n"+idleEngine, tt.bal)
			o.LogDir = ""
			var stderr bytes.Buffer
			if code := Run(context.Background(), o, &bytes.Buffer{}, &stderr); code != tt.code {
				t.Fatalf("Run = %d, want %d; stderr: %s", code, tt.code, stderr.String())
			}
			if tt.code != 0 && !strings.Contains(stderr.String(), "engine says hello") {
				t.Fatalf("stderr = %q, want the log tail before the directory is removed", stderr.String())
			}
			left, err := filepath.Glob(filepath.Join(tmp, "workflow-engine-*"))
			if err != nil || len(left) != 0 {
				t.Fatalf("temporary log directories left behind: %v (%v)", left, err)
			}
		})
	}
}

func TestRunRunsPrepareBeforeTheEngine(t *testing.T) {
	o := options(t, servingTemporal, idleEngine, "#!/bin/sh\nexit 0\n")
	o.Prepare = func(context.Context) error { return errors.New("schema setup failed") }
	var stderr bytes.Buffer
	if code := Run(context.Background(), o, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "schema setup failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunFailsWhenTheEngineExitsEarly(t *testing.T) {
	o := options(t, "#!/bin/sh\nexit 1\n", "#!/bin/sh\nexit 7\n", "#!/bin/sh\nexit 0\n")
	var stderr bytes.Buffer
	if code := Run(context.Background(), o, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "exited with code 7 before it was ready") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunFailsWhenTheEngineNeverServes(t *testing.T) {
	o := options(t, "#!/bin/sh\nexit 1\n", idleEngine, "#!/bin/sh\nexit 0\n")
	o.StartupTimeout = 2 * time.Second
	var stderr bytes.Buffer
	if code := Run(context.Background(), o, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "was not SERVING") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunFailsWhenTheEngineCannotStart(t *testing.T) {
	o := options(t, servingTemporal, idleEngine, "#!/bin/sh\nexit 0\n")
	o.Engine = filepath.Join(o.Root, "absent")
	var stderr bytes.Buffer
	if code := Run(context.Background(), o, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unable to start") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestEnsureNamespaceAcceptsExistingNamespaceAndAttribute(t *testing.T) {
	temporal := script(t, t.TempDir(), "temporal", "#!/bin/sh\n"+
		"case \"$2 $3\" in 'namespace create'|'search-attribute create') echo 'already exists' >&2; exit 1;; esac\nexit 0\n")
	if err := ensureNamespace(context.Background(), temporal, 5*time.Second); err != nil {
		t.Fatalf("ensureNamespace = %v, want nil", err)
	}
}

func TestEnsureNamespaceReportsFailures(t *testing.T) {
	tests := []struct {
		name, failing, want string
	}{
		{"create", "namespace create", "could not create namespace"},
		{"attribute", "search-attribute create", "could not add search attribute WorkflowKind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temporal := script(t, t.TempDir(), "temporal", "#!/bin/sh\n"+
				"case \"$2 $3\" in '"+tt.failing+"') echo 'permission denied' >&2; exit 1;; esac\nexit 0\n")
			err := ensureNamespace(context.Background(), temporal, 5*time.Second)
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("ensureNamespace = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestEnsureNamespaceTimesOutWhenDescribeKeepsFailing(t *testing.T) {
	temporal := script(t, t.TempDir(), "temporal", "#!/bin/sh\ncase \"$2 $3\" in 'namespace describe') exit 1;; esac\nexit 0\n")
	err := ensureNamespace(context.Background(), temporal, 0)
	if err == nil || !strings.Contains(err.Error(), "did not become available") {
		t.Fatalf("ensureNamespace = %v, want the availability timeout", err)
	}
}

func TestRunQuietTurnsATimeoutIntoAFailedCall(t *testing.T) {
	sleep := script(t, t.TempDir(), "slow", "#!/bin/sh\nsleep 30\n")
	r := runQuiet(context.Background(), 200*time.Millisecond, sleep)
	if r.code != exitCodeTimeout || !strings.Contains(r.stderr, "timed out after 0s") {
		t.Fatalf("runQuiet = %+v, want the timeout result", r)
	}
}

func TestRunQuietReportsAMissingCommand(t *testing.T) {
	r := runQuiet(context.Background(), time.Second, filepath.Join(t.TempDir(), "absent"))
	if r.code != 127 || r.stderr == "" {
		t.Fatalf("runQuiet = %+v, want code 127 with a message", r)
	}
}

func TestRunBalTestReportsAnUnstartableCommand(t *testing.T) {
	var stderr bytes.Buffer
	if code := runBalTest(filepath.Join(t.TempDir(), "absent"), t.TempDir(), time.Second, &stderr); code != 1 {
		t.Fatalf("runBalTest = %d, want 1", code)
	}
}

func TestEngineStopKillsAnEngineThatIgnoresTerminate(t *testing.T) {
	engine := script(t, t.TempDir(), "engine", "#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n")
	p, err := startEngine(engine, nil, &bytes.Buffer{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { p.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(6 * time.Second):
		t.Fatal("stop did not return after the kill fallback")
	}
	if done, _ := p.exitedWith(); !done {
		t.Fatal("the engine is still running")
	}
}

func TestPrintLogTailPrintsTheLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printLogTail(&out, path, 2)
	if out.String() != "--- last 2 lines of "+path+" ---\ntwo\nthree\n" {
		t.Fatalf("output = %q", out.String())
	}
	printLogTail(&out, filepath.Join(t.TempDir(), "absent.log"), 2)
}
