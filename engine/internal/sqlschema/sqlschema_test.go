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
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	mysqlschema "go.temporal.io/server/schema/mysql/v8"
	pgschema "go.temporal.io/server/schema/postgresql/v12"
)

type fakeConn struct {
	name      string
	version   string
	tables    []string
	execs     []string
	created   []string
	closed    bool
	createErr error
	listErr   error
	readErr   error
	execErr   map[string]error
}

func (f *fakeConn) Exec(stmt string, _ ...any) error {
	f.execs = append(f.execs, stmt)
	for prefix, err := range f.execErr {
		if strings.HasPrefix(stmt, prefix) {
			return err
		}
	}
	return nil
}

func (f *fakeConn) DropAllTables() error { return nil }

func (f *fakeConn) CreateSchemaVersionTables() error {
	for _, table := range []string{versionTable, historyTable} {
		if !slices.Contains(f.tables, table) {
			f.tables = append(f.tables, table)
		}
	}
	return nil
}

func (f *fakeConn) ReadSchemaVersion() (string, error) {
	if f.readErr != nil {
		return "", f.readErr
	}
	if f.version == "" {
		return "", sql.ErrNoRows
	}
	return f.version, nil
}

// schemaExecs leaves out the lock statements, which run on the workflow database's connection.
func (f *fakeConn) schemaExecs() []string {
	return slices.DeleteFunc(slices.Clone(f.execs), func(stmt string) bool {
		return strings.Contains(stmt, "_LOCK(") || strings.Contains(stmt, "advisory_")
	})
}

func (f *fakeConn) UpdateSchemaVersion(newVersion string, _ string) error {
	f.version = newVersion
	return nil
}

func (f *fakeConn) WriteSchemaUpdateLog(string, string, string, string) error { return nil }
func (f *fakeConn) Close()                                                    { f.closed = true }
func (f *fakeConn) Type() string                                              { return "sql" }

func (f *fakeConn) CreateDatabase(name string) error {
	f.created = append(f.created, name)
	return f.createErr
}

func (f *fakeConn) ListTables() ([]string, error) { return f.tables, f.listErr }

type fakeServer struct {
	conns   map[string]*fakeConn
	openErr map[string]error
	opened  []*config.SQL
}

func newFakeServer() *fakeServer {
	return &fakeServer{conns: map[string]*fakeConn{
		"":                    {name: ""},
		"temporal":            {name: "temporal"},
		"temporal_visibility": {name: "temporal_visibility"},
	}, openErr: map[string]error{}}
}

func (s *fakeServer) open(cfg *config.SQL, _ log.Logger) (conn, error) {
	s.opened = append(s.opened, cfg)
	if err := s.openErr[cfg.DatabaseName]; err != nil {
		return nil, err
	}
	return s.conns[cfg.DatabaseName], nil
}

func persistence(plugin string) config.Persistence {
	sql := func(name string) *config.SQL {
		return &config.SQL{PluginName: plugin, DatabaseName: name, ConnectAddr: "db:5432", MaxConns: 20, MaxIdleConns: 20}
	}
	return config.Persistence{
		DefaultStore:    "default",
		VisibilityStore: "visibility",
		DataStores: map[string]config.DataStore{
			"default":    {SQL: sql("temporal")},
			"visibility": {SQL: sql("temporal_visibility")},
		},
	}
}

func TestPrepareSetsUpEmptyDatabases(t *testing.T) {
	for plugin, want := range map[string]struct{ lock, unlock, version, visibility string }{
		"postgres12":     {"SELECT pg_advisory_lock", "SELECT pg_advisory_unlock", pgschema.Version, pgschema.VisibilityVersion},
		"postgres12_pgx": {"SELECT pg_advisory_lock", "SELECT pg_advisory_unlock", pgschema.Version, pgschema.VisibilityVersion},
		"mysql8":         {"SELECT GET_LOCK", "SELECT RELEASE_LOCK", mysqlschema.Version, mysqlschema.VisibilityVersion},
	} {
		t.Run(plugin, func(t *testing.T) {
			s := newFakeServer()
			if err := prepare(persistence(plugin), s.open, log.NewNoopLogger()); err != nil {
				t.Fatalf("prepare = %v", err)
			}
			server, main, vis := s.conns[""], s.conns["temporal"], s.conns["temporal_visibility"]
			if !slices.Equal(server.created, []string{"temporal", "temporal_visibility"}) {
				t.Errorf("created = %v, want both databases", server.created)
			}
			if len(server.execs) != 0 || len(main.execs) < 3 || !strings.HasPrefix(main.execs[0], want.lock) ||
				!strings.HasPrefix(main.execs[len(main.execs)-1], want.unlock) {
				t.Errorf("statements = %v on the server, %d on the workflow database; want the lock around the schema "+
					"work in the workflow database", server.execs, len(main.execs))
			}
			if main.version != want.version || vis.version != want.visibility {
				t.Errorf("versions = %s, %s, want %s, %s", main.version, vis.version, want.version, want.visibility)
			}
			if len(main.schemaExecs()) == 0 || len(vis.execs) == 0 {
				t.Error("no schema statements ran")
			}
			if !server.closed || !main.closed || !vis.closed {
				t.Error("a connection was left open")
			}
			if c := s.opened[0]; c.DatabaseName != "" || c.MaxConns != 1 || c.MaxIdleConns != 1 || c.MaxConnLifetime != 0 {
				t.Errorf("server connection = %+v, want one connection to the default database", c)
			}
			if c := s.opened[1]; c.DatabaseName != "temporal" || c.MaxConns != 1 || c.MaxConnLifetime != 0 {
				t.Errorf("lock connection = %+v, want one connection to the workflow database", c)
			}
		})
	}
}

