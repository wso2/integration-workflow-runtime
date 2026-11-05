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

package sqlschema

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql"
	mysqlschema "go.temporal.io/server/schema/mysql/v8"
	pgschema "go.temporal.io/server/schema/postgresql/v12"
	"go.temporal.io/server/tools/common/schema"
	sqltool "go.temporal.io/server/tools/sql"
)

// testDBPasswordEnv enables these tests against the databases the Ballerina suites use (tests/engine/*.toml).
const testDBPasswordEnv = "WORKFLOW_TEST_DB_PASSWORD" //nolint:gosec // the variable name, not a credential

// server is a test database server; admin can create databases and roles.
type server struct {
	plugin, driver, addr, admin string
	password                    string
}

func realServers(t *testing.T) []server {
	t.Helper()
	password, ok := os.LookupEnv(testDBPasswordEnv)
	if !ok {
		t.Skipf("%s is not set; start the test databases to run this", testDBPasswordEnv)
	}
	return []server{
		{"postgres12", "postgres", "127.0.0.1:15432", "workflow", password},
		{"mysql8", "mysql", "127.0.0.1:13306", "root", password},
	}
}

// stores returns a persistence configuration for this server with databases unique to the test.
func (s server) stores(t *testing.T, user, password string) config.Persistence {
	t.Helper()
	suffix := fmt.Sprintf("%s_%d", time.Now().Format("150405"), time.Now().Nanosecond()%1000)
	p := persistence(s.plugin)
	for _, ds := range p.DataStores {
		ds.SQL.ConnectAddr, ds.SQL.User, ds.SQL.Password = s.addr, user, password
		ds.SQL.DatabaseName = "schema_test_" + ds.SQL.DatabaseName + "_" + suffix
		t.Cleanup(func() {
			admin := *ds.SQL
			admin.User, admin.Password = s.admin, s.password
			dropDatabase(t, admin)
		})
	}
	return p
}

