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

package cli

import (
	"maps"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/metrics"
	"go.temporal.io/server/common/persistence/sql"
	"go.temporal.io/server/common/persistence/sql/sqlplugin"
	"go.temporal.io/server/common/resolver"
	sqliteschema "go.temporal.io/server/schema/sqlite"

	"github.com/wso2/integration-workflow-runtime/engine/internal/runtimeconfig"
)

// devSearchAttributes holds WorkflowKind, the search attribute ballerina/workflow registers at start; pre-registering it in a keyword column
// avoids a first start whose workflow is refused before the engine's namespace cache sees the new attribute.
var devSearchAttributes = map[string]string{"Keyword01": "WorkflowKind"}

// createDevNamespaces opens the development database (the SQLite plugin creates its schema) and registers the
// namespaces in it before the server starts; an existing namespace is left as it is.
func createDevNamespaces(p config.Persistence, names []string) error {
	cfg := p.DataStores[p.DefaultStore].SQL
	db, err := sql.NewSQLAdminDB(sqlplugin.DbKindUnknown, cfg, resolver.NewNoopResolver(), log.NewNoopLogger(),
		metrics.NoopMetricsHandler)
	if err != nil {
		return err
	}
	// Never closed: an in-memory SQLite database is deleted with its last connection, and this one outlives start.
	_ = db
	namespaces := make([]*sqliteschema.NamespaceConfig, 0, len(names))
	for _, name := range names {
		ns, err := sqliteschema.NewNamespaceConfig(runtimeconfig.ClusterName, name, false, nil)
		if err != nil {
			return err
		}
		ns.Detail.Config.CustomSearchAttributeAliases = maps.Clone(devSearchAttributes)
		namespaces = append(namespaces, ns)
	}
	return sqliteschema.CreateNamespaces(cfg, namespaces...)
}
