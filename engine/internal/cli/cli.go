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
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/wso2/integration-workflow-runtime/engine/internal/sqlschema"
	"github.com/wso2/integration-workflow-runtime/engine/plugins/jwtclaims"
)

const usageText = `usage: workflow-engine version
       workflow-engine start (--runtime-config <runtime.toml> | --config-file <temporal.yaml>) [--service <name>]... [--allow-no-auth]
       workflow-engine start-dev [--ip <address>] [--port <port>] [--db-file <file>] [--namespace <name>]... [--log-level <level>]
       workflow-engine render-config --runtime-config <runtime.toml>`

// serverFactory builds the server for the services this node runs; tests record the services.
type serverFactory func(services []string, opts ...temporal.ServerOption) (temporal.Server, error)

func newTemporalServer(services []string, opts ...temporal.ServerOption) (temporal.Server, error) {
	return temporal.NewServer(append(opts, temporal.ForServices(services))...)
}

type schemaPreparer func(p config.Persistence, logger log.Logger) error

type namespaceCreator func(p config.Persistence, names []string) error

type command struct {
	version       string
	stdout        io.Writer
	stderr        io.Writer
	newServer     serverFactory
	prepareSchema schemaPreparer
	devNamespaces namespaceCreator
}

// Main runs the command with the given arguments and returns the process exit code.
func Main(version string, args []string) int {
	c := command{
		version: version, stdout: os.Stdout, stderr: os.Stderr,
		newServer: newTemporalServer, prepareSchema: sqlschema.Prepare, devNamespaces: createDevNamespaces,
	}
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
	case "start-dev":
		return c.exitCode(c.startDev(args[1:]))
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
	cfg, rt, err := loadConfig(*runtimeConfig, *configFile)
	if err != nil {
		return err
	}
	switch {
	case len(services) > 0:
	case rt != nil:
		services = rt.Services
	default:
		services = defaultServices(cfg)
	}
	prepare := func(logger log.Logger) error {
		if rt != nil && rt.ManageSchema {
			if err := c.prepareSchema(cfg.Persistence, logger); err != nil {
				return fmt.Errorf("unable to prepare the database schema: %w", err)
			}
		}
		return nil
	}
	return c.serve(cfg, rt, services, *allowNoAuth, prepare)
}

