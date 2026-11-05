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
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// upgradeDocker logs its calls; the schema version is 1.18 until the engine marks the upgrade done. FAIL_DOCKER
// fails a call whose arguments contain it, and DEAD_SERVER makes the old server's container stop.
const upgradeDocker = `#!/bin/sh
dir=$(dirname "$0")
echo "$@" >> "$dir/docker.log"
case "$*" in *"$FAIL_DOCKER"*) [ -n "$FAIL_DOCKER" ] && { echo "docker failed" >&2; exit 1; };; esac
case "$*" in
  *curr_version*) if [ -f "$dir/upgraded" ]; then echo 1.19; else echo 1.18; fi;;
  inspect*) if [ -n "$DEAD_SERVER" ]; then echo false; else echo true; fi;;
  "run -d"*) [ -z "$DEAD_SERVER" ] && touch "$dir/serving";;
  stop*) rm -f "$dir/serving";;
esac
exit 0
`

// upgradeTemporal reports SERVING while the old server or the engine runs, and a completed activity unless
// NO_ACTIVITY is set.
const upgradeTemporal = `#!/bin/sh
case "$1 $2 $3" in
  "operator cluster health") [ -f "$(dirname "$0")/serving" ] && echo SERVING;;
  "workflow show"*) [ -z "$NO_ACTIVITY" ] && echo '{"eventType": "EVENT_TYPE_ACTIVITY_TASK_COMPLETED"}';;
esac
exit 0
`

// upgradeEngine upgrades the schema unless STALE_SCHEMA is set, serves, and runs until terminated.
const upgradeEngine = `#!/bin/sh
dir=$(dirname "$0")
[ -z "$STALE_SCHEMA" ] && touch "$dir/upgraded"
touch "$dir/serving"
trap 'exit 0' TERM
while :; do sleep 1; done
`

// upgradeBal records each run's groups and fails the group named in FAIL_GROUP.
const upgradeBal = `#!/bin/sh
echo "$@" >> "$PWD/ran"
case "$*" in *"$FAIL_GROUP"*) [ -n "$FAIL_GROUP" ] && exit 1;; esac
exit 0
`

