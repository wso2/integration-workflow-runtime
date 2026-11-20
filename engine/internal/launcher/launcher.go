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

// Package launcher is the workflow-runtime command: it finds the runtime's home from its own location, works from
// there so relative paths in runtime.toml resolve against it, and hands the command to workflow-engine.
package launcher

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigEnv names another runtime.toml than <home>/conf/runtime.toml.
const ConfigEnv = "WORKFLOW_RUNTIME_CONFIG"

const usageText = `usage: workflow-runtime [start] [--allow-no-auth] [--service <name>]...
       workflow-runtime dev [--db-file <file>] [--namespace <name>]... [--port <port>] [--ip <address>]
       workflow-runtime render-config
       workflow-runtime version

Starts the engine from <home>/conf/runtime.toml, or from the file %s names.
`

// executor replaces this process with the engine, or runs it, and returns the exit code when it comes back.
type executor func(engine string, args []string) (int, error)

// Main runs the command and returns the process exit code when it does not hand over to the engine.
func Main(version string, args []string) int {
	return run(version, args, os.Executable, os.Getenv, execEngine, os.Stdout, os.Stderr)
}

func run(version string, args []string, executable func() (string, error), getenv func(string) string,
	launch executor, stdout, stderr io.Writer) int {
	home, err := homeDir(executable)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	command, rest := "start", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, rest = args[0], args[1:]
	}
	if command == "help" || (command == "start" && len(rest) == 1 && (rest[0] == "-h" || rest[0] == "--help")) {
		_, _ = fmt.Fprintf(stdout, usageText, ConfigEnv)
		return 0
	}
	config := getenv(ConfigEnv)
	if config == "" {
		config = filepath.Join(home, "conf", "runtime.toml")
	} else if abs, err := filepath.Abs(config); err == nil {
		// Relative to where the caller ran the command, not to the home it changes to.
		config = abs
	}
	var engineArgs []string
	switch command {
	case "start", "render-config":
		engineArgs = append([]string{command, "--runtime-config", config}, rest...)
	case "dev":
		engineArgs = append([]string{"start-dev"}, rest...)
	case "version":
		_, _ = fmt.Fprintf(stdout, "workflow-runtime %s\n", version)
		engineArgs = []string{"version"}
	default:
		engineArgs = append([]string{command}, rest...)
	}
	engine := filepath.Join(home, "bin", "workflow-engine")
	if runtime.GOOS == "windows" {
		engine += ".exe"
	}
	if err := os.Chdir(home); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	code, err := launch(engine, engineArgs)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "unable to run %s: %v\n", engine, err)
		return 1
	}
	return code
}

// homeDir is the directory above the one holding this executable, with symbolic links resolved.
func homeDir(executable func() (string, error)) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", fmt.Errorf("unable to find this executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(filepath.Dir(exe)), nil
}
