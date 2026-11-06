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
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"path"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
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

// lockStatements try for, and release, a lock held by the session that took it: per database on PostgreSQL, and
// server-wide on MySQL, where a node holds it for one database at a time. The try returns 1 once the lock is taken.
var lockStatements = map[string][2]string{
	"postgresql/v12": {"SELECT CASE WHEN pg_try_advisory_lock(hashtext($1)) THEN 1 ELSE 0 END",
		"SELECT pg_advisory_unlock(hashtext($1))"},
	"mysql/v8": {"SELECT GET_LOCK(?, 0)", "SELECT RELEASE_LOCK(?)"},
}

// lockRetry is how long a node waits between tries for the lock. Waiting inside a lock call instead would hold a
// transaction open, and PostgreSQL's CREATE INDEX CONCURRENTLY in the holder's migration waits for it: a deadlock.
var lockRetry = 500 * time.Millisecond

// conn is what Prepare needs from a database connection; Temporal's schema tool connection provides it.
type conn interface {
	schema.DB
	CreateDatabase(name string) error
	ListTables() ([]string, error)
}

type opener func(cfg *config.SQL, logger log.Logger) (conn, error)

// session runs a store's schema lock and its schema changes on one database session.
type session interface {
	conn
	// lock waits until it holds the schema lock.
	lock(logger log.Logger) error
	unlock() error
}

type sessionOpener func(cfg *config.SQL, logger log.Logger) (session, error)

type store struct {
	sql    *config.SQL
	schema string
}

// Prepare creates the workflow and visibility databases when missing, sets up Temporal's schema versioning in an
// empty database, and applies the schema versions this binary ships. Stores that are not PostgreSQL or MySQL are
// left alone: SQLite creates its own schema.
func Prepare(p config.Persistence, logger log.Logger) error {
	return prepare(p, openConnection, openSession, logger)
}

func openConnection(cfg *config.SQL, logger log.Logger) (conn, error) {
	return sqltool.NewConnection(cfg, logger)
}

func prepare(p config.Persistence, open opener, openSession sessionOpener, logger log.Logger) error {
	stores := sqlStores(p)
	if len(stores) == 0 {
		return nil
	}
	createErrs := createDatabases(stores, open, logger)
	for i, s := range stores {
		if err := prepareLocked(s, createErrs[i], openSession, logger); err != nil {
			return err
		}
	}
	return nil
}

