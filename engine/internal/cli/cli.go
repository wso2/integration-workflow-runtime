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
//
// The start sequence follows cmd/server/main.go of temporalio/temporal (MIT License,
// Copyright (c) 2025 Temporal Technologies Inc., Copyright (c) 2020 Uber Technologies, Inc.).

// Package cli implements the workflow-engine command: it starts Temporal's services from a configuration file.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/headers"
	"go.temporal.io/server/common/log"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite"
	"go.temporal.io/server/common/primitives"
	"go.temporal.io/server/temporal"

	"github.com/wso2/integration-workflow-runtime/engine/internal/runtimeconfig"
)

const usageText = `usage: workflow-engine version
       workflow-engine start (--runtime-config <runtime.toml> | --config-file <temporal.yaml>) [--service <name>]... [--allow-no-auth]
       workflow-engine render-config --runtime-config <runtime.toml>`

type serverFactory func(opts ...temporal.ServerOption) (temporal.Server, error)

type command struct {
	version   string
	stdout    io.Writer
	stderr    io.Writer
	newServer serverFactory
}

// Main runs the command with the given arguments and returns the process exit code.
func Main(version string, args []string) int {
	c := command{version: version, stdout: os.Stdout, stderr: os.Stderr, newServer: temporal.NewServer}
	return c.run(args)
}

func (c command) run(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(c.stderr, usageText)
		return 2
	}
	switch args[0] {
	case "version":
		_, _ = fmt.Fprintf(c.stdout, "workflow-engine %s (Temporal server %s)\n", c.version, headers.ServerVersion)
		return 0
	case "start":
		return c.exitCode(c.start(args[1:]))
	case "render-config":
		return c.exitCode(c.renderConfig(args[1:]))
	default:
		_, _ = fmt.Fprintln(c.stderr, usageText)
		return 2
	}
}

func (c command) exitCode(err error) int {
	if err != nil {
		_, _ = fmt.Fprintln(c.stderr, err)
		return 1
	}
	return 0
}

type serviceList []string

func (s *serviceList) String() string { return strings.Join(*s, ",") }

func (s *serviceList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func (c command) start(args []string) error {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	runtimeConfig := flags.String("runtime-config", "", "the runtime's runtime.toml; Temporal's configuration is generated from it")
	configFile := flags.String("config-file", "", "a Temporal server configuration file, used as is")
	allowNoAuth := flags.Bool("allow-no-auth", false, "start without an authorizer (development and tests only)")
	var services serviceList
	flags.Var(&services, "service", "service to start, repeatable: frontend, internal-frontend, history, matching, worker")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	cfg, err := loadConfig(*runtimeConfig, *configFile)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		services = defaultServices(cfg)
	}
	logger := log.NewZapLogger(log.BuildZapLogger(cfg.Log))

	authorizer, err := authorization.GetAuthorizerFromConfig(&cfg.Global.Authorization)
	if err != nil {
		return fmt.Errorf("unable to create the authorizer: %w", err)
	}
	// Secure by default: an open frontend is refused unless explicitly asked for.
	if authorization.IsNoopAuthorizer(authorizer) && !*allowNoAuth {
		return errors.New("no authorizer configured; set one or pass --allow-no-auth for development")
	}
	// Without a claim mapper Temporal grants every caller system-admin claims, so the authorizer would admit anyone.
	if strings.TrimSpace(cfg.Global.Authorization.ClaimMapper) == "" && !*allowNoAuth {
		return errors.New("no claim mapper configured; an authorizer needs one (for example claimMapper: default)")
	}
	// With an authorizer on, history and worker reach the frontend only through the internal frontend; without it
	// the engine exits about a minute after start.
	if !authorization.IsNoopAuthorizer(authorizer) && !hasInternalFrontend(cfg) {
		return errors.New("an authorizer needs the internal frontend; add services.internal-frontend to the configuration")
	}
	claimMapper, err := authorization.GetClaimMapperFromConfig(&cfg.Global.Authorization, logger)
	if err != nil {
		return fmt.Errorf("unable to create the claim mapper: %w", err)
	}
	audienceMapper, err := authorization.GetAudienceMapperFromConfig(&cfg.Global.Authorization)
	if err != nil {
		return fmt.Errorf("unable to create the audience mapper: %w", err)
	}

	server, err := c.newServer(
		temporal.ForServices(services),
		temporal.WithConfig(cfg),
		temporal.WithLogger(logger),
		temporal.InterruptOn(temporal.InterruptCh()),
		temporal.WithAuthorizer(authorizer),
		temporal.WithClaimMapper(func(*config.Config) authorization.ClaimMapper { return claimMapper }),
		temporal.WithAudienceGetter(func(*config.Config) authorization.JWTAudienceMapper { return audienceMapper }),
	)
	if err != nil {
		return fmt.Errorf("unable to create the server: %w", err)
	}
	return server.Start()
}

func (c command) renderConfig(args []string) error {
	flags := flag.NewFlagSet("render-config", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	runtimeConfig := flags.String("runtime-config", "", "the runtime's runtime.toml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *runtimeConfig == "" {
		return errors.New("--runtime-config is required")
	}
	cfg, err := runtimeconfig.Load(*runtimeConfig)
	if err != nil {
		return err
	}
	rendered, err := runtimeconfig.Render(cfg)
	if err != nil {
		return fmt.Errorf("unable to render the configuration: %w", err)
	}
	_, err = c.stdout.Write(rendered)
	return err
}

// loadConfig returns the Temporal configuration from exactly one of a runtime.toml and a Temporal YAML file.
func loadConfig(runtimeConfig, configFile string) (*config.Config, error) {
	switch {
	case runtimeConfig != "" && configFile != "":
		return nil, errors.New("pass either --runtime-config or --config-file, not both")
	case runtimeConfig != "":
		return runtimeconfig.Load(runtimeConfig)
	case configFile != "":
		cfg, err := config.Load(config.WithConfigFile(configFile))
		if err != nil {
			return nil, fmt.Errorf("unable to load configuration: %w", err)
		}
		return cfg, nil
	default:
		return nil, errors.New("--runtime-config or --config-file is required")
	}
}

// defaultServices is Temporal's default set plus the internal frontend when the configuration defines one.
func defaultServices(cfg *config.Config) []string {
	services := append([]string{}, temporal.DefaultServices...)
	if hasInternalFrontend(cfg) {
		services = append(services, string(primitives.InternalFrontendService))
	}
	return services
}

func hasInternalFrontend(cfg *config.Config) bool {
	_, ok := cfg.Services[string(primitives.InternalFrontendService)]
	return ok
}
