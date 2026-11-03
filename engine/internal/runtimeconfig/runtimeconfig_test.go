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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.temporal.io/server/common/config"
)

// The mapping of every setting is covered by the fixtures in testdata/config (fixtures_test.go); these tests cover
// what a redacted rendering cannot show.

func noEnv(string) (string, bool) { return "", false }

func mustParse(t *testing.T, toml string) *config.Config {
	t.Helper()
	rt, err := parse([]byte(toml), noEnv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &rt.Temporal
}

func TestPasswordFromEnvironment(t *testing.T) {
	t.Parallel()
	lookup := func(name string) (string, bool) { return "from-env", name == "WF_DB_PASSWORD" }
	rt, err := parse([]byte("[database]\ntype = \"postgresql\"\nhost = \"db\"\npassword = \"$env{WF_DB_PASSWORD}\"\n"), lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []string{defaultStore, visibilityStore} {
		if got := rt.Temporal.Persistence.DataStores[store].SQL.Password; got != "from-env" {
			t.Fatalf("%s password = %q, want the environment value", store, got)
		}
	}
}

func TestLoadReadsFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "runtime.toml")
	if err := os.WriteFile(path, []byte("[database]\ntype = \"sqlite\"\ninMemory = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load = %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.toml")); err == nil || !strings.Contains(err.Error(), "unable to read") {
		t.Fatalf("Load of a missing file = %v, want a read error", err)
	}
}

func TestRenderHidesPasswordsWithoutChangingTheConfiguration(t *testing.T) {
	t.Parallel()
	cfg := mustParse(t, "[database]\ntype = \"postgresql\"\nhost = \"db\"\npassword = \"s3cret\"\n")
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "s3cret") || !strings.Contains(string(out), redacted) {
		t.Fatalf("rendered config leaks or omits the password:\n%s", out)
	}
	for _, store := range []string{defaultStore, visibilityStore} {
		if cfg.Persistence.DataStores[store].SQL.Password != "s3cret" {
			t.Fatal("Render changed the configuration it was given")
		}
	}
}
