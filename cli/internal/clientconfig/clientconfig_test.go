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

package clientconfig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cli.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyProfileThenEnvironment(t *testing.T) {
	f, err := Open(write(t, `
[profile.prod]
address = "engine.example.com:7233"
namespace = "orders"
apiKeyFile = "/run/secrets/token"

[profile.prod.tls]
caPath = "ca.pem"
serverName = "engine.example.com"
`))
	if err != nil {
		t.Fatal(err)
	}
	s := Defaults()
	if err := f.Apply("prod", &s); err != nil {
		t.Fatal(err)
	}
	if s.Address != "engine.example.com:7233" || s.Namespace != "orders" || s.APIKeyFile != "/run/secrets/token" ||
		s.TLS.CAPath != "ca.pem" || !s.TLS.On() {
		t.Fatalf("after the profile: %+v", s)
	}
	env := map[string]string{"WORKFLOW_NAMESPACE": "billing", "WORKFLOW_TLS": "false", "WORKFLOW_ADDRESS": ""}
	if err := ApplyEnv(&s, func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err != nil {
		t.Fatal(err)
	}
	if s.Namespace != "billing" || s.TLS.On() || s.Address != "engine.example.com:7233" {
		t.Fatalf("after the environment: %+v", s)
	}
	env["WORKFLOW_TLS"] = "maybe"
	if err := ApplyEnv(&s, func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err == nil ||
		!strings.Contains(err.Error(), "WORKFLOW_TLS") {
		t.Fatalf("a bad WORKFLOW_TLS = %v", err)
	}
}

func TestTLSIsOnWithAnyTLSSetting(t *testing.T) {
	off := false
	for name, tt := range map[string]struct {
		tls  TLS
		want bool
	}{
		"nothing":           {TLS{}, false},
		"a CA":              {TLS{CAPath: "ca.pem"}, true},
		"a server name":     {TLS{ServerName: "e"}, true},
		"a client cert":     {TLS{CertPath: "c", KeyPath: "k"}, true},
		"explicitly off":    {TLS{CAPath: "ca.pem", Enabled: &off}, false},
		"enabled, no files": {TLS{Enabled: boolPtr(true)}, true},
	} {
		if got := tt.tls.On(); got != tt.want {
			t.Errorf("%s: On = %v, want %v", name, got, tt.want)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

func TestSetGetDeleteWriteTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "cli.toml")
	f, err := Open(path)
	if err != nil || len(f.Profiles()) != 0 {
		t.Fatalf("a missing file = %v, %v", f.Profiles(), err)
	}
	for _, kv := range [][2]string{{"address", "e:7233"}, {"tls.enabled", "true"}, {"tls.caPath", "ca.pem"}} {
		if err := f.Set("prod", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := reopened.Get("prod", "tls.enabled"); v != "true" {
		t.Fatalf("tls.enabled = %q after a round trip", v)
	}
	s := Defaults()
	if err := reopened.Apply("prod", &s); err != nil || !s.TLS.On() || s.Address != "e:7233" {
		t.Fatalf("Apply = %+v, %v", s, err)
	}
	if err := reopened.Delete("prod", "tls.caPath"); err != nil {
		t.Fatal(err)
	}
	if v, _ := reopened.Get("prod", "tls.caPath"); v != "" {
		t.Fatalf("tls.caPath = %q after delete", v)
	}
	if err := reopened.Delete("prod", "colour"); err == nil {
		t.Fatal("deleting an unknown property worked")
	}
	if _, err := reopened.Get("prod", "colour"); err == nil {
		t.Fatal("getting an unknown property worked")
	}
	if err := reopened.Set("prod", "tls.enabled", "sometimes"); err == nil {
		t.Fatal("a bad boolean was stored")
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, %v", info, err)
	}
}

func TestInvalidProfileNamesLeaveTheFileReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.toml")
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("prod-eu_1", "address", "e:7233"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prod.eu", "", "a b", `q"uote`, "a]b"} {
		if err := f.Set(name, "address", "x:7233"); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
			t.Errorf("Set(%q) = %v, want the name refused", name, err)
		}
		if err := f.Delete(name, ""); err == nil {
			t.Errorf("Delete(%q) worked", name)
		}
		if _, err := f.Get(name, "address"); err == nil {
			t.Errorf("Get(%q) worked", name)
		}
		if err := f.Apply(name, &Settings{}); err == nil {
			t.Errorf("Apply(%q) worked", name)
		}
	}
	reopened, err := Open(path)
	if err != nil || !slices.Equal(reopened.Profiles(), []string{"prod-eu_1"}) {
		t.Fatalf("after refused names: profiles = %v, %v", reopened.Profiles(), err)
	}
}

func TestSettingsGetAndSet(t *testing.T) {
	s := Defaults()
	if v, _ := s.Get("tls.enabled"); v != "" {
		t.Fatalf("unset tls.enabled = %q", v)
	}
	for _, p := range Properties() {
		value := "v"
		if p == "tls.enabled" {
			value = "true"
		}
		if err := s.Set(p, value); err != nil {
			t.Fatalf("Set(%s): %v", p, err)
		}
		if got, _ := s.Get(p); got != value {
			t.Errorf("Get(%s) = %q", p, got)
		}
		if EnvVar(p) == "" {
			t.Errorf("%s has no environment variable", p)
		}
	}
	if err := s.Set("colour", "red"); err == nil {
		t.Fatal("Set accepted an unknown property")
	}
	if _, err := s.Get("colour"); err == nil {
		t.Fatal("Get accepted an unknown property")
	}
}

func TestInvalidFiles(t *testing.T) {
	for content, want := range map[string]string{
		"address = \"x\"\n":                  "not under [profile.<name>]",
		"[profile.p]\ncolour = \"red\"\n":    `profile "p": unknown property "colour"`,
		"[profile.p\n":                       "invalid",
		"[profile.p.tls]\nenabled = \"x\"\n": "", // read as a string, refused when applied
	} {
		f, err := Open(write(t, content))
		if want == "" {
			if err != nil {
				t.Fatalf("%q: %v", content, err)
			}
			if err := f.Apply("p", &Settings{}); err == nil {
				t.Errorf("%q: Apply accepted a bad boolean", content)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", content, err, want)
		}
	}
	if err := os.Mkdir(filepath.Join(t.TempDir(), "d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("a directory opened as the profile file")
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil || !strings.HasSuffix(path, filepath.Join("wso2-workflow", "cli.toml")) {
		t.Fatalf("DefaultPath = %q, %v", path, err)
	}
}