// prepareLocked prepares a store while holding the schema lock in its database. The lock and the schema changes share
// one session that is never replaced, so when the session ends, the lock and every change still running end with it.
func prepareLocked(s store, createErr error, openSession sessionOpener, logger log.Logger) (err error) {
	name := s.sql.DatabaseName
	db, err := openSession(s.sql, logger)
	if err != nil {
		return errors.Join(fmt.Errorf("unable to connect to database %s: %w", name, err), createErr)
	}
	defer db.Close()
	if err := db.lock(logger); err != nil {
		return errors.Join(fmt.Errorf("unable to take the schema lock in database %s: %w", name, err), createErr)
	}
	defer func() {
		if unlockErr := db.unlock(); unlockErr != nil && err == nil {
			logger.Warn("Unable to release the schema lock; it is released when the connection closes", tag.Error(unlockErr))
		}
	}()
	if err := prepareStore(db, s, createErr, logger); err != nil {
		if db.Exec("SELECT 1") != nil {
			return fmt.Errorf("lost the schema lock in database %s: %w", name, err)
		}
		return err
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
			return errors.Join(fmt.Errorf("unable to create database %s: %w", name, createErr),
				fmt.Errorf("unable to read the tables of database %s: %w", name, err))
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

// errSessionEnded refuses a new connection in place of a schema session that ended: it would not hold the lock.
var errSessionEnded = errors.New("the database session that held the schema lock ended")

// singleSession is Temporal's schema tool connection over a pool of one connection that is never replaced; the lock
// queries go through the same pool, so they run on the same session.
type singleSession struct {
	*sqltool.Connection
	db         *sqlx.DB
	statements [2]string
}

// openSession connects through Temporal's schema tool on one database session.
func openSession(cfg *config.SQL, logger log.Logger) (session, error) {
	single := *cfg
	single.MaxConns, single.MaxIdleConns, single.MaxConnLifetime = 1, 1, 0
	var mu sync.Mutex
	var db *sqlx.DB
	var connectErr error
	// Temporal calls Connect again to refresh a broken pool; refusing that keeps the schema tool off a new session.
	single.Connect = func(c *config.SQL) (*sqlx.DB, error) {
		mu.Lock()
		defer mu.Unlock()
		if db != nil {
			return nil, errSessionEnded
		}
		db, connectErr = connectOnce(c)
		return db, connectErr
	}
	c, err := sqltool.NewConnection(&single, logger)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	if db == nil {
		c.Close()
		return nil, connectErr
	}
	return &singleSession{Connection: c, db: db, statements: lockStatements[schemaDirs[cfg.PluginName]]}, nil
}

func (s *singleSession) lock(logger log.Logger) error {
	for waited := false; ; waited = true {
		var taken int
		if err := s.db.QueryRow(s.statements[0], lockName).Scan(&taken); err != nil {
			return err
		}
		if taken == 1 {
			return nil
		}
		if !waited {
			logger.Info("Waiting for another node to finish preparing the schema")
		}
		time.Sleep(lockRetry)
	}
}

func (s *singleSession) unlock() error {
	_, err := s.db.Exec(s.statements[1], lockName)
	return err
}

// connectOnce connects as Temporal's plugin does, so the session uses the same address, credentials and TLS, in a
// pool that opens a single connection.
func connectOnce(cfg *config.SQL) (*sqlx.DB, error) {
	c := *cfg
	c.Connect = nil
	switch c.PluginName {
	case "postgres12", "postgres12_pgx":
		d := onceDriver{Driver: &pgdriver.PQDriver{}, name: "postgres"}
		if c.PluginName == "postgres12_pgx" {
			d = onceDriver{Driver: &pgdriver.PGXDriver{}, name: "pgx"}
		}
		s, err := pgsession.NewSession(&c, d, resolver.NewNoopResolver())
		if err != nil {
			return nil, err
		}
		return s.DB, nil
	case "mysql8":
		c.ConnectProtocol = onceNetwork(c.ConnectProtocol)
		s, err := mysqlsession.NewSession(sqlplugin.DbKindUnknown, &c, resolver.NewNoopResolver())
		if err != nil {
			return nil, err
		}
		return s.DB, nil
	default:
		return nil, fmt.Errorf("no schema lock for plugin %s", c.PluginName)
	}
}

// onceDriver opens PostgreSQL pools over a onceConnector; Temporal's driver still classifies the errors.
type onceDriver struct {
	pgdriver.Driver
	name string
}

func (d onceDriver) CreateConnection(dsn string) (*sqlx.DB, error) {
	pool, err := sql.Open(d.name, dsn)
	if err != nil {
		return nil, err
	}
	drv := pool.Driver()
	_ = pool.Close()
	var connector driver.Connector = dsnConnector{drv: drv, dsn: dsn}
	if dc, ok := drv.(driver.DriverContext); ok {
		if connector, err = dc.OpenConnector(dsn); err != nil {
			return nil, err
		}
	}
	return sqlx.NewDb(sql.OpenDB(&onceConnector{Connector: connector}), d.name), nil
}

// CreateRefreshableConnection resolves the address once: a single session never needs a refreshed password.
func (d onceDriver) CreateRefreshableConnection(buildDSN func() (string, error)) (*sqlx.DB, error) {
	dsn, err := buildDSN()
	if err != nil {
		return nil, err
	}
	return d.CreateConnection(dsn)
}

// dsnConnector opens connections for a driver without its own connector, as database/sql does.
type dsnConnector struct {
	drv driver.Driver
	dsn string
}

func (c dsnConnector) Connect(context.Context) (driver.Conn, error) { return c.drv.Open(c.dsn) }
func (c dsnConnector) Driver() driver.Driver                        { return c.drv }

// onceConnector hands out one connection. database/sql replaces a broken connection with a new one, which would be a
// new session without the schema lock.
type onceConnector struct {
	driver.Connector
	mu   sync.Mutex
	used bool
}

func (c *onceConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used {
		return nil, errSessionEnded
	}
	conn, err := c.Connector.Connect(ctx)
	c.used = err == nil
	return conn, err
}

var onceNetworks atomic.Uint64

// onceNetwork registers a MySQL network that dials once, so the driver cannot replace a session that ended.
func onceNetwork(protocol string) string {
	if protocol == "" {
		protocol = "tcp"
	}
	name := fmt.Sprintf("workflow-schema-%d", onceNetworks.Add(1))
	var mu sync.Mutex
	used := false
	mysql.RegisterDialContext(name, func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		if used {
			return nil, errSessionEnded
		}
		// The driver adds MySQL's default port only on its own tcp network.
		if _, _, err := net.SplitHostPort(addr); err != nil && protocol == "tcp" {
			addr = net.JoinHostPort(addr, "3306")
		}
		c, err := (&net.Dialer{}).DialContext(ctx, protocol, addr)
		used = err == nil
		return c, err
	})
	return name
}
