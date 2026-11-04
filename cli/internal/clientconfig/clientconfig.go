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

// Package clientconfig holds how workflow-cli reaches an engine: named profiles in a TOML file, environment
// variables and flags, in that order of precedence, as the Temporal CLI resolves its own.
package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

// DefaultProfile is used when no profile is named.
const DefaultProfile = "default"

// Settings is the connection to one engine.
type Settings struct {
	Address    string
	Namespace  string
	APIKey     string
	APIKeyFile string
	TLS        TLS
}

// TLS is the client side of the engine's TLS. It is on when Enabled is set, or when any file or name is given.
type TLS struct {
	Enabled    *bool
	CAPath     string
	CertPath   string
	KeyPath    string
	ServerName string
}

// On reports whether the connection uses TLS.
func (t TLS) On() bool {
	if t.Enabled != nil {
		return *t.Enabled
	}
	return t.CAPath != "" || t.CertPath != "" || t.KeyPath != "" || t.ServerName != ""
}

// Defaults reach a development engine on this machine.
func Defaults() Settings {
	return Settings{Address: "localhost:7233", Namespace: "default"}
}

// property is one key of a profile, with how it reads and writes Settings.
type property struct {
	name string
	get  func(*Settings) string
	set  func(*Settings, string) error
}

func text(field func(*Settings) *string) (func(*Settings) string, func(*Settings, string) error) {
	return func(s *Settings) string { return *field(s) },
		func(s *Settings, v string) error { *field(s) = v; return nil }
}

var properties = func() []property {
	p := func(name string, field func(*Settings) *string) property {
		get, set := text(field)
		return property{name, get, set}
	}
	return []property{
		p("address", func(s *Settings) *string { return &s.Address }),
		p("namespace", func(s *Settings) *string { return &s.Namespace }),
		p("apiKey", func(s *Settings) *string { return &s.APIKey }),
		p("apiKeyFile", func(s *Settings) *string { return &s.APIKeyFile }),
		{"tls.enabled", func(s *Settings) string {
			if s.TLS.Enabled == nil {
				return ""
			}
			return strconv.FormatBool(*s.TLS.Enabled)
		}, func(s *Settings, v string) error {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("tls.enabled %q must be true or false", v)
			}
			s.TLS.Enabled = &b
			return nil
		}},
		p("tls.caPath", func(s *Settings) *string { return &s.TLS.CAPath }),
		p("tls.certPath", func(s *Settings) *string { return &s.TLS.CertPath }),
		p("tls.keyPath", func(s *Settings) *string { return &s.TLS.KeyPath }),
		p("tls.serverName", func(s *Settings) *string { return &s.TLS.ServerName }),
	}
}()

// Properties are the keys a profile can hold, in the order they are shown.
func Properties() []string {
	names := make([]string, len(properties))
	for i, p := range properties {
		names[i] = p.name
	}
	return names
}

func lookup(name string) (property, error) {
	for _, p := range properties {
		if p.name == name {
			return p, nil
		}
	}
	return property{}, fmt.Errorf("unknown property %q; one of %s", name, strings.Join(Properties(), ", "))
}

// Set sets one property on s.
func (s *Settings) Set(name, value string) error {
	p, err := lookup(name)
	if err != nil {
		return err
	}
	return p.set(s, value)
}

// Get returns one property of s, empty when unset.
func (s *Settings) Get(name string) (string, error) {
	p, err := lookup(name)
	if err != nil {
		return "", err
	}
	return p.get(s), nil
}

// DefaultPath is <user config directory>/wso2-workflow/cli.toml.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("unable to find the user configuration directory: %w", err)
	}
	return filepath.Join(dir, "wso2-workflow", "cli.toml"), nil
}

// File is the profile file. A missing file has no profiles.
type File struct {
	path string
	k    *koanf.Koanf
}

