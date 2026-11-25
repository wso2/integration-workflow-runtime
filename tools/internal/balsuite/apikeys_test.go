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
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// keyEngine prints a key for each api-key create, named after the --name it was given.
const keyEngine = "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = --name ] && echo \"key-$2\"; shift; done\nexit 0\n"

func TestPrepareAPIKeyEngine(t *testing.T) {
	dir := t.TempDir()
	engine := script(t, dir, "engine", keyEngine)
	base := filepath.Join(dir, "runtime-sqlite.toml")
	if err := os.WriteFile(base, []byte("[database]\ntype = \"sqlite\"\ninMemory = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := PrepareAPIKeyEngine(engine, base, dir)
	if err != nil {
		t.Fatal(err)
	}
	if e.AdminKey != "key-suite-admin" || e.IntegrationKey != "key-suite-integration" {
		t.Fatalf("keys = %q, %q", e.AdminKey, e.IntegrationKey)
	}
	config, _ := os.ReadFile(e.Config)
	for _, want := range []string{"inMemory = true", "[engine.tls]", "type = \"apiKeys\"", "signingKey = \""} {
		if !strings.Contains(string(config), want) {
			t.Errorf("runtime.toml lacks %q:\n%s", want, config)
		}
	}
	// The engine certificate must verify for the suite's address against the CA the runner hands out.
	caPEM, _ := os.ReadFile(e.CAFile)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	certPEM, _ := os.ReadFile(filepath.Join(dir, "engine.crt"))
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost"}); err != nil {
		t.Fatalf("the engine certificate does not verify for localhost: %v", err)
	}
	if !slices.Contains(e.BalEnv(), "BAL_CONFIG_VAR_BALLERINA_WORKFLOW_AUTHAPIKEY=key-suite-integration") ||
		!slices.Contains(e.BalEnv(), "BAL_CONFIG_VAR_BALLERINA_WORKFLOW_MODE=SELF_HOSTED") ||
		!slices.Equal(e.CLIEnv(), []string{"TEMPORAL_API_KEY=key-suite-admin"}) ||
		!slices.Equal(e.CLIFlags(), []string{"--tls-ca-path", e.CAFile}) {
		t.Fatalf("settings = %v %v %v", e.CLIFlags(), e.CLIEnv(), e.BalEnv())
	}
}

func TestPrepareAPIKeyEngineReportsFailures(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "runtime-sqlite.toml")
	if _, err := PrepareAPIKeyEngine(script(t, dir, "engine", keyEngine), base, dir); err == nil {
		t.Error("a missing base configuration was accepted")
	}
	_ = os.WriteFile(base, nil, 0o600)
	if _, err := PrepareAPIKeyEngine(script(t, dir, "failing", "#!/bin/sh\necho nope >&2\nexit 1\n"), base, dir); err == nil ||
		!strings.Contains(err.Error(), "nope") {
		t.Errorf("a failing api-key create = %v", err)
	}
	if _, err := PrepareAPIKeyEngine(script(t, dir, "silent", "#!/bin/sh\nexit 0\n"), base, dir); err == nil ||
		!strings.Contains(err.Error(), "printed no key") {
		t.Errorf("an api-key create without output = %v", err)
	}
}

// Run hands the CLI flags and environment to every Temporal CLI call and the bal environment to bal test.
func TestRunPassesSecuredSettings(t *testing.T) {
	temporal := "#!/bin/sh\necho \"$TEMPORAL_API_KEY $*\" >> \"$(dirname \"$0\")/temporal-calls\"\n" +
		strings.TrimPrefix(servingTemporal, "#!/bin/sh\n")
	bal := "#!/bin/sh\necho \"$BAL_CONFIG_VAR_BALLERINA_WORKFLOW_AUTHAPIKEY\" > \"$(dirname \"$0\")/bal-env\"\nexit 0\n"
	o := options(t, temporal, idleEngine, bal)
	o.CLIFlags, o.CLIEnv = []string{"--tls-ca-path", "ca.pem"}, []string{"TEMPORAL_API_KEY=admin"}
	o.BalEnv = []string{"BAL_CONFIG_VAR_BALLERINA_WORKFLOW_AUTHAPIKEY=integration"}
	var stderr bytes.Buffer
	if code := Run(context.Background(), o, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("Run = %d; stderr: %s", code, stderr.String())
	}
	calls, _ := os.ReadFile(filepath.Join(o.Root, "temporal-calls"))
	for line := range strings.Lines(string(calls)) {
		if !strings.HasPrefix(line, "admin ") || !strings.Contains(line, "--tls-ca-path ca.pem") {
			t.Errorf("a Temporal CLI call without the secured settings: %q", line)
		}
	}
	if env, _ := os.ReadFile(filepath.Join(o.Root, "bal-env")); strings.TrimSpace(string(env)) != "integration" {
		t.Errorf("bal test saw the integration key as %q", env)
	}
}

func TestTOMLLiteralKeepsAnyPath(t *testing.T) {
	for in, want := range map[string]string{`/tmp/o'neil/key.pem`: `"/tmp/o'neil/key.pem"`,
		`C:\keys\a "b".pem`: `"C:\\keys\\a \"b\".pem"`} {
		if got := tomlLiteral(in); got != want {
			t.Errorf("tomlLiteral(%q) = %s, want %s", in, got, want)
		}
	}
}
