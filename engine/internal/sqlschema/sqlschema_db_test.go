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
	"os"
	"sync"
	"testing"
	"time"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/mysql"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/postgresql"
	sqltool "go.temporal.io/server/tools/sql"
)

// testDBPasswordEnv enables these tests against the databases the Ballerina suites use (tests/engine/*.toml).
const testDBPasswordEnv = "WORKFLOW_TEST_DB_PASSWORD" //nolint:gosec // the variable name, not a credential

func TestPrepareOnRealDatabases(t *testing.T) {
	password, ok := os.LookupEnv(testDBPasswordEnv)
	if !ok {
		t.Skipf("%s is not set; start the test databases to run this", testDBPasswordEnv)
	}
	servers := []struct{ plugin, addr, user string }{
		{"postgres12", "127.0.0.1:15432", "workflow"},
		{"mysql8", "127.0.0.1:13306", "root"},
	}
	for _, server := range servers {
		t.Run(server.plugin, func(t *testing.T) {
			suffix := time.Now().Format("150405")
			p := persistence(server.plugin)
			for _, ds := range p.DataStores {
				ds.SQL.ConnectAddr, ds.SQL.User, ds.SQL.Password = server.addr, server.user, password
				ds.SQL.DatabaseName = "schema_test_" + ds.SQL.DatabaseName + "_" + suffix
				t.Cleanup(func() { dropDatabase(t, *ds.SQL) })
			}
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
			for name, want := range map[string]string{p.DefaultStore: "1.19", p.VisibilityStore: "1.14"} {
				if got := readVersion(t, *p.DataStores[name].SQL); got != want {
					t.Errorf("%s version = %s, want %s", name, got, want)
				}
			}
		})
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
