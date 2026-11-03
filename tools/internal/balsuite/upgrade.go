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
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

const (
	// UpgradeSuite is the suite whose "before" group runs on the old server and "after" group on the engine.
	UpgradeSuite = "upgrade"
	// upgradeWorkflowID is the instance tests/ballerina/upgrade starts before the upgrade and finishes after it.
	upgradeWorkflowID = "upgrade-order-1"
	// dockerHost is how containers reach the databases published on the host.
	dockerHost = "host.docker.internal"
	// dockerTimeout bounds one docker call, image pulls included.
	dockerTimeout = 5 * time.Minute
)

// upgradeDatabase is how the old server, its schema tool and a SQL client reach one test database.
type upgradeDatabase struct {
	plugin, port, user, schemas, client string
}

// UpgradeDatabases are the --database values the upgrade run supports; SQLite has no schema versions.
var UpgradeDatabases = map[string]upgradeDatabase{
	"postgresql": {plugin: "postgres12", port: "15432", user: "workflow", schemas: "postgresql/v12", client: "postgres:16"},
	"mysql":      {plugin: "mysql8", port: "13306", user: "root", schemas: "mysql/v8", client: "mysql:8.4"},
}

// UpgradeOptions describes one upgrade run.
type UpgradeOptions struct {
	Database       string // postgresql or mysql
	Root           string // repository root; the suite is tests/ballerina/upgrade
	Engine         string // workflow-engine binary
	Bal            string
	Temporal       string // Temporal CLI
	Docker         string
	OldVersion     string // Temporal release that runs first, such as 1.30.9: its server image and admin-tools image
	Password       string // database password; also what the engine reads from WORKFLOW_TEST_DB_PASSWORD
	LogDir         string
	StartupTimeout time.Duration
	TestTimeout    time.Duration
}

// oldServerConfig matches the engine's cluster metadata and shard count, which the engine refuses to change. The old
// server runs without an internal frontend, as Temporal's default services do.
var oldServerConfig = template.Must(template.New("config").Parse(`log:
  stdout: true
  level: warn
persistence:
  defaultStore: default
  visibilityStore: visibility
  numHistoryShards: 512
  datastores:
{{- range .Stores}}
    {{.Store}}:
      sql:
        pluginName: {{$.Plugin}}
        databaseName: {{.Name}}
        connectAddr: {{$.Host}}:{{$.Port}}
        connectProtocol: tcp
        user: {{$.User}}
        password: "{{$.Password}}"
        maxConns: 20
        maxIdleConns: 20
        maxConnLifetime: 1h
{{- end}}
global:
  membership:
    maxJoinDuration: 30s
    broadcastAddress: 127.0.0.1
clusterMetadata:
  enableGlobalNamespace: false
  failoverVersionIncrement: 10
  masterClusterName: active
  currentClusterName: active
  clusterInformation:
    active:
      enabled: true
      initialFailoverVersion: 1
      rpcAddress: 127.0.0.1:17233
      httpAddress: 127.0.0.1:17243
dcRedirectionPolicy:
  policy: noop
services:
  frontend:
    rpc: {grpcPort: 17233, membershipPort: 16933, bindOnIP: 0.0.0.0, httpPort: 17243}
  history:
    rpc: {grpcPort: 17234, membershipPort: 16934, bindOnIP: 0.0.0.0}
  matching:
    rpc: {grpcPort: 17235, membershipPort: 16935, bindOnIP: 0.0.0.0}
  worker:
    rpc: {grpcPort: 17239, membershipPort: 16939, bindOnIP: 0.0.0.0}
publicClient:
  hostPort: 127.0.0.1:17233
`))

type upgradeRun struct {
	o         UpgradeOptions
	db        upgradeDatabase
	names     [2]string // workflow and visibility databases
	container string
	stderr    io.Writer
}

// RunUpgrade starts a workflow on the old Temporal server with that release's schema, stops it mid-wait, starts the
// engine on the same databases (which upgrades their schemas) and finishes the workflow there.
func RunUpgrade(ctx context.Context, o UpgradeOptions, stdout, stderr io.Writer) int {
	db, ok := UpgradeDatabases[o.Database]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "error: the upgrade run needs postgresql or mysql, not %q\n", o.Database)
		return 2
	}
	suffix := time.Now().Format("150405")
	r := &upgradeRun{o: o, db: db, stderr: stderr, container: "workflow-upgrade-old-" + suffix,
		names: [2]string{"upgrade_temporal_" + suffix, "upgrade_visibility_" + suffix}}
	work, err := os.MkdirTemp("", "workflow-upgrade-")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(work) }()
	logDir := o.LogDir
	if logDir == "" {
		logDir = work
	}
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer r.dropDatabases(ctx)
	code, err := r.run(ctx, work, logDir, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	}
	return code
}

