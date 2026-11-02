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
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the expected output of the testdata/config fixtures")

// fixture is a txtar archive: a runtime.toml section, an optional env section (NAME=value lines), and the expected
// rendered Temporal configuration (temporal.yaml) or the expected error (error).
type fixture struct {
	path     string
	sections map[string]string
	order    []string
}

func readFixture(t *testing.T, path string) fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{path: path, sections: map[string]string{}}
	var name string
	var body strings.Builder
	flush := func() {
		if name != "" {
			f.sections[name] = body.String()
			f.order = append(f.order, name)
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if strings.HasPrefix(line, "-- ") && strings.HasSuffix(strings.TrimRight(line, "\n"), " --") {
			flush()
			name = strings.TrimSuffix(strings.TrimPrefix(strings.TrimRight(line, "\n"), "-- "), " --")
			continue
		}
		if name != "" {
			body.WriteString(line)
		}
	}
	flush()
	return f
}

func (f fixture) write(t *testing.T, section, content string) {
	t.Helper()
	if _, ok := f.sections[section]; !ok {
		f.order = append(f.order, section)
	}
	f.sections[section] = content
	var out bytes.Buffer
	for _, name := range f.order {
		out.WriteString("-- " + name + " --\n" + f.sections[name])
	}
	if err := os.WriteFile(f.path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) env() func(string) (string, bool) {
	vars := map[string]string{}
	for _, line := range strings.Split(f.sections["env"], "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[k] = v
		}
	}
	return func(name string) (string, bool) { v, ok := vars[name]; return v, ok }
}

// TestConfigFixtures compares the whole rendered configuration, or the exact error, for every fixture.
func TestConfigFixtures(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join("testdata", "config", "*.txtar"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".txtar"), func(t *testing.T) {
			t.Parallel()
			f := readFixture(t, path)
			toml, ok := f.sections["runtime.toml"]
			if !ok {
				t.Fatal("the fixture has no runtime.toml section")
			}
			rt, err := parse([]byte(toml), f.env())
			_, wantsError := f.sections["error"]
			switch {
			case wantsError:
				if err == nil {
					t.Fatal("the configuration parsed, but the fixture expects an error")
				}
				got := err.Error() + "\n"
				if *update {
					f.write(t, "error", got)
					return
				}
				if got != f.sections["error"] {
					t.Fatalf("error = %q\nwant  %q", got, f.sections["error"])
				}
			case err != nil:
				t.Fatalf("parse: %v", err)
			default:
				rendered, err := Render(rt.Temporal)
				if err != nil {
					t.Fatal(err)
				}
				if *update {
					f.write(t, "temporal.yaml", string(rendered))
					return
				}
				if string(rendered) != f.sections["temporal.yaml"] {
					t.Fatalf("rendered configuration differs from temporal.yaml; rerun with -update and review the diff:\n%s", rendered)
				}
			}
		})
	}
}
