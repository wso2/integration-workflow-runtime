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

// NewVerifier verifies keys signed with key; revokedPath names the revocation list. warn, when set, is told when the
// list is missing: once at start when it was never copied, and at every check when a list read before is gone.
func NewVerifier(key *Key, revokedPath string, warn func(string)) *Verifier {
	return &Verifier{key: key, revoked: &revocationList{path: revokedPath, warn: warn}, now: time.Now}
}

// Refresh reads the revocation list now, so a missing list is reported at start rather than at the first call.
func (v *Verifier) Refresh() error {
	_, err := v.revoked.has("", v.now())
	return err
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
	warn    func(string)
	missing bool // the missing list was reported
	loaded  bool // a list was read, so its disappearance is an error rather than "nothing revoked"
	mu      sync.Mutex
	read    bool
	err     error
	checked time.Time
	modTime time.Time
	size    int64
	ids     map[string]bool
}

// has reports whether id is revoked; the list, or the error reading it, is kept until the next check.
func (r *revocationList) has(id string, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.read || now.Sub(r.checked) >= revocationCheck {
		r.err, r.read, r.checked = r.reload(), true, now
	}
	if r.err != nil {
		return false, r.err
	}
	return r.ids[id], nil
}

func (r *revocationList) reload() error {
	info, err := os.Stat(r.path)
	if errors.Is(err, os.ErrNotExist) && r.loaded {
		// A list that disappears, such as a shared volume that did not mount, must not bring revoked keys back.
		msg := fmt.Sprintf("the API key revocation list %s was removed; API keys are refused until it is back", r.path)
		r.notify(msg)
		return errors.New(msg)
	}
	if errors.Is(err, os.ErrNotExist) {
		if !r.missing {
			r.notify(fmt.Sprintf("the API key revocation list %s does not exist, so no key is revoked on this node; "+
				"copy it from the node where keys are revoked", r.path))
		}
		r.ids, r.modTime, r.size, r.missing = map[string]bool{}, time.Time{}, 0, true
		return nil
	}
	r.missing = false
	if err != nil {
		return fmt.Errorf("unable to read the API key revocation list: %w", err)
	}
	if writableByOthers(info) {
		return fmt.Errorf("the API key revocation list %s can be written by other users (mode %v), who could "+
			"un-revoke keys; allow at most its owner and group to write it, for example chmod 640", r.path,
			info.Mode().Perm())
	}
	if r.loaded && info.ModTime().Equal(r.modTime) && info.Size() == r.size {
		return nil
	}
	ids, err := ReadRevoked(r.path)
	if err != nil {
		return err
	}
	r.ids, r.modTime, r.size, r.loaded = ids, info.ModTime(), info.Size(), true
	return nil
}

func (r *revocationList) notify(msg string) {
	if r.warn != nil {
		r.warn(msg)
	}
}

// ReadRevoked reads a revocation list; a missing file revokes nothing. A last line without its newline is kept: it is
// either a complete ID written by hand, or a fragment of one that matches no key.
func ReadRevoked(path string) (map[string]bool, error) {
	ids := map[string]bool{}
	lines, err := readLines(path, true)
	if errors.Is(err, os.ErrNotExist) {
		return ids, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unable to read the API key revocation list: %w", err)
	}
	for _, line := range lines {
		line, _, _ = strings.Cut(line, "#")
		if line = strings.TrimSpace(line); line != "" {
			ids[line] = true
		}
	}
	return ids, nil
}
