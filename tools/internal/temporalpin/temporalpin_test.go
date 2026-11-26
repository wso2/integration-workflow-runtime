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
package temporalpin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEngineAndCLIPinTheSameTemporal(t *testing.T) {
	versions := map[string]string{}
	for _, module := range []string{"engine", "cli"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", module, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		if versions[module], err = Version(data); err != nil {
			t.Fatalf("%s: %v", module, err)
		}
	}
	if versions["engine"] != versions["cli"] {
		t.Errorf("engine/go.mod requires go.temporal.io/server %s but cli/go.mod %s; bump both together",
			versions["engine"], versions["cli"])
	}
}

func TestVersion(t *testing.T) {
	for in, want := range map[string]string{
		"require (\n\tgo.temporal.io/server v1.32.2\n)\n":     "v1.32.2",
		"require go.temporal.io/server v1.33.0 // indirect\n": "v1.33.0",
	} {
		if got, err := Version([]byte(in)); err != nil || got != want {
			t.Errorf("Version(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Version([]byte("require go.temporal.io/api v1.63.5\n")); err == nil {
		t.Error("a go.mod without the server reported a version")
	}
}
