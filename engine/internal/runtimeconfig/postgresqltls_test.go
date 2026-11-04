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

package runtimeconfig

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jmoiron/sqlx"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql/driver"
	"go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql/session"
	"go.temporal.io/server/common/resolver"
)

var errCaptured = errors.New("dsn captured")

// capturingDriver records the DSN Temporal builds and stops before connecting.
type capturingDriver struct {
	driver.PGXDriver
	dsn string
}

func (d *capturingDriver) CreateConnection(dsn string) (*sqlx.DB, error) {
	d.dsn = dsn
	return nil, errCaptured
}

// The engine refuses database.tls.serverName for PostgreSQL because Temporal drops it; when this test fails, Temporal
// passes the name through and the rule in validate can go.
func TestTemporalPostgreSQLIgnoresTheTLSServerName(t *testing.T) {
	cfg := mustParse(t, "[database]\ntype = \"postgresql\"\nhost = \"127.0.0.1\"\n\n[database.tls]\nenabled = true\n")
	store := cfg.Persistence.DataStores[cfg.Persistence.DefaultStore].SQL
	store.TLS.ServerName = "db.example.com"

	d := &capturingDriver{}
	if _, err := session.NewSession(store, d, resolver.NewNoopResolver()); !errors.Is(err, errCaptured) {
		t.Fatalf("NewSession: %v, want the captured DSN", err)
	}
	pc, err := pgx.ParseConfig(d.dsn)
	if err != nil {
		t.Fatalf("parse the DSN: %v", err)
	}
	if pc.TLSConfig == nil {
		t.Fatal("the DSN does not use TLS")
	}
	if got := pc.TLSConfig.ServerName; got != "127.0.0.1" {
		t.Fatalf("pgx verifies the certificate against %q; Temporal now passes the server name through", got)
	}
}
