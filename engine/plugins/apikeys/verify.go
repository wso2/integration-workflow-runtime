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

package apikeys

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.temporal.io/server/common/authorization"
)

// revocationCheck is how often the revocation list's file is checked for changes.
const revocationCheck = 10 * time.Second

// Verifier checks the runtime's keys against the signing key and the revocation list.
type Verifier struct {
	key     *Key
	revoked *revocationList
	now     func() time.Time
}

// NewVerifier verifies keys signed with key; revokedPath names the revocation list, which may not exist yet.
func NewVerifier(key *Key, revokedPath string) *Verifier {
	return &Verifier{key: key, revoked: &revocationList{path: revokedPath}, now: time.Now}
}

// Claims verifies a raw key and returns the roles it grants.
func (v *Verifier) Claims(raw string) (*authorization.Claims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if kid, _ := t.Header["kid"].(string); kid != v.key.id {
			return nil, fmt.Errorf("the key was signed by another signing key (kid %q)", kid)
		}
		return &v.key.private.PublicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}), jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(v.now))
	if err != nil {
		return nil, fmt.Errorf("invalid API key: %w", err)
	}
	id, _ := claims["jti"].(string)
	if id == "" {
		return nil, errors.New("invalid API key: no key ID")
	}
	revoked, err := v.revoked.has(id, v.now())
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, fmt.Errorf("API key %s is revoked", id)
	}
	subject, _ := claims["sub"].(string)
	result := &authorization.Claims{Subject: subject, AuthType: "api-key"}
	permissions, _ := claims[permissionsClaim].([]any)
	for _, p := range permissions {
		s, _ := p.(string)
		namespace, role, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		grant(result, namespace, role)
	}
	return result, nil
}

func grant(claims *authorization.Claims, namespace, roleName string) {
	var role authorization.Role
	switch roleName {
	case "read":
		role = authorization.RoleReader
	case "write":
		role = authorization.RoleWriter
	case "admin":
		role = authorization.RoleAdmin
	default:
		return
	}
	if namespace == "*" {
		claims.System |= role
		return
	}
	if claims.Namespaces == nil {
		claims.Namespaces = map[string]authorization.Role{}
	}
	claims.Namespaces[namespace] |= role
}

// revocationList holds the revoked key IDs from a file of one ID per line ("#" starts a comment), re-read when the
// file changes, so a revocation reaches a running engine without a restart.
type revocationList struct {
	path    string
	mu      sync.Mutex
	checked time.Time
	modTime time.Time
	size    int64
	ids     map[string]bool
}

func (r *revocationList) has(id string, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ids == nil || now.Sub(r.checked) >= revocationCheck {
		if err := r.reload(); err != nil {
			return false, err
		}
		r.checked = now
	}
	return r.ids[id], nil
}

func (r *revocationList) reload() error {
	info, err := os.Stat(r.path)
	if errors.Is(err, os.ErrNotExist) {
		r.ids, r.modTime, r.size = map[string]bool{}, time.Time{}, 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to read the API key revocation list: %w", err)
	}
	if r.ids != nil && info.ModTime().Equal(r.modTime) && info.Size() == r.size {
		return nil
	}
	ids, err := ReadRevoked(r.path)
	if err != nil {
		return err
	}
	r.ids, r.modTime, r.size = ids, info.ModTime(), info.Size()
	return nil
}

// ReadRevoked reads a revocation list; a missing file revokes nothing.
func ReadRevoked(path string) (map[string]bool, error) {
	ids := map[string]bool{}
	f, err := os.Open(path) //nolint:gosec // the configured revocation list
	if errors.Is(err, os.ErrNotExist) {
		return ids, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unable to read the API key revocation list: %w", err)
	}
	defer func() { _ = f.Close() }()
	lines := bufio.NewScanner(f)
	// Room for lines longer than Revoke writes, so a hand-edited comment does not lock every key out.
	lines.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for lines.Scan() {
		line, _, _ := strings.Cut(lines.Text(), "#")
		if line = strings.TrimSpace(line); line != "" {
			ids[line] = true
		}
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("unable to read the API key revocation list: %w", err)
	}
	return ids, nil
}
