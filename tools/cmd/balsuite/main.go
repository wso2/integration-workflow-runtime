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

// Command balsuite runs a Ballerina suite against the engine started from tests/engine/runtime-<database>.toml:
// balsuite <smoke|full> --engine build/workflow-engine [--database sqlite|postgresql|mysql]
// balsuite upgrade --engine build/workflow-engine --database postgresql|mysql [--old-temporal 1.30.9] runs a workflow
// across an upgrade from that Temporal release to the engine.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/wso2/integration-workflow-runtime/tools/internal/balsuite"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("balsuite", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	engine := flags.String("engine", "", "path to the workflow-engine binary (required)")
	database := flags.String("database", "sqlite",
		"sqlite, postgresql or mysql: which tests/engine/runtime-<database>.toml to use; dev runs start-dev instead")
	bal := flags.String("bal", envOr("BAL", "bal"), "jBallerina `bal` command")
	temporal := flags.String("temporal", envOr("TEMPORAL_CLI", "temporal"), "Temporal CLI")
	startup := flags.Duration("startup-timeout", 90*time.Second, "limit for the engine to become SERVING")
	testTimeout := flags.Duration("test-timeout", 30*time.Minute, "limit for `bal test`")
	docker := flags.String("docker", envOr("DOCKER", "docker"), "docker command (upgrade only)")
	oldTemporal := flags.String("old-temporal", "1.30.9", "Temporal release the upgrade starts from (upgrade only)")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: balsuite <smoke|full|upgrade> --engine <workflow-engine> [--database sqlite|postgresql|mysql|dev] [flags]")
		flags.PrintDefaults()
	}
	if len(args) == 0 || !slices.Contains([]string{"smoke", "full", balsuite.UpgradeSuite}, args[0]) {
		flags.Usage()
		return 2
	}
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *engine == "" {
		fmt.Fprintln(os.Stderr, "--engine is required")
		return 2
	}
	if !slices.Contains([]string{"sqlite", "postgresql", "mysql", "dev"}, *database) {
		fmt.Fprintf(os.Stderr, "unknown database %q\n", *database)
		return 2
	}
	enginePath, err := filepath.Abs(*engine)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if args[0] == balsuite.UpgradeSuite {
		// Ctrl-C or SIGTERM cancels the run, and RunUpgrade still removes the old server and the databases.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return balsuite.RunUpgrade(ctx, balsuite.UpgradeOptions{
			Database:       *database,
			Root:           rootPath,
			Engine:         enginePath,
			Bal:            *bal,
			Temporal:       *temporal,
			Docker:         *docker,
			OldVersion:     *oldTemporal,
			Password:       os.Getenv("WORKFLOW_TEST_DB_PASSWORD"),
			LogDir:         os.Getenv("ENGINE_LOG_DIR"),
			StartupTimeout: *startup,
			TestTimeout:    *testTimeout,
		}, os.Stdout, os.Stderr)
	}
	// Dev mode is the try-out path: no configuration file, and no namespace or search attribute created here.
	engineArgs := []string{"start-dev", "--port", strings.TrimPrefix(balsuite.Address, "localhost:")}
	if *database != "dev" {
		engineArgs = []string{"start", "--runtime-config", filepath.Join(rootPath, "tests", "engine", "runtime-"+*database+".toml"),
			"--allow-no-auth"}
	}
	return balsuite.Run(context.Background(), balsuite.Options{
		Suite:          args[0],
		Database:       *database,
		Root:           rootPath,
		Engine:         enginePath,
		EngineArgs:     engineArgs,
		DevMode:        *database == "dev",
		Bal:            *bal,
		Temporal:       *temporal,
		LogDir:         os.Getenv("ENGINE_LOG_DIR"),
		StartupTimeout: *startup,
		TestTimeout:    *testTimeout,
	}, os.Stdout, os.Stderr)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