// startDev runs every service on SQLite with no authorization and creates the "default" namespace, like Temporal's
// own development server. It is for one developer's machine only.
func (c command) startDev(args []string) error {
	flags := flag.NewFlagSet("start-dev", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	ip := flags.String("ip", "127.0.0.1", "address to listen on; keep 127.0.0.1 unless others must reach this engine")
	port := flags.Int("port", 7233, "frontend port; the other services use the next ports up to port+10")
	dbFile := flags.String("db-file", "", "keep the data in this SQLite file; in memory and lost on exit when empty")
	logLevel := flags.String("log-level", "warn", "debug, info, warn or error")
	var namespaces serviceList
	flags.Var(&namespaces, "namespace", "namespace to create besides default, repeatable")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rt, err := runtimeconfig.Dev(runtimeconfig.DevOptions{IP: *ip, Port: *port, DBFile: *dbFile, LogLevel: *logLevel})
	if err != nil {
		return err
	}
	// SQLite creates the file but not its directory, and a fresh distribution has no data/.
	if *dbFile != "" {
		if err := os.MkdirAll(filepath.Dir(*dbFile), 0o750); err != nil {
			return fmt.Errorf("unable to create the directory of --db-file %s: %w", *dbFile, err)
		}
	}
	names := append([]string{"default"}, namespaces...)
	storage := "in memory"
	if *dbFile != "" {
		storage = "in " + *dbFile
	}
	_, _ = fmt.Fprintf(c.stderr, "Development engine on %s:%d (namespaces %s; data %s; no TLS or authorization)\n",
		*ip, *port, strings.Join(names, ", "), storage)
	prepare := func(log.Logger) error {
		if err := c.devNamespaces(rt.Temporal.Persistence, names); err != nil {
			return fmt.Errorf("unable to create the namespaces: %w", err)
		}
		return nil
	}
	return c.serve(&rt.Temporal, rt, rt.Services, true, prepare)
}

// serve checks the security settings, runs prepare, and starts Temporal's services.
func (c command) serve(cfg *config.Config, rt *runtimeconfig.Runtime, services []string, allowNoAuth bool,
	prepare func(log.Logger) error) error {
	logKey := "log.outputFile"
	if rt != nil {
		logKey = "engine.logFile"
	}
	zl, err := newLogger(cfg.Log, logKey)
	if err != nil {
		return err
	}
	logger := newQuietLogger(zl)

	authorizer, err := authorization.GetAuthorizerFromConfig(&cfg.Global.Authorization)
	if err != nil {
		return fmt.Errorf("unable to create the authorizer: %w", err)
	}
	// Secure by default: an open frontend is refused unless explicitly asked for.
	if authorization.IsNoopAuthorizer(authorizer) && !allowNoAuth {
		return errors.New("no authorizer configured; set one or pass --allow-no-auth for development")
	}
	// Without a claim mapper Temporal grants every caller system-admin claims, so the authorizer would admit anyone.
	if strings.TrimSpace(cfg.Global.Authorization.ClaimMapper) == "" && !allowNoAuth {
		return errors.New("no claim mapper configured; an authorizer needs one (for example claimMapper: default)")
	}
	// With an authorizer on, history and worker reach the frontend only through the internal frontend; without it
	// the engine exits about a minute after start.
	if !authorization.IsNoopAuthorizer(authorizer) && !hasInternalFrontend(cfg) {
		return errors.New("an authorizer needs the internal frontend; add services.internal-frontend to the configuration")
	}
	claimMapper, err := newClaimMapper(cfg, rt, logger)
	if err != nil {
		return fmt.Errorf("unable to create the claim mapper: %w", err)
	}
	audienceMapper, err := authorization.GetAudienceMapperFromConfig(&cfg.Global.Authorization)
	if err != nil {
		return fmt.Errorf("unable to create the audience mapper: %w", err)
	}
	if err := prepare(logger); err != nil {
		return err
	}

	server, err := c.newServer(services,
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
	rt, err := runtimeconfig.Load(*runtimeConfig)
	if err != nil {
		return err
	}
	rendered, err := runtimeconfig.Render(&rt.Temporal)
	if err != nil {
		return fmt.Errorf("unable to render the configuration: %w", err)
	}
	_, err = c.stdout.Write(rendered)
	return err
}

// loadConfig returns the Temporal configuration from exactly one of a runtime.toml and a Temporal YAML file; the
// runtime settings are nil for a Temporal YAML file, which is used as is.
func loadConfig(runtimeConfig, configFile string) (*config.Config, *runtimeconfig.Runtime, error) {
	switch {
	case runtimeConfig != "" && configFile != "":
		return nil, nil, errors.New("pass either --runtime-config or --config-file, not both")
	case runtimeConfig != "":
		rt, err := runtimeconfig.Load(runtimeConfig)
		if err != nil {
			return nil, nil, err
		}
		return &rt.Temporal, rt, nil
	case configFile != "":
		cfg, err := config.Load(config.WithConfigFile(configFile))
		if err != nil {
			return nil, nil, fmt.Errorf("unable to load configuration: %w", err)
		}
		return cfg, nil, nil
	default:
		return nil, nil, errors.New("--runtime-config or --config-file is required")
	}
}

// newClaimMapper builds the identity-provider mapper that runtime.toml's [engine.auth] asks for, or Temporal's own.
func newClaimMapper(cfg *config.Config, rt *runtimeconfig.Runtime, logger log.Logger) (authorization.ClaimMapper, error) {
	if cfg.Global.Authorization.ClaimMapper != runtimeconfig.ClaimMapperName {
		return authorization.GetClaimMapperFromConfig(&cfg.Global.Authorization, logger)
	}
	if rt == nil || rt.Auth == nil {
		return nil, fmt.Errorf("claim mapper %q is configured only through runtime.toml [engine.auth]",
			runtimeconfig.ClaimMapperName)
	}
	if len(cfg.Global.Authorization.JWTKeyProvider.KeySourceURIs) == 0 {
		jwks, err := jwtclaims.Discover(context.Background(), http.DefaultClient, rt.Auth.Issuer)
		if err != nil {
			return nil, fmt.Errorf("unable to discover the identity provider's key set: %w", err)
		}
		cfg.Global.Authorization.JWTKeyProvider.KeySourceURIs = []string{jwks}
	}
	if rt.Auth.Issuer == "" && rt.Auth.Audience == "" {
		logger.Warn("engine.auth checks neither issuer nor audience: every unexpired token the key set signs is " +
			"accepted, including tokens issued for other applications; set issuer or audience")
	}
	keys := authorization.NewDefaultTokenKeyProvider(&cfg.Global.Authorization, logger)
	return jwtclaims.New(keys, *rt.Auth)
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

// newLogger opens the log file first: Temporal's builder drops the error of a sink it cannot open and returns nil.
func newLogger(cfg log.Config, key string) (log.Logger, error) {
	if cfg.OutputFile != "" && !cfg.Stdout {
		if err := os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o750); err != nil {
			return nil, fmt.Errorf("%s %s: %w", key, cfg.OutputFile, err)
		}
		f, err := os.OpenFile(cfg.OutputFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640) //nolint:gosec // the configured log file
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", key, cfg.OutputFile, err)
		}
		_ = f.Close()
	}
	zl := log.BuildZapLogger(cfg)
	if zl == nil {
		return nil, fmt.Errorf("%s %s: unable to open the log output", key, cfg.OutputFile)
	}
	return log.NewZapLogger(zl), nil
}
