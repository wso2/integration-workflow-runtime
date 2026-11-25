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

package vulnallow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stream is govulncheck JSON output: one called finding, one imported-only finding and other messages.
const stream = `{"config":{"protocol_version":"v1.0.0"}}
{"osv":{"id":"GO-2026-6974"}}
{"finding":{"osv":"GO-2026-6974","fixed_version":"v1.47.0","trace":[{"module":"go.opentelemetry.io/otel/sdk","version":"v1.45.0","package":"go.opentelemetry.io/otel/sdk/trace","function":"Start"}]}}
{"finding":{"osv":"GO-2026-1111","fixed_version":"v2.0.0","trace":[{"module":"example.com/unused","version":"v1.0.0","package":"example.com/unused"}]}}
`

const called = `{"finding":{"osv":"GO-2026-2222","fixed_version":"v1.1.0","trace":[{"module":"example.com/used","version":"v1.0.0","function":"Do"}]}}
`

func TestCheck(t *testing.T) {
	for name, c := range map[string]struct {
		input   string
		allowed map[string]string
		code    int
		out     string
	}{
		"allowed called finding":     {stream, map[string]string{"GO-2026-6974": "no compatible fix"}, 0, "allowed GO-2026-6974"},
		"called finding not allowed": {stream, nil, 1, "GO-2026-6974 in go.opentelemetry.io/otel/sdk@v1.45.0"},
		"another called finding":     {stream + called, map[string]string{"GO-2026-6974": "x"}, 1, "GO-2026-2222"},
		"imports only":               {`{"finding":{"osv":"GO-1","trace":[{"module":"m"}]}}`, nil, 0, "No vulnerabilities called"},
		"unused exception":           {"", map[string]string{"GO-2026-6974": "x"}, 0, "exception GO-2026-6974 is not reported in this module"},
		"broken output":              {"{not json", nil, 1, "unable to read govulncheck's output"},
	} {
		var stdout, stderr bytes.Buffer
		code := Check(strings.NewReader(c.input), c.allowed, &stdout, &stderr)
		if code != c.code || !strings.Contains(stdout.String()+stderr.String(), c.out) {
			t.Errorf("%s: code %d, output %q; want %d and %q", name, code, stdout.String()+stderr.String(), c.code, c.out)
		}
	}
}

func TestReadExceptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exceptions.txt")
	_ = os.WriteFile(path, []byte("# comment\n\nGO-2026-6974 no fix that builds\n"), 0o600)
	allowed, err := ReadExceptions(path)
	if err != nil || allowed["GO-2026-6974"] != "no fix that builds" || len(allowed) != 1 {
		t.Fatalf("exceptions = %v, %v", allowed, err)
	}
	_ = os.WriteFile(path, []byte("GO-2026-6974\n"), 0o600)
	if _, err := ReadExceptions(path); err == nil || !strings.Contains(err.Error(), "has no reason") {
		t.Errorf("an exception without a reason = %v", err)
	}
	if _, err := ReadExceptions(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file was read")
	}
	// The repository's own file must stay readable.
	if _, err := ReadExceptions("../../../third-party/vulnerability-exceptions.txt"); err != nil {
		t.Errorf("third-party/vulnerability-exceptions.txt: %v", err)
	}
}
