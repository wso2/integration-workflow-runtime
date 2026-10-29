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

package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/temporal"
)

const minimalConfig = "testdata/minimal.yaml"

type fakeServer struct {
	started  bool
	startErr error
}

func (f *fakeServer) Start() error {
	f.started = true
	return f.startErr
}

func (f *fakeServer) Stop() error { return nil }

func newTestCommand(server temporal.Server, factoryErr error) (command, *bytes.Buffer, *bytes.Buffer) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	c := command{
		version: "9.9.9-test",
		stdout:  stdout,
		stderr:  stderr,
		newServer: func(...temporal.ServerOption) (temporal.Server, error) {
			return server, factoryErr
		},
	}
	return c, stdout, stderr
}

func TestRunWithoutArgumentsPrintsUsage(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: workflow-engine") {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"serve"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: workflow-engine") {
		t.Fatalf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestVersionPrintsProductAndTemporalVersions(t *testing.T) {
	c, stdout, _ := newTestCommand(nil, nil)
	if code := c.run([]string{"version"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "workflow-engine 9.9.9-test (Temporal server ") {
		t.Fatalf("stdout = %q, want the product and Temporal versions", out)
	}
}

func TestServiceListCollectsRepeatedFlags(t *testing.T) {
	var s serviceList
	for _, v := range []string{"frontend", "history"} {
		if err := s.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if got := s.String(); got != "frontend,history" {
		t.Fatalf("String() = %q, want %q", got, "frontend,history")
	}
}

func TestStartRequiresConfigFile(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"start"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--config-file is required") {
		t.Fatalf("stderr = %q, want the missing --config-file error", stderr.String())
	}
}

func TestStartHelpIsNotAnError(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"start", "-h"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "-config-file") {
		t.Fatalf("stderr = %q, want the start flags", stderr.String())
	}
}

func TestStartRejectsUnknownFlag(t *testing.T) {
	c, _, _ := newTestCommand(nil, nil)
	if err := c.start([]string{"--no-such-flag"}); err == nil {
		t.Fatal("start accepted an unknown flag")
	}
}

func TestStartRejectsMissingConfigFile(t *testing.T) {
	c, _, _ := newTestCommand(nil, nil)
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	err := c.start([]string{"--config-file", missing})
	if err == nil || !strings.Contains(err.Error(), "unable to load configuration") {
		t.Fatalf("start with %s = %v, want a configuration load error", missing, err)
	}
}

func TestStartRefusesNoAuthorizer(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	err := c.start([]string{"--config-file", minimalConfig})
	if err == nil || !strings.Contains(err.Error(), "no authorizer configured") {
		t.Fatalf("start without an authorizer = %v, want the no-authorizer refusal", err)
	}
	if server.started {
		t.Fatal("the server started without an authorizer")
	}
}

func TestStartWithAllowNoAuthStartsServer(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--config-file", minimalConfig, "--allow-no-auth", "--service", "frontend"}); err != nil {
		t.Fatalf("start = %v, want nil", err)
	}
	if !server.started {
		t.Fatal("the server was not started")
	}
}

func TestStartReportsServerCreationError(t *testing.T) {
	c, _, _ := newTestCommand(nil, errors.New("boom"))
	err := c.start([]string{"--config-file", minimalConfig, "--allow-no-auth"})
	if err == nil || !strings.Contains(err.Error(), "unable to create the server: boom") {
		t.Fatalf("start = %v, want the server creation error", err)
	}
}

