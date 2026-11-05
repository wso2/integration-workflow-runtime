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

// Package sqlschema creates Temporal's SQL databases and brings their schemas to the version this binary ships.
package sqlschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"slices"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	mysqlsession "go.temporal.io/server/common/persistence/sql/sqlplugin/mysql/session"
	pgdriver "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql/driver"
	pgsession "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql/session"
	"go.temporal.io/server/common/resolver"
	"go.temporal.io/server/tools/common/schema"
	sqltool "go.temporal.io/server/tools/sql"
)

const (
	// lockName serializes schema changes between nodes that start together against the same database server.
	lockName = "workflow-engine-schema"
	// versionTable and historyTable exist once Temporal's schema versioning is set up in a database.
	versionTable = "schema_version"
	historyTable = "schema_update_history"
	// initialVersion is where Temporal's versioned schema starts, as with `setup-schema -v 0.0`.
	initialVersion = "0.0"
)

// schemaDirs maps Temporal's SQL plugins to their schema directories embedded in the binary.
var schemaDirs = map[string]string{
	"postgres12":     "postgresql/v12",
	"postgres12_pgx": "postgresql/v12",
	"mysql8":         "mysql/v8",
}

// lockStatements take and release a server-wide lock held by the session that took it.
var lockStatements = map[string][2]string{
	"postgresql/v12": {"SELECT pg_advisory_lock(hashtext($1))", "SELECT pg_advisory_unlock(hashtext($1))"},
	"mysql/v8":       {"SELECT GET_LOCK(?, -1)", "SELECT RELEASE_LOCK(?)"},
}

// conn is what Prepare needs from a database connection; Temporal's schema tool connection provides it.
type conn interface {
	schema.DB
	CreateDatabase(name string) error
	ListTables() ([]string, error)
}

type opener func(cfg *config.SQL, logger log.Logger) (conn, error)

// sessionLock runs statements on one database session, so a lock it takes is held until that session ends.
type sessionLock interface {
	exec(stmt string, args ...any) error
	// alive fails once the session has ended, and with it the lock.
	alive() error
	close()
}

type lockOpener func(cfg *config.SQL) (sessionLock, error)

type store struct {
	sql    *config.SQL
	schema string
}

// Prepare creates the workflow and visibility databases when missing, sets up Temporal's schema versioning in an
// empty database, and applies the schema versions this binary ships. Stores that are not PostgreSQL or MySQL are
// left alone: SQLite creates its own schema.
func Prepare(p config.Persistence, logger log.Logger) error {
	return prepare(p, openConnection, openLock, logger)
}

func openConnection(cfg *config.SQL, logger log.Logger) (conn, error) {
	return sqltool.NewConnection(cfg, logger)
}