// Open reads the profile file at path.
func Open(path string) (*File, error) {
	f := &File{path: path, k: koanf.New(".")}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own configuration file
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	values, err := toml.Parser().Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	if err := f.k.Load(confmap.Provider(values, "."), nil); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	for _, key := range f.k.Keys() {
		parts := strings.SplitN(key, ".", 3)
		if len(parts) < 3 || parts[0] != "profile" {
			return nil, fmt.Errorf("invalid %s: %q is not under [profile.<name>]", path, key)
		}
		if _, err := lookup(parts[2]); err != nil {
			return nil, fmt.Errorf("invalid %s: profile %q: %w", path, parts[1], err)
		}
	}
	return f, nil
}

// Profiles lists the profile names, sorted.
func (f *File) Profiles() []string {
	names := f.k.MapKeys("profile")
	sort.Strings(names)
	return names
}

// Has reports whether the profile exists.
func (f *File) Has(profile string) bool {
	return slices.Contains(f.Profiles(), profile)
}

// Apply sets on s every property the profile holds.
func (f *File) Apply(profile string, s *Settings) error {
	for _, p := range properties {
		key := "profile." + profile + "." + p.name
		if !f.k.Exists(key) {
			continue
		}
		if err := p.set(s, f.k.String(key)); err != nil {
			return fmt.Errorf("profile %q: %w", profile, err)
		}
	}
	return nil
}

// Get returns one property of a profile, empty when unset.
func (f *File) Get(profile, name string) (string, error) {
	if _, err := lookup(name); err != nil {
		return "", err
	}
	return f.k.String("profile." + profile + "." + name), nil
}

// Set stores one property of a profile and writes the file.
func (f *File) Set(profile, name, value string) error {
	p, err := lookup(name)
	if err != nil {
		return err
	}
	if err := p.set(&Settings{}, value); err != nil {
		return err
	}
	var v any = value
	if name == "tls.enabled" {
		v, _ = strconv.ParseBool(value)
	}
	if err := f.k.Set("profile."+profile+"."+name, v); err != nil {
		return err
	}
	return f.save()
}

// Delete removes one property of a profile, or the whole profile when name is empty, and writes the file.
func (f *File) Delete(profile, name string) error {
	if !f.Has(profile) {
		return fmt.Errorf("no profile %q in %s", profile, f.path)
	}
	key := "profile." + profile
	if name != "" {
		if _, err := lookup(name); err != nil {
			return err
		}
		key += "." + name
	}
	f.k.Delete(key)
	return f.save()
}

// save writes the file readable by its owner only, since a profile can hold an API key.
func (f *File) save() error {
	data, err := toml.Parser().Marshal(f.k.Raw())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// Environment variables, one per property; WORKFLOW_PROFILE and WORKFLOW_CONFIG_FILE choose the profile and file.
var envVars = map[string]string{ //nolint:gosec // variable names, not credentials
	"address":        "WORKFLOW_ADDRESS",
	"namespace":      "WORKFLOW_NAMESPACE",
	"apiKey":         "WORKFLOW_API_KEY",
	"apiKeyFile":     "WORKFLOW_API_KEY_FILE",
	"tls.enabled":    "WORKFLOW_TLS",
	"tls.caPath":     "WORKFLOW_TLS_CA_PATH",
	"tls.certPath":   "WORKFLOW_TLS_CERT_PATH",
	"tls.keyPath":    "WORKFLOW_TLS_KEY_PATH",
	"tls.serverName": "WORKFLOW_TLS_SERVER_NAME",
}

// EnvVar names the environment variable of a property.
func EnvVar(property string) string { return envVars[property] }

// ApplyEnv sets on s every property whose environment variable is set and not empty.
func ApplyEnv(s *Settings, lookupEnv func(string) (string, bool)) error {
	for _, p := range properties {
		if v, ok := lookupEnv(envVars[p.name]); ok && v != "" {
			if err := p.set(s, v); err != nil {
				return fmt.Errorf("%s: %w", envVars[p.name], err)
			}
		}
	}
	return nil
}
