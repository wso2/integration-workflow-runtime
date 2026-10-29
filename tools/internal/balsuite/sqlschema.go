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
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DBPasswordEnv holds the password of the test database user for PostgreSQL and MySQL.
const DBPasswordEnv = "WORKFLOW_TEST_DB_PASSWORD" //nolint:gosec // the variable name, not a credential

const schemaToolTimeout = 2 * time.Minute

// sqlDatabase is the test-only schema setup for one SQL database; the launcher takes this over later.
type sqlDatabase struct {
	plugin  string
	port    string
	user    string
	schemas string
}

// SQLDatabases maps the --database values that need schemas to how workflow-engine-sql reaches them.
var SQLDatabases = map[string]sqlDatabase{
	"postgresql": {plugin: "postgres12", port: "15432", user: "workflow", schemas: "postgresql/v12"},
	"mysql":      {plugin: "mysql8", port: "13306", user: "root", schemas: "mysql/v8"},
}

// CreateSQLSchemas creates both Temporal databases and installs their schemas with the workflow-engine-sql
// binary beside the engine. Databases and schemas that already exist are left alone.
func CreateSQLSchemas(ctx context.Context, database, engine string) error {
	settings, ok := SQLDatabases[database]
	if !ok {
		return nil
	}
	password, ok := os.LookupEnv(DBPasswordEnv)
	if !ok {
		return fmt.Errorf("%s must be set for the %s database", DBPasswordEnv, database)
	}
	tool := filepath.Join(filepath.Dir(engine), "workflow-engine-sql"+filepath.Ext(engine))
	common := []string{"--plugin", settings.plugin, "--ep", "127.0.0.1", "-p", settings.port,
		"-u", settings.user, "--pw", password}
	for _, db := range []struct{ name, schema string }{{"temporal", "temporal"}, {"temporal_visibility", "visibility"}} {
		steps := [][]string{
			{"create-database"},
			{"setup-schema", "-v", "0.0"},
			{"update-schema", "--schema-name", settings.schemas + "/" + db.schema},
		}
		for _, step := range steps {
			args := append(append(append([]string{}, common...), "--db", db.name), step...)
			r := runQuiet(ctx, schemaToolTimeout, tool, args...)
			if r.code != 0 && !strings.Contains(r.output(), "exist") {
				return fmt.Errorf("%s failed for %s: %s", strings.Join(step, " "), db.name, r.message())
			}
		}
	}
	return nil
}