func prepare(p config.Persistence, open opener, openLock lockOpener, logger log.Logger) (err error) {
	stores := sqlStores(p)
	if len(stores) == 0 {
		return nil
	}
	createErrs := createDatabases(stores, open, logger)
	// The lock lives in the workflow database, which every node can open, so the runtime needs no other database.
	first := *stores[0].sql
	first.MaxConns, first.MaxIdleConns, first.MaxConnLifetime = 1, 1, 0
	lock, err := openLock(&first)
	if err != nil {
		return errors.Join(fmt.Errorf("unable to connect to database %s: %w", first.DatabaseName, err), createErrs[0])
	}
	defer lock.close()
	statements := lockStatements[schemaDirs[first.PluginName]]
	if err := lock.exec(statements[0], lockName); err != nil {
		return fmt.Errorf("unable to take the schema lock in database %s: %w", first.DatabaseName, err)
	}
	defer func() {
		if unlockErr := lock.exec(statements[1], lockName); unlockErr != nil {
			logger.Warn("Unable to release the schema lock; it is released when the connection closes", tag.Error(unlockErr))
		}
	}()
	opened, err := open(&first, logger)
	if err != nil {
		return errors.Join(fmt.Errorf("unable to connect to database %s: %w", first.DatabaseName, err), createErrs[0])
	}
	defer opened.Close()
	db := guarded{opened, lock}
	if err := prepareStore(db, stores[0], createErrs[0], logger); err != nil {
		return err
	}
	for i, s := range stores[1:] {
		other, err := open(s.sql, logger)
		if err != nil {
			return errors.Join(fmt.Errorf("unable to connect to database %s: %w", s.sql.DatabaseName, err), createErrs[i+1])
		}
		err = prepareStore(guarded{other, lock}, s, createErrs[i+1], logger)
		other.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// createDatabases creates the stores' databases through the server's maintenance database and returns each one's
// error. A runtime role that cannot reach that database or create databases still uses databases a DBA created.
func createDatabases(stores []store, open opener, logger log.Logger) []error {
	errs := make([]error, len(stores))
	server := *stores[0].sql
	server.DatabaseName = ""
	server.MaxConns, server.MaxIdleConns, server.MaxConnLifetime = 1, 1, 0
	admin, err := open(&server, logger)
	if err != nil {
		logger.Info("Unable to reach the database server's maintenance database; using existing databases", tag.Error(err))
		for i := range errs {
			errs[i] = fmt.Errorf("unable to connect to the database server to create databases: %w", err)
		}
		return errs
	}
	defer admin.Close()
	for i, s := range stores {
		errs[i] = admin.CreateDatabase(s.sql.DatabaseName)
	}
	return errs
}

// sqlStores returns the workflow store and then the visibility store, when their plugins have a schema here.
func sqlStores(p config.Persistence) []store {
	var stores []store
	for _, named := range []struct{ name, schema string }{{p.DefaultStore, "temporal"}, {p.VisibilityStore, "visibility"}} {
		ds, ok := p.DataStores[named.name]
		if !ok || ds.SQL == nil {
			continue
		}
		if dir, ok := schemaDirs[ds.SQL.PluginName]; ok {
			stores = append(stores, store{sql: ds.SQL, schema: path.Join(dir, named.schema)})
		}
	}
	return stores
}

func prepareStore(db conn, s store, createErr error, logger log.Logger) error {
	name := s.sql.DatabaseName
	tables, err := db.ListTables()
	if err != nil {
		if createErr != nil {
			return fmt.Errorf("unable to create database %s: %w", name, createErr)
		}
		return fmt.Errorf("unable to read the tables of database %s: %w", name, err)
	}
	versioned, err := hasVersion(db, tables)
	if err != nil {
		return fmt.Errorf("unable to read the schema version of database %s: %w", name, err)
	}
	if !versioned {
		// Also resumes a setup that stopped after creating the version tables but before recording 0.0.
		logger.Info("Setting up Temporal's schema versioning", tag.NewStringTag("database", name))
		setup := &schema.SetupConfig{InitialVersion: initialVersion}
		if err := schema.NewSetupSchemaTask(db, setup, logger).Run(); err != nil {
			return fmt.Errorf("unable to set up the schema of database %s: %w", name, err)
		}
	}
	update := &schema.UpdateConfig{DBName: name, SchemaName: s.schema}
	if err := schema.NewUpdateSchemaTask(db, update, logger).Run(); err != nil {
		return fmt.Errorf("unable to update the schema of database %s: %w", name, err)
	}
	return nil
}

// hasVersion reports whether both version tables exist and hold this database's version row. Any other read error
// is returned, so a failed read never reaches setup, which would record 0.0 over a valid version.
func hasVersion(db conn, tables []string) (bool, error) {
	if !slices.Contains(tables, versionTable) || !slices.Contains(tables, historyTable) {
		return false, nil
	}
	if _, err := db.ReadSchemaVersion(); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// guarded checks that the schema lock is still held before each schema change, so a node whose lock session ended
// stops instead of migrating alongside the node that took the lock next.
type guarded struct {
	conn
	lock sessionLock
}

func (g guarded) held() error {
	if err := g.lock.alive(); err != nil {
		return fmt.Errorf("lost the schema lock: %w", err)
	}
	return nil
}

func (g guarded) Exec(stmt string, args ...any) error {
	if err := g.held(); err != nil {
		return err
	}
	return g.conn.Exec(stmt, args...)
}

func (g guarded) CreateSchemaVersionTables() error {
	if err := g.held(); err != nil {
		return err
	}
	return g.conn.CreateSchemaVersionTables()
}

func (g guarded) UpdateSchemaVersion(newVersion, minCompatibleVersion string) error {
	if err := g.held(); err != nil {
		return err
	}
	return g.conn.UpdateSchemaVersion(newVersion, minCompatibleVersion)
}

func (g guarded) WriteSchemaUpdateLog(oldVersion, newVersion, manifestMD5, desc string) error {
	if err := g.held(); err != nil {
		return err
	}
	return g.conn.WriteSchemaUpdateLog(oldVersion, newVersion, manifestMD5, desc)
}

func (g guarded) DropAllTables() error {
	if err := g.held(); err != nil {
		return err
	}
	return g.conn.DropAllTables()
}

// pinnedLock holds one physical connection. database/sql retries a pooled statement on a new connection when the old
// one breaks, which would drop the lock unnoticed; a pinned connection reports the break instead.
type pinnedLock struct {
	db   *sql.DB
	conn *sql.Conn
}

// openLock connects as Temporal's plugin does, so the lock uses the same address, credentials and TLS.
func openLock(cfg *config.SQL) (sessionLock, error) {
	var db *sql.DB
	switch cfg.PluginName {
	case "postgres12", "postgres12_pgx":
		var d pgdriver.Driver = &pgdriver.PQDriver{}
		if cfg.PluginName == "postgres12_pgx" {
			d = &pgdriver.PGXDriver{}
		}
		s, err := pgsession.NewSession(cfg, d, resolver.NewNoopResolver())
		if err != nil {
			return nil, err
		}
		db = s.DB.DB
	case "mysql8":
		s, err := mysqlsession.NewSession(sqlplugin.DbKindMain, cfg, resolver.NewNoopResolver())
		if err != nil {
			return nil, err
		}
		db = s.DB.DB
	default:
		return nil, fmt.Errorf("no schema lock for plugin %s", cfg.PluginName)
	}
	c, err := db.Conn(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &pinnedLock{db: db, conn: c}, nil
}

func (l *pinnedLock) exec(stmt string, args ...any) error {
	_, err := l.conn.ExecContext(context.Background(), stmt, args...)
	return err
}

// alive runs a statement rather than a ping: a driver without a ping reports every connection as alive.
func (l *pinnedLock) alive() error { return l.exec("SELECT 1") }

func (l *pinnedLock) close() {
	_ = l.conn.Close()
	_ = l.db.Close()
}
