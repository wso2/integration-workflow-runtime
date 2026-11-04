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

package enginetest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestSecuredEngine runs the engine with TLS, internode mutual TLS and tokens from an OpenID Connect provider, and
// checks who may do what: callers through the frontend with tokens, and callers of the internal frontend.
func TestSecuredEngine(t *testing.T) {
	binary := engineBinary(t)
	dir := t.TempDir()
	frontend := newPKI(t, dir, "frontend-ca")
	engineCert, engineKey := frontend.issue("engine")
	internode := newPKI(t, dir, "internode-ca")
	nodeCert, nodeKey := internode.issue("node")
	idp := newIdentityProvider(t)
	base := freeBase(t)
	config := writeConfig(t, dir, ports(base)+fmt.Sprintf(`
[engine.tls]
certFile = %s
keyFile = %s

[engine.tls.internode]
enabled = true
certFile = %s
keyFile = %s
caFile = %s

[engine.auth]
type = "jwt"
issuer = %s
rolesClaim = "groups"

[[engine.auth.rules]]
value = "workflow-admins"
namespace = "*"
role = "admin"

[[engine.auth.rules]]
value = "orders-app"
namespace = "orders"
role = "write"

[[engine.auth.rules]]
value = "auditors"
namespace = "orders"
role = "read"

[database]
type = "sqlite"
inMemory = true
`, tomlString(engineCert), tomlString(engineKey), tomlString(nodeCert), tomlString(nodeKey),
		tomlString(internode.caFile("internode-ca")), tomlString(idp.issuer)))

	tls := []string{"--tls-ca-path", frontend.caFile("frontend-ca")}
	token := func(claims map[string]any) []string {
		return append(append([]string{}, tls...), "--api-key", idp.token(claims, nil))
	}
	admin := token(map[string]any{"sub": "ops", "groups": []any{"workflow-admins"}})
	e := start(t, binary, base, admin, "start", "--runtime-config", config)

	for _, ns := range []string{"orders", "billing"} {
		e.must("operator", "namespace", "create", "--namespace", ns, "--retention", "24h")
	}
	// The frontend adds a search attribute through the internal frontend, over internode TLS with the server name
	// taken from the internode certificate; without one, that call fails the TLS handshake.
	e.must("operator", "search-attribute", "create", "--namespace", "orders", "--name", "Customer", "--type", "Keyword")
	app := token(map[string]any{"sub": "orders-app", "groups": []any{"orders-app"}})
	auditor := token(map[string]any{"sub": "audit", "groups": []any{"auditors"}})
	startIn := func(ns string) []string {
		return []string{"workflow", "start", "--namespace", ns, "--type", "Order", "--task-queue", "orders",
			"--workflow-id", fmt.Sprintf("order-%d", time.Now().UnixNano())}
	}
	list := []string{"workflow", "list", "--namespace", "orders"}

	// An integration's token starts and lists workflows in its namespace.
	for _, args := range [][]string{startIn("orders"), list} {
		if out, err := e.temporalAt(e.address, app, args...); err != nil {
			t.Fatalf("the orders token was refused %v: %v\n%s\n%s", args, err, out, e.log)
		}
	}
	if out, err := e.temporalAt(e.address, auditor, list...); err != nil {
		t.Fatalf("the reader token could not list: %v\n%s", err, out)
	}

	other := rsaKey(t)
	refused := map[string]struct {
		flags []string
		args  []string
	}{
		"another namespace":  {app, startIn("billing")},
		"a reader writing":   {auditor, startIn("orders")},
		"no token":           {tls, list},
		"an expired token":   {token(map[string]any{"sub": "x", "groups": []any{"orders-app"}, "exp": time.Now().Add(-time.Hour).Unix()}), list},
		"a forged token":     {append(append([]string{}, tls...), "--api-key", idp.token(map[string]any{"sub": "x", "groups": []any{"workflow-admins"}}, other)), list},
		"another issuer":     {token(map[string]any{"sub": "x", "groups": []any{"workflow-admins"}, "iss": "https://elsewhere"}), list},
		"no matching group":  {token(map[string]any{"sub": "x", "groups": []any{"employees"}}), list},
		"a plaintext client": {[]string{"--tls=false", "--api-key", idp.token(map[string]any{"sub": "ops", "groups": []any{"workflow-admins"}}, nil)}, list},
	}
	for name, tt := range refused {
		want := []string{"Request unauthorized"}
		if name == "a plaintext client" {
			want = tlsRefusals
		}
		if out, err := e.temporalAt(e.address, tt.flags, tt.args...); err == nil || !containsAny(out, want) {
			t.Errorf("%s: %v, want a refusal with one of %q:\n%s", name, err, want, out)
		}
	}

	// The internal frontend grants system access without a token, so only internode certificates may reach it.
	internal := fmt.Sprintf("127.0.0.1:%d", base+3)
	internalCA := []string{"--tls-ca-path", internode.caFile("internode-ca")}
	for name, flags := range map[string][]string{
		"without a client certificate": internalCA,
		"in plain text":                {"--tls=false"},
		"with a frontend token only":   append(append([]string{}, internalCA...), admin[len(admin)-2:]...),
	} {
		if out, err := e.temporalAt(internal, flags, "operator", "namespace", "list"); err == nil ||
			!containsAny(out, tlsRefusals) {
			t.Errorf("the internal frontend did not refuse the connection of a caller %s: %v\n%s", name, err, out)
		}
	}
	withCert := append(append([]string{}, internalCA...), "--tls-cert-path", nodeCert, "--tls-key-path", nodeKey)
	if out, err := e.temporalAt(internal, withCert, "operator", "cluster", "health"); err != nil || !strings.Contains(out, "SERVING") {
		t.Errorf("the internal frontend refused a node certificate: %v\n%s", err, out)
	}
}

// tlsRefusals are how the Temporal CLI reports a refused TLS connection: before 1.9 as a connection error, from 1.9
// with the reason.
var tlsRefusals = []string{"connection error", "requires TLS", "requires client certificate"}

func containsAny(s string, subs []string) bool {
	return slices.ContainsFunc(subs, func(sub string) bool { return strings.Contains(s, sub) })
}