func TestStartReturnsServerStartError(t *testing.T) {
	server := &fakeServer{startErr: errors.New("port in use")}
	c, _, _ := newTestCommand(server, nil)
	if code := c.run([]string{"start", "--config-file", minimalConfig, "--allow-no-auth"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestMainReturnsUsageExitCode(t *testing.T) {
	if code := Main("9.9.9-test", nil); code != 2 {
		t.Fatalf("Main(nil) = %d, want 2", code)
	}
}

func TestStartRefusesAuthorizerWithoutClaimMapper(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	err := c.start([]string{"--config-file", "testdata/authorizer-only.yaml"})
	if err == nil || !strings.Contains(err.Error(), "no claim mapper configured") {
		t.Fatalf("start with an authorizer and no claim mapper = %v, want the claim mapper refusal", err)
	}
	if server.started {
		t.Fatal("the server started with an authorizer that admits every caller")
	}
}

func TestStartAcceptsAuthorizerWithClaimMapper(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--config-file", "testdata/authorizer-and-claim-mapper.yaml"}); err != nil {
		t.Fatalf("start with an authorizer and a claim mapper = %v, want nil", err)
	}
	if !server.started {
		t.Fatal("the server was not started")
	}
}

func TestStartAllowsMissingClaimMapperWhenNoAuthIsAllowed(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--config-file", "testdata/authorizer-only.yaml", "--allow-no-auth"}); err != nil {
		t.Fatalf("start with --allow-no-auth = %v, want nil", err)
	}
}

func TestStartRefusesAuthorizerWithoutInternalFrontend(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	err := c.start([]string{"--config-file", "testdata/authorizer-without-internal-frontend.yaml"})
	if err == nil || !strings.Contains(err.Error(), "an authorizer needs the internal frontend") {
		t.Fatalf("start with an authorizer and no internal frontend = %v, want the internal frontend refusal", err)
	}
	if server.started {
		t.Fatal("the server started without the internal frontend its authorizer needs")
	}
}

func TestDefaultServicesAddTheInternalFrontendWhenConfigured(t *testing.T) {
	plain := &config.Config{Services: map[string]config.Service{"frontend": {}}}
	if got := defaultServices(plain); !slices.Equal(got, temporal.DefaultServices) {
		t.Fatalf("defaultServices without an internal frontend = %v, want %v", got, temporal.DefaultServices)
	}
	withInternal := &config.Config{Services: map[string]config.Service{"frontend": {}, "internal-frontend": {}}}
	if got := defaultServices(withInternal); !slices.Contains(got, "internal-frontend") || len(got) != len(temporal.DefaultServices)+1 {
		t.Fatalf("defaultServices with an internal frontend = %v, want the defaults plus internal-frontend", got)
	}
}

func TestStartFromRuntimeConfig(t *testing.T) {
	server := &fakeServer{}
	c, _, _ := newTestCommand(server, nil)
	if err := c.start([]string{"--runtime-config", "testdata/runtime-sqlite.toml", "--allow-no-auth"}); err != nil {
		t.Fatalf("start = %v, want nil", err)
	}
	if !server.started {
		t.Fatal("the server was not started")
	}
}

func TestStartFromRuntimeConfigKeepsTheAuthorizerCheck(t *testing.T) {
	c, _, _ := newTestCommand(&fakeServer{}, nil)
	err := c.start([]string{"--runtime-config", "testdata/runtime-sqlite.toml"})
	if err == nil || !strings.Contains(err.Error(), "no authorizer configured") {
		t.Fatalf("start = %v, want the no-authorizer refusal", err)
	}
}

func TestStartNeedsExactlyOneConfiguration(t *testing.T) {
	c, _, _ := newTestCommand(&fakeServer{}, nil)
	both := c.start([]string{"--runtime-config", "testdata/runtime-sqlite.toml", "--config-file", minimalConfig})
	if both == nil || !strings.Contains(both.Error(), "not both") {
		t.Fatalf("start with both = %v, want a conflict error", both)
	}
	neither := c.start(nil)
	if neither == nil || !strings.Contains(neither.Error(), "--runtime-config or --config-file is required") {
		t.Fatalf("start with neither = %v, want the required error", neither)
	}
}

func TestRenderConfigHidesThePassword(t *testing.T) {
	c, stdout, _ := newTestCommand(nil, nil)
	if code := c.run([]string{"render-config", "--runtime-config", "testdata/runtime-postgresql.toml"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := stdout.String()
	if strings.Contains(out, "do-not-print") || !strings.Contains(out, "pluginName: postgres12") {
		t.Fatalf("render-config output leaks the password or lacks the plugin:\n%s", out)
	}
}

func TestRenderConfigErrors(t *testing.T) {
	c, _, stderr := newTestCommand(nil, nil)
	if code := c.run([]string{"render-config"}); code != 1 || !strings.Contains(stderr.String(), "--runtime-config is required") {
		t.Fatalf("render-config without a file: code %d, stderr %q", code, stderr.String())
	}
	if err := c.renderConfig([]string{"--runtime-config", filepath.Join(t.TempDir(), "absent.toml")}); err == nil {
		t.Fatal("render-config accepted a missing file")
	}
	if err := c.renderConfig([]string{"--no-such-flag"}); err == nil {
		t.Fatal("render-config accepted an unknown flag")
	}
}
