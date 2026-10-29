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

// Package balsuite runs a Ballerina test suite (smoke or full) against a freshly started workflow-engine binary:
// it starts the engine, waits until its frontend is SERVING, creates the default namespace and the search
// attributes ballerina/workflow uses, runs `bal test` in the suite, and stops the engine.
package balsuite

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Address is the engine frontend the suites' Config.toml points at.
	Address   = "localhost:17233"
	namespace = "default"
	// cliTimeout bounds every Temporal CLI call, so a hanging call fails instead of blocking the run.
	cliTimeout      = 15 * time.Second
	logTailLines    = 80
	exitCodeTimeout = 124
	// stopTimeout is how long a terminated engine gets before it is killed.
	stopTimeout = 30 * time.Second
	// namespaceTimeout bounds the wait for the created namespace to become available.
	namespaceTimeout = 30 * time.Second
)

// searchAttributes are the attributes ballerina/workflow tags every instance with; a fresh namespace lacks them.
var searchAttributes = map[string]string{"WorkflowKind": "Keyword"}

// Options describes one run.
type Options struct {
	Suite      string   // smoke or full
	Database   string   // sqlite, postgresql or mysql: names the log file and the SERVING message
	Root       string   // repository root; the suite is tests/ballerina/<Suite>
	Engine     string   // workflow-engine binary
	EngineArgs []string // arguments after `start`
	Bal        string   // bal command
	Temporal   string   // temporal CLI
	LogDir     string   // engine log directory; a temporary one, removed after the run, when empty
	// Prepare runs after the engine binary is known and before it starts, for example to create SQL schemas.
	Prepare        func(ctx context.Context) error
	StartupTimeout time.Duration
	TestTimeout    time.Duration
}

type result struct {
	code   int
	stdout string
	stderr string
}

func (r result) output() string { return strings.ToLower(r.stdout + r.stderr) }

func (r result) message() string { return strings.TrimSpace(firstNonEmpty(r.stderr, r.stdout)) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// runQuiet runs a short CLI call with a timeout; a call that hangs counts as a failed call.
func runQuiet(ctx context.Context, timeout time.Duration, name string, args ...string) result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var stdout, stderr strings.Builder
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		r.code, r.stderr = exitCodeTimeout, fmt.Sprintf("timed out after %.0fs", timeout.Seconds())
	default:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			r.code = exit.ExitCode()
		} else {
			r.code, r.stderr = 127, err.Error()
		}
	}
	return r
}

// engineProcess is the started engine with its exit watched once.
type engineProcess struct {
	cmd         *exec.Cmd
	exited      chan error
	done        bool
	err         error
	stopTimeout time.Duration // how long stop waits after terminating before it kills
}

func startEngine(engine string, args []string, log io.Writer, stopTimeout time.Duration) (*engineProcess, error) {
	cmd := exec.Command(engine, append([]string{"start"}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("unable to start %s: %w", engine, err)
	}
	p := &engineProcess{cmd: cmd, exited: make(chan error, 1), stopTimeout: stopTimeout}
	go func() { p.exited <- cmd.Wait() }()
	return p, nil
}

// exitedWith reports whether the engine has exited and, if so, its exit code.
func (p *engineProcess) exitedWith() (bool, int) {
	if !p.done {
		select {
		case p.err = <-p.exited:
			p.done = true
		default:
			return false, 0
		}
	}
	return true, p.cmd.ProcessState.ExitCode()
}

func (p *engineProcess) stop() {
	if done, _ := p.exitedWith(); done {
		return
	}
	_ = terminate(p.cmd)
	select {
	case p.err = <-p.exited:
	case <-time.After(p.stopTimeout):
		_ = p.cmd.Process.Kill()
		p.err = <-p.exited
	}
	p.done = true
}

func waitUntilServing(ctx context.Context, temporal string, engine *engineProcess, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done, code := engine.exitedWith(); done {
			return fmt.Errorf("workflow-engine exited with code %d before it was ready", code)
		}
		r := runQuiet(ctx, cliTimeout, temporal, "operator", "cluster", "health", "--address", Address)
		if r.code == 0 && strings.Contains(r.stdout, "SERVING") {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("workflow-engine was not SERVING on %s after %.0fs", Address, timeout.Seconds())
}

func ensureNamespace(ctx context.Context, temporal string, timeout time.Duration) error {
	create := runQuiet(ctx, cliTimeout, temporal, "operator", "namespace", "create", "--namespace", namespace,
		"--retention", "24h", "--address", Address)
	if create.code != 0 && !strings.Contains(create.output(), "already") {
		return fmt.Errorf("could not create namespace %s: %s", namespace, create.message())
	}
	deadline := time.Now().Add(timeout)
	for runQuiet(ctx, cliTimeout, temporal, "operator", "namespace", "describe", "--namespace", namespace,
		"--address", Address).code != 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("namespace %s did not become available", namespace)
		}
		time.Sleep(time.Second)
	}
	for name, kind := range searchAttributes {
		added := runQuiet(ctx, cliTimeout, temporal, "operator", "search-attribute", "create", "--namespace", namespace,
			"--name", name, "--type", kind, "--address", Address)
		if added.code != 0 && !strings.Contains(added.output(), "already") {
			return fmt.Errorf("could not add search attribute %s: %s", name, added.message())
		}
	}
	return nil
}

// runBalTest runs `bal test`; on timeout the whole process group dies, since `bal` leaves its JVM running otherwise.
func runBalTest(bal string, suiteDir string, timeout time.Duration, stderr io.Writer) int {
	cmd := exec.Command(bal, "test")
	cmd.Dir = suiteDir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: unable to start %s: %v\n", bal, err)
		return 1
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
		return cmd.ProcessState.ExitCode()
	case <-time.After(timeout):
		_ = killProcessGroup(cmd)
		<-exited
		_, _ = fmt.Fprintf(stderr, "error: bal test did not finish within %.0fs\n", timeout.Seconds())
		return exitCodeTimeout
	}
}

func printLogTail(w io.Writer, path string, lines int) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	var content []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		content = append(content, scanner.Text())
	}
	if len(content) > lines {
		content = content[len(content)-lines:]
	}
	_, _ = fmt.Fprintf(w, "--- last %d lines of %s ---\n", len(content), path)
	for _, line := range content {
		_, _ = fmt.Fprintln(w, line)
	}
}

// Run executes the suite and returns the process exit code.
func Run(ctx context.Context, o Options, stdout, stderr io.Writer) int {
	suiteDir := filepath.Join(o.Root, "tests", "ballerina", o.Suite)
	logDir := o.LogDir
	if logDir == "" {
		dir, err := os.MkdirTemp("", "workflow-engine-")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		logDir = dir
		defer func() { _ = os.RemoveAll(dir) }()
	}
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	logPath := filepath.Join(logDir, "engine-"+o.Suite+"-"+o.Database+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer func() { _ = logFile.Close() }()

	if o.Prepare != nil {
		if err := o.Prepare(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	engine, err := startEngine(o.Engine, o.EngineArgs, logFile, stopTimeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	code := func() int {
		if err := waitUntilServing(ctx, o.Temporal, engine, o.StartupTimeout); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if err := ensureNamespace(ctx, o.Temporal, namespaceTimeout); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "workflow-engine SERVING on %s (%s); running the %s suite\n", Address, o.Database, o.Suite)
		return runBalTest(o.Bal, suiteDir, o.TestTimeout, stderr)
	}()
	engine.stop()
	if code != 0 {
		_ = logFile.Sync()
		printLogTail(stderr, logPath, logTailLines)
	}
	return code
}
