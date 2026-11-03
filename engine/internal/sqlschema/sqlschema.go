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
	"database/sql"
	"errors"
	"fmt"
	"path"
	"slices"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
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

type store struct {
	sql    *config.SQL
	schema string
}

// Prepare creates the workflow and visibility databases when missing, sets up Temporal's schema versioning in an
// empty database, and applies the schema versions this binary ships. Stores that are not PostgreSQL or MySQL are
// left alone: SQLite creates its own schema.
func Prepare(p config.Persistence, logger log.Logger) error {
	return prepare(p, openConnection, logger)
}

func openConnection(cfg *config.SQL, logger log.Logger) (conn, error) {
	return sqltool.NewConnection(cfg, logger)
}

func prepare(p config.Persistence, open opener, logger log.Logger) (err error) {
	stores := sqlStores(p)
	if len(stores) == 0 {
		return nil
	}
	createErrs := createDatabases(stores, open, logger)
	// The lock lives in the workflow database, which every node can open, so the runtime needs no other database.
	first := *stores[0].sql
	first.MaxConns, first.MaxIdleConns, first.MaxConnLifetime = 1, 1, 0
	db, err := open(&first, logger)
	if err != nil {
		return errors.Join(fmt.Errorf("unable to connect to database %s: %w", first.DatabaseName, err), createErrs[0])
	}
	defer db.Close()
	statements := lockStatements[schemaDirs[first.PluginName]]
	if err := db.Exec(statements[0], lockName); err != nil {
		return fmt.Errorf("unable to take the schema lock in database %s: %w", first.DatabaseName, err)
	}
	defer func() {
		if unlockErr := db.Exec(statements[1], lockName); unlockErr != nil {
			logger.Warn("Unable to release the schema lock; it is released when the connection closes", tag.Error(unlockErr))
		}
	}()
	if err := prepareStore(db, stores[0], createErrs[0], logger); err != nil {
		return err
	}
	for i, s := range stores[1:] {
		other, err := open(s.sql, logger)
		if err != nil {
			return errors.Join(fmt.Errorf("unable to connect to database %s: %w", s.sql.DatabaseName, err), createErrs[i+1])
		}
		err = prepareStore(other, s, createErrs[i+1], logger)
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