func (r *upgradeRun) run(ctx context.Context, work, logDir string, stdout io.Writer) (int, error) {
	o := r.o
	suiteDir := filepath.Join(o.Root, "tests", "ballerina", UpgradeSuite)
	if err := r.installOldSchema(ctx); err != nil {
		return 1, err
	}
	before, err := r.versions(ctx)
	if err != nil {
		return 1, err
	}
	if err := r.startOldServer(ctx, work); err != nil {
		r.stopOldServer(ctx, logDir)
		return 1, err
	}
	code, err := func() (int, error) {
		if err := ensureNamespace(ctx, o.Temporal, namespaceTimeout); err != nil {
			return 1, err
		}
		_, _ = fmt.Fprintf(stdout, "Temporal %s SERVING on %s (%s, schema %s); starting %s\n", o.OldVersion, Address,
			o.Database, strings.Join(before, ", "), upgradeWorkflowID)
		if code := runBalTest(o.Bal, suiteDir, o.TestTimeout, r.stderr, "--groups", "before"); code != 0 {
			return code, fmt.Errorf("the before group failed on Temporal %s", o.OldVersion)
		}
		return 0, r.checkActivityRan(ctx)
	}()
	r.stopOldServer(ctx, logDir)
	if code != 0 || err != nil {
		return max(code, 1), err
	}

	config, err := r.engineConfig(work)
	if err != nil {
		return 1, err
	}
	logPath := filepath.Join(logDir, "engine-upgrade-"+o.Database+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return 1, err
	}
	defer func() { _ = logFile.Close() }()
	engine, err := startEngine(o.Engine, []string{"start", "--runtime-config", config, "--allow-no-auth"}, logFile, stopTimeout)
	if err != nil {
		return 1, err
	}
	code, err = func() (int, error) {
		if err := waitUntilServing(ctx, o.Temporal, engine, o.StartupTimeout); err != nil {
			return 1, err
		}
		after, err := r.versions(ctx)
		if err != nil {
			return 1, err
		}
		for i := range after {
			if after[i] == before[i] {
				return 1, fmt.Errorf("database %s is still at schema %s after the engine started", r.names[i], after[i])
			}
		}
		_, _ = fmt.Fprintf(stdout, "workflow-engine SERVING on %s (schema %s, was %s); finishing %s\n", Address,
			strings.Join(after, ", "), strings.Join(before, ", "), upgradeWorkflowID)
		if code := runBalTest(o.Bal, suiteDir, o.TestTimeout, r.stderr, "--groups", "after"); code != 0 {
			return code, fmt.Errorf("the after group failed on the upgraded engine")
		}
		return 0, nil
	}()
	engine.stop()
	if code != 0 {
		_ = logFile.Sync()
		printLogTail(r.stderr, logPath, logTailLines)
	}
	return code, err
}

// docker runs one docker command; env holds variables the command passes on by name, so values stay out of argv.
func (r *upgradeRun) docker(ctx context.Context, env []string, args ...string) result {
	return runQuietEnv(ctx, dockerTimeout, env, r.o.Docker, args...)
}

// dockerRun runs a throwaway container that reaches the databases published on the host.
func (r *upgradeRun) dockerRun(ctx context.Context, env []string, args ...string) result {
	base := []string{"run", "--rm", "--add-host", dockerHost + ":host-gateway"}
	for _, kv := range env {
		base = append(base, "-e", strings.SplitN(kv, "=", 2)[0])
	}
	return r.docker(ctx, env, append(base, args...)...)
}

// installOldSchema creates both databases and installs the schema versions the old release ships, with its own tool.
func (r *upgradeRun) installOldSchema(ctx context.Context) error {
	tool := fmt.Sprintf("temporal-sql-tool --plugin %s --ep %s -p %s -u %s", r.db.plugin, dockerHost, r.db.port, r.db.user)
	var steps []string
	for i, schema := range []string{"temporal", "visibility"} {
		db := tool + " --db " + r.names[i]
		steps = append(steps, db+" create-database", db+" setup-schema -v 0.0",
			fmt.Sprintf("%s update-schema -d /etc/temporal/schema/%s/%s/versioned", db, r.db.schemas, schema))
	}
	res := r.dockerRun(ctx, []string{"SQL_PASSWORD=" + r.o.Password}, "--entrypoint", "sh",
		"temporalio/admin-tools:"+r.o.OldVersion, "-c", "set -e; "+strings.Join(steps, "; "))
	if res.code != 0 {
		return fmt.Errorf("unable to install the schema of Temporal %s: %s", r.o.OldVersion, res.message())
	}
	return nil
}

// versions reads the schema version of both databases with the database's own client image.
func (r *upgradeRun) versions(ctx context.Context) ([]string, error) {
	versions := make([]string, len(r.names))
	for i, name := range r.names {
		res := r.sql(ctx, name, "SELECT curr_version FROM schema_version")
		version := strings.TrimSpace(res.stdout)
		if res.code != 0 || version == "" {
			return nil, fmt.Errorf("unable to read the schema version of %s: %s", name, res.message())
		}
		versions[i] = version
	}
	return versions, nil
}

