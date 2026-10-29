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
	"strings"
	"testing"
)

// fakeSQLTool records every call in calls.log and fails the steps named in FAIL_STEP with the given message.
const fakeSQLTool = "#!/bin/sh\necho \"$@\" >> \"$(dirname \"$0\")/calls.log\"\n" +
	"case \" $* \" in *\" $FAIL_STEP \"*) echo \"$FAIL_MESSAGE\" >&2; exit 1;; esac\nexit 0\n"

func fakeEngineDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script(t, dir, "workflow-engine-sql", fakeSQLTool)
	return dir
}

func TestCreateSQLSchemasRunsEveryStepForBothDatabases(t *testing.T) {
	dir := fakeEngineDir(t)
	t.Setenv(DBPasswordEnv, "secret")
	if err := CreateSQLSchemas(context.Background(), "postgresql", filepath.Join(dir, "workflow-engine")); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 6 {
		t.Fatalf("got %d calls, want 6:\n%s", len(lines), calls)
	}
	for _, want := range []string{
		"--plugin postgres12 --ep 127.0.0.1 -p 15432 -u workflow --pw secret --db temporal create-database",
		"--db temporal setup-schema -v 0.0",
		"--db temporal update-schema --schema-name postgresql/v12/temporal",
		"--db temporal_visibility update-schema --schema-name postgresql/v12/visibility",
	} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("calls lack %q:\n%s", want, calls)
		}
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
