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
	"path/filepath"
	"testing"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/metrics"
	tpersistence "go.temporal.io/server/common/persistence"
	"go.temporal.io/server/common/persistence/serialization"
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	_ "go.temporal.io/server/common/persistence/sql/sqlplugin/sqlite"
	"go.temporal.io/server/common/resolver"

	"github.com/wso2/integration-workflow-runtime/engine/internal/runtimeconfig"
)

func TestCreateDevNamespacesOnSQLite(t *testing.T) {
	for name, opts := range map[string]runtimeconfig.DevOptions{
		"memory": {IP: "127.0.0.1", Port: 19233, LogLevel: "error"},
		"file":   {IP: "127.0.0.1", Port: 19233, LogLevel: "error", DBFile: filepath.Join(t.TempDir(), "dev.db")},
	} {
		rt, err := runtimeconfig.Dev(opts)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 { // the second call finds the namespaces and leaves them
			if err := CreateDevNamespaces(rt.Temporal.Persistence, runtimeconfig.ClusterName, []string{"default", "orders"}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		for _, ns := range []string{"default", "orders"} {
			aliases := namespaceAliases(t, rt.Temporal.Persistence, ns)
			if aliases["Keyword01"] != "WorkflowKind" {
				t.Errorf("%s: namespace %s aliases = %v, want Keyword01 as WorkflowKind", name, ns, aliases)
			}
		}
	}
}

// namespaceAliases reads a namespace from the development store, failing the test when it is missing.
func namespaceAliases(t *testing.T, p config.Persistence, name string) map[string]string {
	t.Helper()
	db, err := sql.NewSQLDB(sqlplugin.DbKindMain, p.DataStores[p.DefaultStore].SQL, resolver.NewNoopResolver(),
		log.NewNoopLogger(), metrics.NoopMetricsHandler)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.SelectFromNamespace(context.Background(), sqlplugin.NamespaceFilter{Name: &name})
	if err != nil || len(rows) != 1 {
		t.Fatalf("namespace %s: rows = %d, %v", name, len(rows), err)
	}
	detail, err := serialization.NewSerializer().NamespaceDetailFromBlob(tpersistence.NewDataBlob(rows[0].Data, rows[0].DataEncoding))
	if err != nil {
		t.Fatal(err)
	}
	return detail.GetConfig().GetCustomSearchAttributeAliases()
}
