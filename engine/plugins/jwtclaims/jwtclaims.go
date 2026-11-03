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

// Package jwtclaims maps tokens from a customer's identity provider to Temporal's namespace roles.
//
// Temporal's default claim mapper reads one top-level claim holding "<namespace>:<role>" values. Identity providers
// rarely issue that, so this mapper also reads nested or space-separated claims, checks the issuer, and maps group or
// role names to namespace roles through rules.
package jwtclaims

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.temporal.io/server/common/authorization"
)

// AllNamespaces in a rule grants the role in every namespace (Temporal's system-level role).
const AllNamespaces = "*"

// temporalSystemNamespace is how Temporal's own permission format names the system scope.
const temporalSystemNamespace = "temporal-system"

const bearer = "bearer"

// Settings configure the mapper; the zero value of each field has a working default.
type Settings struct {
	// Issuer, when set, must equal the token's "iss".
	Issuer string
	// Audience, when set, must be in the token's "aud".
	Audience string
	// SubjectClaim names the caller's identity; "sub" by default.
	SubjectClaim string
	// RolesClaim is a dotted path to a list of strings, or a string of space- or comma-separated values.
	RolesClaim string
	// ValueRegex parses each value without rules; it needs the named groups "namespace" and "role".
	ValueRegex string
	// Rules map claim values to namespace roles. Without rules, values are "<namespace>:<role>".
	Rules []Rule
	// Leeway tolerates clock skew in "exp", "nbf" and "iat".
	Leeway time.Duration
}

// Rule grants Role in Namespace (or every namespace) to callers whose roles claim contains Value.
type Rule struct {
	Value     string
	Namespace string
	Role      string
}

// Mapper is an authorization.ClaimMapper for identity-provider tokens.
type Mapper struct {
	keys     authorization.TokenKeyProvider
	settings Settings
	regex    *regexp.Regexp
	rules    map[string][]grant
}

type grant struct {
	namespace string
	role      authorization.Role
}

var _ authorization.ClaimMapper = (*Mapper)(nil)

// New validates the settings and returns a mapper that verifies tokens with keys.
func New(keys authorization.TokenKeyProvider, s Settings) (*Mapper, error) {
	if s.SubjectClaim == "" {
		s.SubjectClaim = "sub"
	}
	if s.RolesClaim == "" {
		s.RolesClaim = "permissions"
	}
	m := &Mapper{keys: keys, settings: s, rules: map[string][]grant{}}
	if s.ValueRegex != "" {
		if len(s.Rules) > 0 {
			return nil, errors.New("use either a value regex or rules, not both")
		}
		r, err := regexp.Compile(s.ValueRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid value regex: %w", err)
		}
		if r.SubexpIndex("namespace") < 0 || r.SubexpIndex("role") < 0 {
			return nil, errors.New("the value regex needs the named groups \"namespace\" and \"role\"")
		}
		m.regex = r
	}
	for _, rule := range s.Rules {
		role, ok := ParseRole(rule.Role)
		if strings.EqualFold(strings.TrimSpace(rule.Role), "worker") {
			return nil, fmt.Errorf("rule for %q: grant write, not worker: Temporal's authorizer needs write for "+
				"an integration's workers to poll and complete tasks", rule.Value)
		}
		if !ok {
			return nil, fmt.Errorf("rule for %q: role %q must be read, write or admin", rule.Value, rule.Role)
		}
		if rule.Value == "" || rule.Namespace == "" {
			return nil, errors.New("every rule needs a value and a namespace")
		}
		m.rules[rule.Value] = append(m.rules[rule.Value], grant{namespace: rule.Namespace, role: role})
	}
	return m, nil
}

// GetClaims verifies the bearer token and maps its roles claim. A call without a token gets no roles.
func (m *Mapper) GetClaims(info *authorization.AuthInfo) (*authorization.Claims, error) {
	claims := &authorization.Claims{AuthType: "jwt"}
	if info == nil || info.AuthToken == "" {
		return claims, nil
	}
	scheme, raw, ok := strings.Cut(info.AuthToken, " ")
	if !ok || !strings.EqualFold(scheme, bearer) {
		return nil, errors.New("the authorization header is not a bearer token")
	}
	token, err := m.parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	subject, ok := lookup(token, m.settings.SubjectClaim).(string)
	if !ok || subject == "" {
		return nil, fmt.Errorf("the token has no string %q claim", m.settings.SubjectClaim)
	}
	claims.Subject = subject
	for _, value := range values(lookup(token, m.settings.RolesClaim)) {
		m.apply(claims, value)
	}
	return claims, nil
}

func (m *Mapper) parse(raw string) (jwt.MapClaims, error) {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods(m.keys.SupportedMethods()),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(m.settings.Leeway),
	}
	if m.settings.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(m.settings.Issuer))
	}
	if m.settings.Audience != "" {
		opts = append(opts, jwt.WithAudience(m.settings.Audience))
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(raw, claims, m.key, opts...); err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	return claims, nil
}

func (m *Mapper) key(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	alg := token.Method.Alg()
	switch token.Method.(type) {
	case *jwt.SigningMethodRSA, *jwt.SigningMethodRSAPSS:
		return m.keys.RsaKey(alg, kid)
	case *jwt.SigningMethodECDSA:
		return m.keys.EcdsaKey(alg, kid)
	case *jwt.SigningMethodHMAC:
		return m.keys.HmacKey(alg, kid)
	default:
		return nil, fmt.Errorf("unsupported signing method %s", alg)
	}
}

func (m *Mapper) apply(claims *authorization.Claims, value string) {
	if len(m.rules) > 0 {
		for _, g := range m.rules[value] {
			grantRole(claims, g.namespace, g.role)
		}
		return
	}
	var namespace, roleName string
	if m.regex != nil {
		match := m.regex.FindStringSubmatch(value)
		if match == nil {
			return
		}
		namespace, roleName = match[m.regex.SubexpIndex("namespace")], match[m.regex.SubexpIndex("role")]
	} else {
		var ok bool
		if namespace, roleName, ok = strings.Cut(value, ":"); !ok {
			return
		}
	}
	if role, ok := ParseRole(roleName); ok {
		grantRole(claims, namespace, role)
	}
}

func grantRole(claims *authorization.Claims, namespace string, role authorization.Role) {
	if namespace == AllNamespaces || namespace == temporalSystemNamespace {
		claims.System |= role
		return
	}
	if claims.Namespaces == nil {
		claims.Namespaces = map[string]authorization.Role{}
	}
	claims.Namespaces[namespace] |= role
}

// ParseRole maps a role name to Temporal's role. "worker" is not one: Temporal's default authorizer needs write for a
// worker to poll and complete tasks, so a worker-only grant would refuse every call it was meant for.
func ParseRole(name string) (authorization.Role, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "reader":
		return authorization.RoleReader, true
	case "write", "writer":
		return authorization.RoleWriter, true
	case "admin":
		return authorization.RoleAdmin, true
	}
	return authorization.RoleUndefined, false
}

// lookup follows a dotted path ("realm_access.roles"); a key that itself contains dots is tried first.
func lookup(claims map[string]any, path string) any {
	if v, ok := claims[path]; ok {
		return v
	}
	head, rest, ok := strings.Cut(path, ".")
	if !ok {
		return nil
	}
	nested, ok := claims[head].(map[string]any)
	if !ok {
		return nil
	}
	return lookup(nested, rest)
}

// values reads a list of strings, or one string of space- or comma-separated values (an OAuth "scope").
func values(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == ',' })
	}
	return nil
}
