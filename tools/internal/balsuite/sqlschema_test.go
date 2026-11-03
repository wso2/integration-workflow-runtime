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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeSQLTool records every call in calls.log and fails the steps named in FAIL_STEP with the given message.
const fakeSQLTool = "#!/bin/sh\necho \"pw=$SQL_PASSWORD $@\" >> \"$(dirname \"$0\")/calls.log\"\n" +
	"case \" $* \" in *\" $FAIL_STEP \"*) echo \"$FAIL_MESSAGE\" >&2; exit 1;; esac\nexit 0\n"

func fakeEngineDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script(t, dir, "workflow-engine-sql", fakeSQLTool)
	return dir
}

func TestCreateSQLSchemasRunsEveryStepForBothDatabases(t *testing.T) {
	for database, common := range map[string]string{
		"postgresql": "--plugin postgres12 --ep 127.0.0.1 -p 15432 -u workflow",
		"mysql":      "--plugin mysql8 --ep 127.0.0.1 -p 13306 -u root",
	} {
		t.Run(database, func(t *testing.T) {
			dir := fakeEngineDir(t)
			t.Setenv(DBPasswordEnv, "secret")
			if err := CreateSQLSchemas(context.Background(), database, filepath.Join(dir, "workflow-engine")); err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls.log"))
			if err != nil {
				t.Fatal(err)
			}
			schemas := SQLDatabases[database].schemas
			var want []string
			for _, db := range []struct{ name, schema string }{{"temporal", "temporal"}, {"temporal_visibility", "visibility"}} {
				prefix := "pw=secret " + common + " --db " + db.name + " "
				want = append(want, prefix+"create-database", prefix+"setup-schema -v 0.0",
					prefix+"update-schema --schema-name "+schemas+"/"+db.schema)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if !slices.Equal(lines, want) {
				t.Fatalf("calls:\n%s\nwant:\n%s", calls, strings.Join(want, "\n"))
			}
			for _, line := range lines {
				if args := strings.TrimPrefix(line, "pw=secret "); strings.Contains(args, "secret") {
					t.Errorf("the password is in the arguments: %s", line)
				}
			}
		})
	}
}

func TestCreateSQLSchemasReportsAFailedUpdateDespiteSkippedDuplicates(t *testing.T) {
	dir := fakeEngineDir(t)
	t.Setenv(DBPasswordEnv, "secret")
	t.Setenv("FAIL_STEP", "update-schema")
	t.Setenv("FAIL_MESSAGE", "index already exists, skipping; then: syntax error")
	err := CreateSQLSchemas(context.Background(), "postgresql", filepath.Join(dir, "workflow-engine"))
	if err == nil || !strings.Contains(err.Error(), "update-schema") {
		t.Fatalf("CreateSQLSchemas = %v, want the failed update-schema", err)
	}
}

func TestCreateSQLSchemasAcceptsExistingObjects(t *testing.T) {
	dir := fakeEngineDir(t)
	t.Setenv(DBPasswordEnv, "secret")
	t.Setenv("FAIL_STEP", "create-database")
	t.Setenv("FAIL_MESSAGE", "database temporal already exists")
	if err := CreateSQLSchemas(context.Background(), "mysql", filepath.Join(dir, "workflow-engine")); err != nil {
		t.Fatalf("CreateSQLSchemas = %v, want nil for an existing database", err)
	}
}

func TestCreateSQLSchemasReportsAMissingDatabase(t *testing.T) {
	dir := fakeEngineDir(t)
	t.Setenv(DBPasswordEnv, "secret")
	t.Setenv("FAIL_STEP", "create-database")
	t.Setenv("FAIL_MESSAGE", `database "postgres" does not exist`)
	err := CreateSQLSchemas(context.Background(), "postgresql", filepath.Join(dir, "workflow-engine"))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("CreateSQLSchemas = %v, want the missing database reported", err)
	}
}

func TestCreateSQLSchemasReportsAFailedStep(t *testing.T) {
	dir := fakeEngineDir(t)
	t.Setenv(DBPasswordEnv, "secret")
	t.Setenv("FAIL_STEP", "setup-schema")
	t.Setenv("FAIL_MESSAGE", "connection refused")
	err := CreateSQLSchemas(context.Background(), "mysql", filepath.Join(dir, "workflow-engine"))
	if err == nil || !strings.Contains(err.Error(), "setup-schema -v 0.0 failed for temporal: connection refused") {
		t.Fatalf("CreateSQLSchemas = %v, want the failed step", err)
	}
}

func TestCreateSQLSchemasNeedsThePassword(t *testing.T) {
	dir := fakeEngineDir(t)
	t.Setenv(DBPasswordEnv, "") // registers the restore; the variable must then be absent, not empty
	if err := os.Unsetenv(DBPasswordEnv); err != nil {
		t.Fatal(err)
	}
	err := CreateSQLSchemas(context.Background(), "postgresql", filepath.Join(dir, "workflow-engine"))
	if err == nil || !strings.Contains(err.Error(), DBPasswordEnv+" must be set") {
		t.Fatalf("CreateSQLSchemas = %v, want the missing password error", err)
	}
}

func TestCreateSQLSchemasSkipsSQLite(t *testing.T) {
	if err := CreateSQLSchemas(context.Background(), "sqlite", filepath.Join(t.TempDir(), "workflow-engine")); err != nil {
		t.Fatalf("CreateSQLSchemas = %v, want nil for sqlite", err)
	}
}