func (r *upgradeRun) sql(ctx context.Context, database, query string) result {
	if r.o.Database == "mysql" {
		args := []string{r.db.client, "mysql", "-h", dockerHost, "-P", r.db.port, "-u", r.db.user, "-N", "-e", query}
		if database != "" {
			args = append(args, database)
		}
		return r.dockerRun(ctx, []string{"MYSQL_PWD=" + r.o.Password}, args...)
	}
	if database == "" {
		database = "postgres"
	}
	return r.dockerRun(ctx, []string{"PGPASSWORD=" + r.o.Password}, r.db.client, "psql", "-h", dockerHost, "-p",
		r.db.port, "-U", r.db.user, "-d", database, "-Atc", query)
}

func (r *upgradeRun) startOldServer(ctx context.Context, work string) error {
	config := filepath.Join(work, "temporal-"+r.o.OldVersion+".yaml")
	file, err := os.Create(config)
	if err != nil {
		return err
	}
	err = oldServerConfig.Execute(file, map[string]any{
		"Plugin": r.db.plugin, "Host": dockerHost, "Port": r.db.port, "User": r.db.user, "Password": r.o.Password,
		"Stores": []map[string]string{{"Store": "default", "Name": r.names[0]}, {"Store": "visibility", "Name": r.names[1]}},
	})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	res := r.docker(ctx, nil, "run", "-d", "--name", r.container, "--add-host", dockerHost+":host-gateway",
		"-p", "127.0.0.1:17233:17233", "-v", config+":/etc/temporal/config.yaml:ro", "--entrypoint", "temporal-server",
		"temporalio/server:"+r.o.OldVersion, "--config-file", "/etc/temporal/config.yaml", "--allow-no-auth", "start")
	if res.code != 0 {
		return fmt.Errorf("unable to start Temporal %s: %s", r.o.OldVersion, res.message())
	}
	deadline := time.Now().Add(r.o.StartupTimeout)
	for time.Now().Before(deadline) {
		if health := runQuiet(ctx, cliTimeout, r.o.Temporal, "operator", "cluster", "health", "--address", Address); health.code == 0 &&
			strings.Contains(health.stdout, "SERVING") {
			return nil
		}
		if running := r.docker(ctx, nil, "inspect", "-f", "{{.State.Running}}", r.container); strings.TrimSpace(running.stdout) != "true" {
			return fmt.Errorf("the Temporal %s server exited before it was ready", r.o.OldVersion)
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the Temporal %s server was not SERVING on %s after %.0fs", r.o.OldVersion, Address, r.o.StartupTimeout.Seconds())
}

// stopOldServer keeps the old server's log next to the engine's, then removes its container.
func (r *upgradeRun) stopOldServer(ctx context.Context, logDir string) {
	logs := r.docker(ctx, nil, "logs", r.container)
	_ = os.WriteFile(filepath.Join(logDir, "temporal-"+r.o.OldVersion+"-"+r.o.Database+".log"),
		[]byte(logs.stdout+logs.stderr), 0o600)
	_ = r.docker(ctx, nil, "stop", "-t", "30", r.container)
	_ = r.docker(ctx, nil, "rm", "-f", r.container)
}

// checkActivityRan makes sure the old server recorded the first activity, so the upgraded engine resumes real history.
func (r *upgradeRun) checkActivityRan(ctx context.Context) error {
	show := runQuiet(ctx, cliTimeout, r.o.Temporal, "workflow", "show", "--workflow-id", upgradeWorkflowID,
		"--namespace", namespace, "--address", Address, "--output", "json")
	if show.code != 0 || !strings.Contains(strings.ReplaceAll(show.output(), "_", ""), "activitytaskcompleted") {
		return fmt.Errorf("the old server has no completed activity for %s: %s", upgradeWorkflowID, show.message())
	}
	return nil
}

// engineConfig is the suite's runtime.toml for this database, pointed at the upgrade run's databases.
func (r *upgradeRun) engineConfig(work string) (string, error) {
	content, err := os.ReadFile(filepath.Join(r.o.Root, "tests", "engine", "runtime-"+r.o.Database+".toml"))
	if err != nil {
		return "", err
	}
	toml := strings.Replace(string(content), `name = "temporal"`, fmt.Sprintf("name = %q", r.names[0]), 1)
	toml = strings.Replace(toml, `visibilityName = "temporal_visibility"`, fmt.Sprintf("visibilityName = %q", r.names[1]), 1)
	path := filepath.Join(work, "runtime-upgrade.toml")
	return path, os.WriteFile(path, []byte(toml), 0o600) //nolint:gosec // a file in the run's own temporary directory
}

func (r *upgradeRun) dropDatabases(ctx context.Context) {
	for _, name := range r.names {
		drop := "DROP DATABASE IF EXISTS " + name
		if r.o.Database == "postgresql" {
			drop += " WITH (FORCE)"
		}
		if res := r.sql(ctx, "", drop); res.code != 0 {
			_, _ = fmt.Fprintf(r.stderr, "warning: unable to drop %s: %s\n", name, res.message())
		}
	}
}
