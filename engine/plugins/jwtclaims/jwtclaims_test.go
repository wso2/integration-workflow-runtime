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

package jwtclaims

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.temporal.io/server/common/authorization"
)

type testKeys struct{ rsa *rsa.PublicKey }

func (k testKeys) RsaKey(string, string) (*rsa.PublicKey, error) { return k.rsa, nil }
func (testKeys) EcdsaKey(string, string) (*ecdsa.PublicKey, error) {
	return nil, errors.New("no ecdsa key")
}
func (testKeys) HmacKey(string, string) ([]byte, error) { return nil, errors.New("no hmac key") }
func (testKeys) SupportedMethods() []string             { return []string{"RS256"} }
func (testKeys) Close()                                 {}

var signingKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

func token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + s
}

func mapper(t *testing.T, s Settings) *Mapper {
	t.Helper()
	m, err := New(testKeys{rsa: &signingKey.PublicKey}, s)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func claimsFor(t *testing.T, m *Mapper, claims jwt.MapClaims) *authorization.Claims {
	t.Helper()
	c, err := m.GetClaims(&authorization.AuthInfo{AuthToken: token(t, claims)})
	if err != nil {
		t.Fatalf("GetClaims: %v", err)
	}
	return c
}

func TestTemporalPermissionFormat(t *testing.T) {
	c := claimsFor(t, mapper(t, Settings{}), jwt.MapClaims{
		"sub": "worker-1", "permissions": []any{"default:writer", "orders:read", "temporal-system:admin", "bad"},
	})
	if c.Subject != "worker-1" || c.AuthType != "jwt" {
		t.Fatalf("subject = %q, auth type = %q", c.Subject, c.AuthType)
	}
	if c.Namespaces["default"] != authorization.RoleWriter || c.Namespaces["orders"] != authorization.RoleReader {
		t.Fatalf("namespaces = %v", c.Namespaces)
	}
	if c.System != authorization.RoleAdmin {
		t.Fatalf("system = %v, want admin", c.System)
	}
}

func TestRulesOnANestedClaim(t *testing.T) {
	m := mapper(t, Settings{RolesClaim: "realm_access.roles", Rules: []Rule{
		{Value: "workflow-admins", Namespace: AllNamespaces, Role: "admin"},
		{Value: "claims-team", Namespace: "claims", Role: "writer"},
		{Value: "claims-team", Namespace: "claims", Role: "read"},
	}})
	c := claimsFor(t, m, jwt.MapClaims{"sub": "ann", "realm_access": map[string]any{
		"roles": []any{"claims-team", "unrelated", 7}}})
	if c.Namespaces["claims"] != authorization.RoleWriter|authorization.RoleReader || c.System != 0 {
		t.Fatalf("claims = %+v", c)
	}
	admin := claimsFor(t, m, jwt.MapClaims{"sub": "ops", "realm_access": map[string]any{"roles": []any{"workflow-admins"}}})
	if admin.System != authorization.RoleAdmin {
		t.Fatalf("system = %v, want admin", admin.System)
	}
}

func TestScopeStringAndDottedKey(t *testing.T) {
	m := mapper(t, Settings{RolesClaim: "scp", SubjectClaim: "client.id", Rules: []Rule{
		{Value: "workflow.read", Namespace: "default", Role: "read"},
		{Value: "workflow.write", Namespace: "default", Role: "write"},
	}})
	c := claimsFor(t, m, jwt.MapClaims{"client.id": "orders-app", "scp": "openid workflow.read,workflow.write"})
	if c.Subject != "orders-app" || c.Namespaces["default"] != authorization.RoleReader|authorization.RoleWriter {
		t.Fatalf("claims = %+v", c)
	}
}

func TestValueRegex(t *testing.T) {
	m := mapper(t, Settings{RolesClaim: "groups", ValueRegex: `^wf-(?P<namespace>\w+)-(?P<role>\w+)$`})
	c := claimsFor(t, m, jwt.MapClaims{"sub": "ann", "groups": []any{"wf-claims-admin", "other", "wf-hr-nope"}})
	if c.Namespaces["claims"] != authorization.RoleAdmin || len(c.Namespaces) != 1 {
		t.Fatalf("namespaces = %v", c.Namespaces)
	}
}

func TestIssuerAudienceAndExpiry(t *testing.T) {
	m := mapper(t, Settings{Issuer: "https://idp", Audience: "workflow-engine"})
	ok := jwt.MapClaims{"sub": "a", "iss": "https://idp", "aud": []any{"workflow-engine", "other"}}
	if _, err := m.GetClaims(&authorization.AuthInfo{AuthToken: token(t, ok)}); err != nil {
		t.Fatalf("valid token refused: %v", err)
	}
	for name, claims := range map[string]jwt.MapClaims{
		"wrong issuer":   {"sub": "a", "iss": "https://evil", "aud": "workflow-engine"},
		"wrong audience": {"sub": "a", "iss": "https://idp", "aud": "other"},
		"expired":        {"sub": "a", "iss": "https://idp", "aud": "workflow-engine", "exp": time.Now().Add(-time.Hour).Unix()},
		"no subject":     {"iss": "https://idp", "aud": "workflow-engine"},
	} {
		if _, err := m.GetClaims(&authorization.AuthInfo{AuthToken: token(t, claims)}); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}
}

func TestTokensThatAreRefused(t *testing.T) {
	m := mapper(t, Settings{})
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": "a", "exp": time.Now().Add(time.Hour).Unix()}).
		SignedString(other)
	if err != nil {
		t.Fatal(err)
	}
	noExpiry, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": "a"}).SignedString(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, header := range map[string]string{
		"basic":     "Basic dXNlcjpwYXNz",
		"no scheme": "abc",
		"forged":    "Bearer " + forged,
		"no expiry": "Bearer " + noExpiry,
		"garbage":   "Bearer not.a.token",
	} {
		if _, err := m.GetClaims(&authorization.AuthInfo{AuthToken: header}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNoTokenGetsNoRoles(t *testing.T) {
	c, err := mapper(t, Settings{}).GetClaims(&authorization.AuthInfo{})
	if err != nil || c.Subject != "" || c.System != 0 || len(c.Namespaces) != 0 {
		t.Fatalf("claims = %+v, err = %v", c, err)
	}
	if c, err := mapper(t, Settings{}).GetClaims(nil); err != nil || c == nil {
		t.Fatalf("nil auth info: %+v, %v", c, err)
	}
}

func TestInvalidSettings(t *testing.T) {
	for name, s := range map[string]Settings{
		"bad regex":       {ValueRegex: "("},
		"missing groups":  {ValueRegex: `^(\w+)$`},
		"regex and rules": {ValueRegex: `^(?P<namespace>\w+):(?P<role>\w+)$`, Rules: []Rule{{Value: "a", Namespace: "b", Role: "read"}}},
		"bad role":        {Rules: []Rule{{Value: "a", Namespace: "b", Role: "owner"}}},
		"empty rule":      {Rules: []Rule{{Role: "read"}}},
	} {
		if _, err := New(testKeys{}, s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUnsupportedKeyType(t *testing.T) {
	m := mapper(t, Settings{})
	if _, err := m.key(&jwt.Token{Method: jwt.SigningMethodNone, Header: map[string]any{}}); err == nil ||
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("key = %v", err)
	}
	if _, err := m.key(&jwt.Token{Method: jwt.SigningMethodES256, Header: map[string]any{}}); err == nil {
		t.Fatal("ecdsa key without a provider key accepted")
	}
	if _, err := m.key(&jwt.Token{Method: jwt.SigningMethodHS256, Header: map[string]any{}}); err == nil {
		t.Fatal("hmac key without a provider key accepted")
	}
}

func TestWorkerRoleIsRefused(t *testing.T) {
	_, err := New(testKeys{}, Settings{Rules: []Rule{{Value: "orders-app", Namespace: "orders", Role: "worker"}}})
	if err == nil || !strings.Contains(err.Error(), "grant write, not worker") {
		t.Fatalf("New = %v, want the worker role refused", err)
	}
	c := claimsFor(t, mapper(t, Settings{}), jwt.MapClaims{"sub": "app", "permissions": []any{"orders:worker"}})
	if c.Namespaces["orders"] != authorization.RoleUndefined {
		t.Fatalf("namespaces = %v, want no role from orders:worker", c.Namespaces)
	}
}

// TestGrantsThroughTemporalsAuthorizer checks the mapped roles against the authorizer the engine configures.
func TestGrantsThroughTemporalsAuthorizer(t *testing.T) {
	m := mapper(t, Settings{RolesClaim: "groups", Rules: []Rule{
		{Value: "orders-app", Namespace: "orders", Role: "write"},
		{Value: "auditors", Namespace: "orders", Role: "read"},
	}})
	authorizer := authorization.NewDefaultAuthorizer()
	poll := &authorization.CallTarget{APIName: "/temporal.api.workflowservice.v1.WorkflowService/PollWorkflowTaskQueue",
		Namespace: "orders"}
	for name, tt := range map[string]struct {
		groups    []any
		namespace string
		want      authorization.Decision
	}{
		"write polls":                {[]any{"orders-app"}, "orders", authorization.DecisionAllow},
		"read cannot poll":           {[]any{"auditors"}, "orders", authorization.DecisionDeny},
		"write in another namespace": {[]any{"orders-app"}, "billing", authorization.DecisionDeny},
	} {
		c := claimsFor(t, m, jwt.MapClaims{"sub": "caller", "groups": tt.groups})
		target := *poll
		target.Namespace = tt.namespace
		result, err := authorizer.Authorize(context.Background(), c, &target)
		if err != nil || result.Decision != tt.want {
			t.Errorf("%s: decision = %v (%v), want %v", name, result.Decision, err, tt.want)
		}
	}
}
