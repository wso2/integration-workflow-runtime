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

// Package vulnallow decides a govulncheck run against a list of accepted advisories, so one advisory without a usable
// fix does not hide every other one.
package vulnallow

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// ReadExceptions reads "<advisory ID> <reason>" lines; "#" starts a comment, and an ID without a reason is refused.
func ReadExceptions(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the repository's exceptions file
	if err != nil {
		return nil, err
	}
	allowed := map[string]string{}
	for n, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, reason, _ := strings.Cut(line, " ")
		if reason = strings.TrimSpace(reason); reason == "" {
			return nil, fmt.Errorf("%s line %d: %s has no reason", path, n+1, id)
		}
		allowed[id] = reason
	}
	return allowed, nil
}

// message is the part of a govulncheck JSON message read here.
type message struct {
	Config  *json.RawMessage `json:"config"`
	Finding *struct {
		OSV          string `json:"osv"`
		FixedVersion string `json:"fixed_version"`
		Trace        []struct {
			Module   string `json:"module"`
			Version  string `json:"version"`
			Function string `json:"function"`
		} `json:"trace"`
	} `json:"finding"`
}

// Check reads govulncheck's JSON stream, one or more runs in a row, and returns 0 when every vulnerability the code
// calls is allowed. Findings at module or package level, which the code does not call, do not fail the run, as in
// govulncheck's own text mode. A stream without a run's config, such as empty output, fails.
func Check(in io.Reader, allowed map[string]string, stdout, stderr io.Writer) int {
	called := map[string]string{}
	scans := 0
	dec := json.NewDecoder(bufio.NewReader(in))
	for {
		var m message
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			_, _ = fmt.Fprintf(stderr, "unable to read govulncheck's output: %v\n", err)
			return 1
		}
		if m.Config != nil {
			scans++
		}
		f := m.Finding
		if f == nil || len(f.Trace) == 0 || f.Trace[0].Function == "" {
			continue
		}
		called[f.OSV] = fmt.Sprintf("%s@%s (fixed in %s)", f.Trace[0].Module, f.Trace[0].Version, f.FixedVersion)
	}
	// Every govulncheck run starts with its config; without one, the scan did not run, so it must not pass.
	if scans == 0 {
		_, _ = fmt.Fprintln(stderr, "govulncheck produced no results; the scan did not run")
		return 1
	}
	code := 0
	for _, id := range sortedKeys(called) {
		if reason, ok := allowed[id]; ok {
			_, _ = fmt.Fprintf(stdout, "allowed %s in %s: %s\n", id, called[id], reason)
			continue
		}
		_, _ = fmt.Fprintf(stderr, "vulnerability %s in %s is called by this code; see https://pkg.go.dev/vuln/%s\n",
			id, called[id], id)
		code = 1
	}
	for _, id := range sortedKeys(allowed) {
		if _, ok := called[id]; !ok {
			_, _ = fmt.Fprintf(stdout, "exception %s is not reported by any module; remove it from the exceptions file\n", id)
		}
	}
	if code == 0 {
		_, _ = fmt.Fprintln(stdout, "No vulnerabilities called, other than the allowed ones.")
	}
	return code
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
