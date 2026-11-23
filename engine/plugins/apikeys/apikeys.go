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

// Package apikeys issues and verifies the runtime's own API keys: long-lived tokens signed with a key the runtime
// holds, so integrations and operators need no identity provider, as with Temporal Cloud's API keys.
//
// A key is a JWT signed with ES256. It names its holder ("sub"), its own ID ("jti") and the namespace roles it grants
// in Temporal's "<namespace>:<role>" form ("permissions"). A revocation list ends a key before it expires.
package apikeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is the "iss" of every key the runtime signs; it tells the runtime's keys from an identity provider's tokens.
const Issuer = "wso2-workflow-runtime"

const (
	permissionsClaim = "permissions"
	keyFileMode      = 0o600
)

// Grant is a role in a namespace; Namespace "*" grants the role in every namespace.
type Grant struct {
	Namespace string
	Role      string
}

// Key is the runtime's signing key and the ID ("kid") its keys carry.
type Key struct {
	private *ecdsa.PrivateKey
	id      string
}

// LoadKey reads the signing key from a PEM file; created reports that the file did not exist and was written.
func LoadKey(path string, create bool) (k *Key, created bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // the configured signing key
	if errors.Is(err, os.ErrNotExist) && create {
		return newKey(path)
	}
	if err != nil {
		return nil, false, fmt.Errorf("unable to read the API key signing key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, false, fmt.Errorf("%s holds no PEM key", path)
	}
	var private *ecdsa.PrivateKey
	switch block.Type {
	case "EC PRIVATE KEY":
		private, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		if parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			var ok bool
			if private, ok = parsed.(*ecdsa.PrivateKey); !ok {
				err = errors.New("not an ECDSA key")
			}
		}
	default:
		err = fmt.Errorf("unexpected PEM block %q", block.Type)
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if private.Curve != elliptic.P256() {
		return nil, false, fmt.Errorf("%s: the signing key must be an ECDSA P-256 key", path)
	}
	return &Key{private: private, id: keyID(&private.PublicKey)}, false, nil
}

func newKey(path string) (*Key, bool, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalECPrivateKey(private)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	// O_EXCL: two commands at once must not each write a different key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyFileMode) //nolint:gosec // the configured key
	if err != nil {
		return nil, false, fmt.Errorf("unable to create the API key signing key: %w", err)
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		// A partial file would block every later attempt; only a complete key stays.
		_ = os.Remove(path)
		return nil, false, fmt.Errorf("unable to write the API key signing key: %w", err)
	}
	return &Key{private: private, id: keyID(&private.PublicKey)}, true, nil
}

// keyID is a short fingerprint of the public key, so a key signed by another runtime is told apart from a forgery.
func keyID(public *ecdsa.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(public)
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// ID is the key's fingerprint, the "kid" of the keys it signs.
func (k *Key) ID() string { return k.id }

// Issued describes a key just created.
type Issued struct {
	Token   string
	ID      string
	Name    string
	Grants  []Grant
	Created time.Time
	Expires time.Time
}

// Create signs a key for name with grants that expires after ttl.
func (k *Key) Create(name string, grants []Grant, ttl time.Duration, now time.Time) (Issued, error) {
	if strings.TrimSpace(name) == "" {
		return Issued{}, errors.New("an API key needs a name")
	}
	// The issued-key record is one tab-separated line per key, so a name keeps its characters but no control ones.
	if strings.ContainsFunc(name, unicode.IsControl) {
		return Issued{}, fmt.Errorf("API key name %q must not contain tabs, newlines or other control characters", name)
	}
	if len(grants) == 0 {
		return Issued{}, errors.New("an API key needs at least one namespace and role")
	}
	if ttl <= 0 {
		return Issued{}, errors.New("an API key needs a positive lifetime")
	}
	permissions := make([]string, len(grants))
	for i, g := range grants {
		if err := g.check(); err != nil {
			return Issued{}, err
		}
		permissions[i] = g.Namespace + ":" + strings.ToLower(g.Role)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return Issued{}, err
	}
	now = now.UTC().Truncate(time.Second)
	issued := Issued{ID: hex.EncodeToString(id), Name: name, Grants: grants, Created: now, Expires: now.Add(ttl)}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": Issuer, "sub": name, "jti": issued.ID, "iat": now.Unix(), "nbf": now.Unix(),
		"exp": issued.Expires.Unix(), permissionsClaim: permissions,
	})
	token.Header["kid"] = k.id
	signed, err := token.SignedString(k.private)
	if err != nil {
		return Issued{}, err
	}
	issued.Token = signed
	return issued, nil
}

func (g Grant) check() error {
	if g.Namespace == "" || strings.ContainsAny(g.Namespace, ": ") || strings.ContainsFunc(g.Namespace, unicode.IsControl) {
		return fmt.Errorf("namespace %q must be a namespace name or *", g.Namespace)
	}
	switch strings.ToLower(g.Role) {
	case "read", "write", "admin":
		return nil
	}
	return fmt.Errorf("role %q must be read, write or admin", g.Role)
}

// IsAPIKey reports whether a bearer token claims to be one of the runtime's keys; the signature is checked later.
func IsAPIKey(raw string) bool {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return false
	}
	iss, _ := claims["iss"].(string)
	return iss == Issuer
}