// open connects as the admin to database name ("" for the server's default database).
func (s server) open(t *testing.T, name string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?multiStatements=true", s.admin, s.password, s.addr, name)
	if s.driver == "postgres" {
		if name == "" {
			name = "postgres"
		}
		dsn = fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", s.admin, s.password, s.addr, name)
	}
	db, err := sql.Open(s.driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (s server) exec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// bind turns ? placeholders into PostgreSQL's $1, $2, ...
func (s server) bind(query string) string {
	if s.driver != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestPrepareOnRealDatabases(t *testing.T) {
	for _, server := range realServers(t) {
		t.Run(server.plugin, func(t *testing.T) {
			p := server.stores(t, server.admin, server.password)
			// Nodes that start together all succeed, one at a time.
			var wg sync.WaitGroup
			errs := make([]error, 3)
			for i := range errs {
				wg.Go(func() { errs[i] = Prepare(p, log.NewNoopLogger()) })
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("node %d: Prepare = %v", i, err)
				}
			}
			if err := Prepare(p, log.NewNoopLogger()); err != nil {
				t.Fatalf("second start: Prepare = %v", err)
			}
			checkCurrent(t, p)
		})
	}
}

func TestPrepareResumesAnInterruptedSetupOnRealDatabases(t *testing.T) {
	for _, server := range realServers(t) {
		t.Run(server.plugin, func(t *testing.T) {
			p := server.stores(t, server.admin, server.password)
			main := *p.DataStores[p.DefaultStore].SQL
			admin := server.open(t, "")
			server.exec(t, admin, "CREATE DATABASE "+main.DatabaseName)
			// The version table exists without its row, and the update history table is missing.
			server.exec(t, server.open(t, main.DatabaseName), "CREATE TABLE schema_version(version_partition INT not null, "+
				"db_name VARCHAR(255) not null, creation_time TIMESTAMP, curr_version VARCHAR(64), "+
				"min_compatible_version VARCHAR(64), PRIMARY KEY (version_partition, db_name))")
			if err := Prepare(p, log.NewNoopLogger()); err != nil {
				t.Fatalf("Prepare = %v", err)
			}
			checkCurrent(t, p)
		})
	}
}

func TestPrepareUpgradesExistingDataOnRealDatabases(t *testing.T) {
	for _, server := range realServers(t) {
		t.Run(server.plugin, func(t *testing.T) {
			p := server.stores(t, server.admin, server.password)
			main, vis := *p.DataStores[p.DefaultStore].SQL, *p.DataStores[p.VisibilityStore].SQL
			// Both databases at Temporal's first schema version, holding a namespace, a workflow and its visibility row.
			for _, store := range []struct {
				cfg    config.SQL
				schema string
			}{{main, "temporal"}, {vis, "visibility"}} {
				installVersion(t, server, store.cfg, schemaDirs[server.plugin]+"/"+store.schema, "1.0")
			}
			// IDs are 16 bytes: MySQL stores them as BINARY(16).
			id, run := []byte("namespace-id-001"), []byte("run-id-000000001")
			mainDB, visDB := server.open(t, main.DatabaseName), server.open(t, vis.DatabaseName)
			seed := []struct {
				db    *sql.DB
				query string
				args  []any
			}{
				{mainDB, "INSERT INTO namespaces (partition_id, id, name, notification_version, data, data_encoding, is_global) " +
					"VALUES (?, ?, ?, ?, ?, ?, ?)", []any{54321, id, "orders", 1, []byte{1}, "Proto3", false}},
				{mainDB, "INSERT INTO executions (shard_id, namespace_id, workflow_id, run_id, next_event_id, last_write_version, " +
					"data, data_encoding, state, state_encoding) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
					[]any{1, id, "order-1", run, 7, 0, []byte{2}, "Proto3", []byte{3}, "Proto3"}},
				{visDB, "INSERT INTO executions_visibility (namespace_id, run_id, start_time, execution_time, workflow_id, " +
					"workflow_type_name, status, encoding) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
					[]any{"namespace-id-0001", "run-id-0000000001", time.Now().UTC(), time.Now().UTC(), "order-1", "OrderFlow", 1, "Proto3"}},
			}
			for _, row := range seed {
				if _, err := row.db.Exec(server.bind(row.query), row.args...); err != nil {
					t.Fatalf("%s: %v", row.query, err)
				}
			}
			if err := Prepare(p, log.NewNoopLogger()); err != nil {
				t.Fatalf("Prepare = %v", err)
			}
			checkCurrent(t, p)
			var name, workflow, visWorkflow string
			var nextEvent int
			checks := []struct {
				db    *sql.DB
				query string
				args  []any
				dest  []any
			}{
				{mainDB, "SELECT name FROM namespaces WHERE id = ?", []any{id}, []any{&name}},
				{mainDB, "SELECT workflow_id, next_event_id FROM executions WHERE run_id = ?", []any{run}, []any{&workflow, &nextEvent}},
				{visDB, "SELECT workflow_id FROM executions_visibility WHERE run_id = ?", []any{"run-id-0000000001"}, []any{&visWorkflow}},
			}
			for _, c := range checks {
				if err := c.db.QueryRow(server.bind(c.query), c.args...).Scan(c.dest...); err != nil {
					t.Fatalf("%s: %v", c.query, err)
				}
			}
			if name != "orders" || workflow != "order-1" || nextEvent != 7 || visWorkflow != "order-1" {
				t.Errorf("after the upgrade: namespace %q, workflow %q at event %d, visibility %q", name, workflow, nextEvent, visWorkflow)
			}
		})
	}
}

func TestPrepareWithARoleLimitedToItsDatabases(t *testing.T) {
	for _, server := range realServers(t) {
		t.Run(server.plugin, func(t *testing.T) {
			role := fmt.Sprintf("schema_role_%d", time.Now().UnixNano()%1_000_000)
			const rolePassword = "role-password" //nolint:gosec // a test role's password
			p := server.stores(t, role, rolePassword)
			main, vis := p.DataStores[p.DefaultStore].SQL.DatabaseName, p.DataStores[p.VisibilityStore].SQL.DatabaseName
			admin := server.open(t, "")
			if server.driver == "postgres" {
				// A DBA creates the databases for the role, and the role may not connect to the maintenance database.
				server.exec(t, admin, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", role, rolePassword),
					fmt.Sprintf("CREATE DATABASE %s OWNER %s", main, role), fmt.Sprintf("CREATE DATABASE %s OWNER %s", vis, role),
					"REVOKE CONNECT ON DATABASE postgres FROM PUBLIC")
				t.Cleanup(func() {
					server.exec(t, admin, "GRANT CONNECT ON DATABASE postgres TO PUBLIC")
					dropDatabase(t, adminSQL(server, main))
					dropDatabase(t, adminSQL(server, vis))
					server.exec(t, admin, "DROP ROLE "+role)
				})
			} else {
				server.exec(t, admin, fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", role, rolePassword),
					"CREATE DATABASE "+main, "CREATE DATABASE "+vis,
					fmt.Sprintf("GRANT ALL ON %s.* TO '%s'@'%%'", main, role), fmt.Sprintf("GRANT ALL ON %s.* TO '%s'@'%%'", vis, role))
				t.Cleanup(func() { server.exec(t, admin, fmt.Sprintf("DROP USER '%s'@'%%'", role)) })
			}
			if err := Prepare(p, log.NewNoopLogger()); err != nil {
				t.Fatalf("Prepare as %s = %v", role, err)
			}
			checkCurrent(t, p)
		})
	}
}

// installVersion creates a database at an older schema version, as an earlier engine release would have left it.
func installVersion(t *testing.T, server server, cfg config.SQL, schemaName, version string) {
	t.Helper()
	server.exec(t, server.open(t, ""), "CREATE DATABASE "+cfg.DatabaseName)
	c, err := sqltool.NewConnection(&cfg, log.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := schema.NewSetupSchemaTask(c, &schema.SetupConfig{InitialVersion: initialVersion}, log.NewNoopLogger()).Run(); err != nil {
		t.Fatal(err)
	}
	update := &schema.UpdateConfig{DBName: cfg.DatabaseName, SchemaName: schemaName, TargetVersion: version}
	if err := schema.NewUpdateSchemaTask(c, update, log.NewNoopLogger()).Run(); err != nil {
		t.Fatal(err)
	}
	if got := readVersion(t, cfg); got != version {
		t.Fatalf("%s starts at %s, want %s", cfg.DatabaseName, got, version)
	}
}

func adminSQL(server server, name string) config.SQL {
	return config.SQL{PluginName: server.plugin, ConnectAddr: server.addr, User: server.admin, Password: server.password,
		DatabaseName: name}
}

func checkCurrent(t *testing.T, p config.Persistence) {
	t.Helper()
	versions := map[string]string{p.DefaultStore: pgschema.Version, p.VisibilityStore: pgschema.VisibilityVersion}
	if p.DataStores[p.DefaultStore].SQL.PluginName == "mysql8" {
		versions = map[string]string{p.DefaultStore: mysqlschema.Version, p.VisibilityStore: mysqlschema.VisibilityVersion}
	}
	for name, want := range versions {
		if got := readVersion(t, *p.DataStores[name].SQL); got != want {
			t.Errorf("%s version = %s, want %s", name, got, want)
		}
	}
}

func readVersion(t *testing.T, cfg config.SQL) string {
	t.Helper()
	c, err := sqltool.NewConnection(&cfg, log.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	version, err := c.ReadSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func dropDatabase(t *testing.T, cfg config.SQL) {
	if err := sqltool.DoDropDatabase(&cfg, "", log.NewNoopLogger()); err != nil {
		t.Logf("unable to drop %s: %v", cfg.DatabaseName, err)
	}
}

func TestTheSchemaLockReportsAnEndedSession(t *testing.T) {
	for _, server := range realServers(t) {
		t.Run(server.plugin, func(t *testing.T) {
			cfg := *server.stores(t, server.admin, server.password).DataStores["default"].SQL
			cfg.DatabaseName = ""
			if server.driver == "postgres" {
				cfg.DatabaseName = "postgres"
			}
			l, err := openLock(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer l.close()
			lock := l.(*pinnedLock)
			var id int64
			query := "SELECT pg_backend_pid()"
			if server.driver != "postgres" {
				query = "SELECT CONNECTION_ID()"
			}
			if err := lock.conn.QueryRowContext(context.Background(), query).Scan(&id); err != nil {
				t.Fatal(err)
			}
			kill := fmt.Sprintf("SELECT pg_terminate_backend(%d)", id)
			if server.driver != "postgres" {
				kill = fmt.Sprintf("KILL %d", id)
			}
			server.exec(t, server.open(t, ""), kill)
			// A pooled connection would answer from a new session here, without the lock.
			if err := l.alive(); err == nil {
				t.Fatal("alive = nil after the lock's session ended")
			}
		})
	}
}