func upgradeOptions(t *testing.T) UpgradeOptions {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{filepath.Join("tests", "ballerina", UpgradeSuite), filepath.Join("tests", "engine")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	toml := "[database]\ntype = \"postgresql\"\nname = \"temporal\"\nvisibilityName = \"temporal_visibility\"\n"
	if err := os.WriteFile(filepath.Join(root, "tests", "engine", "runtime-postgresql.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return UpgradeOptions{
		Database:       "postgresql",
		Root:           root,
		Engine:         script(t, root, "engine", upgradeEngine),
		Bal:            script(t, root, "bal", upgradeBal),
		Temporal:       script(t, root, "temporal", upgradeTemporal),
		Docker:         script(t, root, "docker", upgradeDocker),
		OldVersion:     "1.30.9",
		Password:       "s3cret",
		LogDir:         filepath.Join(root, "logs"),
		StartupTimeout: 5 * time.Second,
		TestTimeout:    10 * time.Second,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestRunUpgradeFinishesTheWorkflowOnTheEngine(t *testing.T) {
	o := upgradeOptions(t)
	var stdout, stderr bytes.Buffer
	if code := RunUpgrade(context.Background(), o, &stdout, &stderr); code != 0 {
		t.Fatalf("RunUpgrade = %d, want 0; stderr: %s", code, stderr.String())
	}
	ran := readFile(t, filepath.Join(o.Root, "tests", "ballerina", UpgradeSuite, "ran"))
	if ran != "test --groups before\ntest --groups after\n" {
		t.Errorf("bal runs = %q, want the before group and then the after group", ran)
	}
	if !strings.Contains(stdout.String(), "schema 1.19, 1.19, was 1.18, 1.18") {
		t.Errorf("stdout = %q, want the schema versions before and after", stdout.String())
	}
	docker := readFile(t, filepath.Join(o.Root, "docker.log"))
	order := []string{"temporalio/admin-tools:1.30.9", "temporalio/server:1.30.9", "stop -t 30", "DROP DATABASE"}
	last := -1
	for _, step := range order {
		at := strings.Index(docker, step)
		if at <= last {
			t.Fatalf("docker calls out of order or missing %q:\n%s", step, docker)
		}
		last = at
	}
	if strings.Contains(docker, o.Password) {
		t.Errorf("the password reached docker's arguments:\n%s", docker)
	}
	if _, err := os.Stat(filepath.Join(o.LogDir, "temporal-1.30.9-postgresql.log")); err != nil {
		t.Errorf("the old server's log was not kept: %v", err)
	}
}

func TestRunUpgradeWritesTheOldServerConfiguration(t *testing.T) {
	var b strings.Builder
	err := oldServerConfig.Execute(&b, map[string]any{"Plugin": "mysql8", "Host": dockerHost, "Port": "13306", "User": "root",
		"Password": "pw", "Stores": []map[string]string{{"Store": "default", "Name": "a"}, {"Store": "visibility", "Name": "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"numHistoryShards: 512", "currentClusterName: active", "databaseName: a",
		"databaseName: b", "connectAddr: host.docker.internal:13306", "pluginName: mysql8"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("configuration lacks %q:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), "internal-frontend") {
		t.Error("the old server must run without an internal frontend")
	}
}

func TestRunUpgradeQuotesTheCredentials(t *testing.T) {
	for _, password := range []string{`a"b`, `a\nb`, "line\nbreak", `back\slash`, "colon: hash #", "'single'"} {
		var b strings.Builder
		err := oldServerConfig.Execute(&b, map[string]any{"Plugin": "postgres12", "Host": dockerHost, "Port": "15432",
			"User": "work\"flow", "Password": password, "Stores": []map[string]string{{"Store": "default", "Name": "a"}}})
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"user": "work\"flow", "password": password} {
			if got := scalar(t, b.String(), key); got != want {
				t.Errorf("%s = %q, want %q:\n%s", key, got, want, b.String())
			}
		}
	}
}

// scalar reads the double-quoted value of key, which YAML unescapes as JSON does for these characters.
func scalar(t *testing.T, config, key string) string {
	t.Helper()
	for line := range strings.Lines(config) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+": "); ok {
			var s string
			if err := json.Unmarshal([]byte(v), &s); err != nil {
				t.Fatalf("%s is not one quoted scalar: %s", key, v)
			}
			return s
		}
	}
	t.Fatalf("configuration lacks %s:\n%s", key, config)
	return ""
}

func TestRunUpgradeFailures(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"old schema not installed", map[string]string{"FAIL_DOCKER": "admin-tools"}, "unable to install the schema of Temporal 1.30.9"},
		{"version unreadable", map[string]string{"FAIL_DOCKER": "curr_version"}, "unable to read the schema version"},
		{"old server not started", map[string]string{"FAIL_DOCKER": "temporalio/server"}, "unable to start Temporal 1.30.9"},
		{"old server exits", map[string]string{"DEAD_SERVER": "1"}, "server exited before it was ready"},
		{"before group fails", map[string]string{"FAIL_GROUP": "before"}, "the before group failed"},
		{"no activity before the upgrade", map[string]string{"NO_ACTIVITY": "1"}, "has no completed activity"},
		{"schema not upgraded", map[string]string{"STALE_SCHEMA": "1"}, "is still at schema 1.18"},
		{"after group fails", map[string]string{"FAIL_GROUP": "after"}, "the after group failed on the upgraded engine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var stdout, stderr bytes.Buffer
			if code := RunUpgrade(context.Background(), upgradeOptions(t), &stdout, &stderr); code == 0 ||
				!strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("RunUpgrade = %d, stderr %q; want a failure with %q", code, stderr.String(), tt.want)
			}
		})
	}
}

func TestRunUpgradeNeedsAVersionedDatabase(t *testing.T) {
	o := upgradeOptions(t)
	o.Database = "sqlite"
	var stdout, stderr bytes.Buffer
	if code := RunUpgrade(context.Background(), o, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "postgresql or mysql") {
		t.Fatalf("RunUpgrade = %d, stderr %q; want 2", code, stderr.String())
	}
}

func TestRunUpgradeUsesTheMySQLClient(t *testing.T) {
	o := upgradeOptions(t)
	o.Database = "mysql"
	toml := "[database]\ntype = \"mysql\"\nname = \"temporal\"\nvisibilityName = \"temporal_visibility\"\n"
	if err := os.WriteFile(filepath.Join(o.Root, "tests", "engine", "runtime-mysql.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunUpgrade(context.Background(), o, &stdout, &stderr); code != 0 {
		t.Fatalf("RunUpgrade = %d; stderr: %s", code, stderr.String())
	}
	docker := readFile(t, filepath.Join(o.Root, "docker.log"))
	if !strings.Contains(docker, "-e MYSQL_PWD mysql:8.4 mysql -h host.docker.internal -P 13306 -u root") ||
		strings.Contains(docker, "WITH (FORCE)") {
		t.Errorf("mysql calls:\n%s", docker)
	}
}