func TestPrepareLeavesCurrentDatabasesAlone(t *testing.T) {
	s := newFakeServer()
	s.conns["temporal"].tables = []string{versionTable, historyTable, "executions"}
	s.conns["temporal"].version = pgschema.Version
	s.conns["temporal_visibility"].tables = []string{versionTable, historyTable}
	s.conns["temporal_visibility"].version = pgschema.VisibilityVersion
	if err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger()); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	for _, name := range []string{"temporal", "temporal_visibility"} {
		if execs := s.conns[name].schemaExecs(); len(execs) != 0 {
			t.Errorf("%s ran %d statements, want none", name, len(execs))
		}
	}
}

func TestPrepareAppliesOnlyNewerVersions(t *testing.T) {
	s := newFakeServer()
	s.conns["temporal"].tables = []string{versionTable, historyTable}
	s.conns["temporal"].version = "1.18"
	s.conns["temporal_visibility"].tables = []string{versionTable, historyTable}
	s.conns["temporal_visibility"].version = pgschema.VisibilityVersion
	if err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger()); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	if got := s.conns["temporal"].version; got != pgschema.Version {
		t.Fatalf("version = %s, want %s", got, pgschema.Version)
	}
	if n := len(s.conns["temporal"].schemaExecs()); n == 0 || n > 20 {
		t.Fatalf("ran %d statements, want only the last version's", n)
	}
}

func TestPrepareUsesDatabasesItCannotCreate(t *testing.T) {
	s := newFakeServer()
	s.conns[""].createErr = errors.New("permission denied to create database")
	if err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger()); err != nil {
		t.Fatalf("prepare = %v, want the existing databases used", err)
	}
	if s.conns["temporal"].version != pgschema.Version {
		t.Fatal("the schema was not installed")
	}
}

func TestPrepareWorksWithoutTheMaintenanceDatabase(t *testing.T) {
	s := newFakeServer()
	s.openErr[""] = errors.New(`permission denied for database "postgres"`)
	if err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger()); err != nil {
		t.Fatalf("prepare = %v, want the DBA-created databases used", err)
	}
	if s.conns["temporal"].version != pgschema.Version || s.conns["temporal_visibility"].version != pgschema.VisibilityVersion {
		t.Fatal("the schemas were not installed")
	}
}

func TestPrepareResumesAnInterruptedSetup(t *testing.T) {
	for name, tables := range map[string][]string{
		"version table without its row": {versionTable, historyTable},
		"no update history table":       {versionTable},
	} {
		t.Run(name, func(t *testing.T) {
			s := newFakeServer()
			s.conns["temporal"].tables = slices.Clone(tables)
			if err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger()); err != nil {
				t.Fatalf("prepare = %v", err)
			}
			main := s.conns["temporal"]
			if main.version != pgschema.Version || !slices.Contains(main.tables, historyTable) {
				t.Fatalf("version = %q, tables = %v; want the setup finished and the schema current", main.version, main.tables)
			}
		})
	}
}

func TestPrepareSkipsStoresWithoutASchemaHere(t *testing.T) {
	for name, p := range map[string]config.Persistence{
		"sqlite":    persistence("sqlite"),
		"cassandra": {DefaultStore: "default", DataStores: map[string]config.DataStore{"default": {}}},
	} {
		s := newFakeServer()
		if err := prepare(p, s.open, log.NewNoopLogger()); err != nil || len(s.opened) != 0 {
			t.Errorf("%s: prepare = %v after %d connections, want nil and none", name, err, len(s.opened))
		}
	}
}

func TestPrepareErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		breakIt func(*fakeServer)
		want    string
	}{
		{"server and database unreachable", func(s *fakeServer) { s.openErr[""], s.openErr["temporal"] = boom, boom },
			"unable to connect to the database server to create databases: boom"},
		{"lock refused", func(s *fakeServer) { s.conns["temporal"].execErr = map[string]error{"SELECT pg_advisory_lock": boom} },
			"unable to take the schema lock in database temporal: boom"},
		{"database unreachable", func(s *fakeServer) { s.openErr["temporal"] = boom }, "unable to connect to database temporal: boom"},
		{"visibility unreachable", func(s *fakeServer) { s.openErr["temporal_visibility"] = boom },
			"unable to connect to database temporal_visibility: boom"},
		{"version unreadable", func(s *fakeServer) {
			s.conns["temporal"].tables = []string{versionTable, historyTable}
			s.conns["temporal"].readErr = boom
		}, "unable to read the schema version of database temporal: boom"},
		{"cannot create or read", func(s *fakeServer) {
			s.conns[""].createErr = errors.New("permission denied")
			s.conns["temporal"].listErr = boom
		}, "unable to create database temporal: permission denied"},
		{"cannot read tables", func(s *fakeServer) { s.conns["temporal_visibility"].listErr = boom },
			"unable to read the tables of database temporal_visibility: boom"},
		{"update fails", func(s *fakeServer) { s.conns["temporal"].execErr = map[string]error{"CREATE": boom} },
			"unable to update the schema of database temporal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeServer()
			tt.breakIt(s)
			err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("prepare = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPrepareKeepsGoingWhenTheUnlockFails(t *testing.T) {
	s := newFakeServer()
	s.conns["temporal"].execErr = map[string]error{"SELECT pg_advisory_unlock": errors.New("connection reset")}
	if err := prepare(persistence("postgres12"), s.open, log.NewNoopLogger()); err != nil {
		t.Fatalf("prepare = %v, want nil: the lock goes with the connection", err)
	}
}
