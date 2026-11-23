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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.temporal.io/server/common/authorization"
)

var now = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func testKey(t *testing.T) (*Key, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security", "api-key-signing.pem")
	k, created, err := LoadKey(path, true)
	if err != nil || !created {
		t.Fatalf("LoadKey = %v, created %v", err, created)
	}
	return k, path
}

func TestLoadKeyCreatesOnceAndReadsBack(t *testing.T) {
	k, path := testKey(t)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file = %v, %v; want mode 0600", info, err)
	}
	again, created, err := LoadKey(path, true)
	if err != nil || created || again.ID() != k.ID() {
		t.Fatalf("second LoadKey = %v, created %v, id %q; want the same key %q", err, created, again.ID(), k.ID())
	}
	if _, _, err := LoadKey(filepath.Join(t.TempDir(), "missing.pem"), false); err == nil {
		t.Error("a missing key was not refused without create")
	}
}

func TestLoadKeyFormats(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, block *pem.Block) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(ec)
	if _, _, err := LoadKey(write("pkcs8.pem", &pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), false); err != nil {
		t.Errorf("PKCS#8 P-256 key: %v", err)
	}
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der384, _ := x509.MarshalECPrivateKey(p384)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsa8, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	for name, block := range map[string]*pem.Block{
		"p384.pem": {Type: "EC PRIVATE KEY", Bytes: der384},
		"rsa.pem":  {Type: "PRIVATE KEY", Bytes: rsa8},
		"cert.pem": {Type: "CERTIFICATE", Bytes: []byte("x")},
		"bad.pem":  {Type: "EC PRIVATE KEY", Bytes: []byte("x")},
	} {
		if _, _, err := LoadKey(write(name, block), false); err == nil {
			t.Errorf("%s was accepted as a signing key", name)
		}
	}
	text := filepath.Join(dir, "text.pem")
	_ = os.WriteFile(text, []byte("not pem"), 0o600)
	if _, _, err := LoadKey(text, false); err == nil {
		t.Error("a file without PEM was accepted")
	}
}

func TestCreateAndVerify(t *testing.T) {
	k, path := testKey(t)
	issued, err := k.Create("orders-app", []Grant{{"orders", "Write"}, {"billing", "read"}}, 365*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !IsAPIKey(issued.Token) || len(issued.ID) != 32 || !issued.Expires.Equal(now.Add(365*24*time.Hour)) {
		t.Fatalf("issued = %+v", issued)
	}
	v := NewVerifier(k, filepath.Join(filepath.Dir(path), "revoked-api-keys.txt"))
	v.now = func() time.Time { return now.Add(time.Hour) }
	claims, err := v.Claims(issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "orders-app" || claims.Namespaces["orders"] != authorization.RoleWriter ||
		claims.Namespaces["billing"] != authorization.RoleReader || claims.System != 0 {
		t.Fatalf("claims = %+v", claims)
	}
	admin, _ := k.Create("ops", []Grant{{"*", "admin"}}, 2*time.Hour, now)
	if claims, err := v.Claims(admin.Token); err != nil || claims.System != authorization.RoleAdmin {
		t.Fatalf("admin claims = %+v, %v", claims, err)
	}
	v.now = func() time.Time { return now.Add(366 * 24 * time.Hour) }
	if _, err := v.Claims(issued.Token); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("an expired key = %v", err)
	}
}

func TestCreateRefusesBadInput(t *testing.T) {
	k, _ := testKey(t)
	for name, c := range map[string]struct {
		name   string
		grants []Grant
		ttl    time.Duration
	}{
		"no name":       {"", []Grant{{"orders", "write"}}, time.Hour},
		"no grant":      {"a", nil, time.Hour},
		"no lifetime":   {"a", []Grant{{"orders", "write"}}, 0},
		"bad role":      {"a", []Grant{{"orders", "worker"}}, time.Hour},
		"bad namespace": {"a", []Grant{{"or:ders", "write"}}, time.Hour},
	} {
		if _, err := k.Create(c.name, c.grants, c.ttl, now); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestVerifierRefusesOtherKeys(t *testing.T) {
	k, path := testKey(t)
	v := NewVerifier(k, filepath.Join(filepath.Dir(path), "revoked"))
	v.now = func() time.Time { return now }
	other, _ := testKey(t)
	foreign, _ := other.Create("x", []Grant{{"orders", "write"}}, time.Hour, now)
	if _, err := v.Claims(foreign.Token); err == nil || !strings.Contains(err.Error(), "another signing key") {
		t.Errorf("a key from another runtime = %v", err)
	}
	// A token that claims our key ID but is signed by another key fails the signature check.
	forged := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": Issuer, "sub": "x", "jti": "1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "permissions": []string{"*:admin"}})
	forged.Header["kid"] = k.ID()
	signed, _ := forged.SignedString(other.private)
	if _, err := v.Claims(signed); err == nil {
		t.Error("a forged key was accepted")
	}
	noID := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": Issuer, "sub": "x", "iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix()})
	noID.Header["kid"] = k.ID()
	signed, _ = noID.SignedString(k.private)
	if _, err := v.Claims(signed); err == nil || !strings.Contains(err.Error(), "no key ID") {
		t.Errorf("a key without an ID = %v", err)
	}
	if IsAPIKey("not a token") {
		t.Error("garbage was taken for an API key")
	}
}

func TestRevocationReachesARunningVerifier(t *testing.T) {
	k, path := testKey(t)
	revoked := filepath.Join(filepath.Dir(path), "revoked-api-keys.txt")
	v := NewVerifier(k, revoked)
	clock := now
	v.now = func() time.Time { return clock }
	issued, _ := k.Create("orders-app", []Grant{{"orders", "write"}}, time.Hour*24, now)
	if _, err := v.Claims(issued.Token); err != nil {
		t.Fatal(err)
	}
	if err := Revoke(revoked, issued.ID, "leaked # in a log"); err != nil {
		t.Fatal(err)
	}
	if err := Revoke(revoked, "has space", ""); err == nil {
		t.Error("an ID with a space was accepted")
	}
	// Within the check interval the cached list still holds; after it, the change is read.
	if _, err := v.Claims(issued.Token); err != nil {
		t.Fatalf("before the next check: %v", err)
	}
	clock = clock.Add(revocationCheck)
	if _, err := v.Claims(issued.Token); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("after revocation = %v", err)
	}
	ids, err := ReadRevoked(revoked)
	if err != nil || !ids[issued.ID] || len(ids) != 1 {
		t.Fatalf("revocation list = %v, %v", ids, err)
	}
	if err := os.Remove(revoked); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(revocationCheck)
	if _, err := v.Claims(issued.Token); err != nil {
		t.Fatalf("after the list was removed: %v", err)
	}
}

func TestRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issued-api-keys.txt")
	if entries, err := ReadRecord(path); err != nil || entries != nil {
		t.Fatalf("missing record = %v, %v", entries, err)
	}
	k, _ := testKey(t)
	issued, _ := k.Create("orders\tapp", []Grant{{"orders", "write"}, {"*", "read"}}, time.Hour, now)
	if err := Record(path, issued); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadRecord(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("record = %v, %v", entries, err)
	}
	e := entries[0]
	if e.ID != issued.ID || e.Name != "orders app" || e.Grants != "orders:write,*:read" || !e.Expires.Equal(issued.Expires) {
		t.Fatalf("entry = %+v", e)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), issued.Token) {
		t.Fatal("the record holds the token")
	}
	_ = os.WriteFile(path, []byte("broken line\n"), 0o600)
	if _, err := ReadRecord(path); err == nil {
		t.Error("a broken record was read")
	}
}

type fakeProvider struct{ called bool }

func (f *fakeProvider) GetClaims(*authorization.AuthInfo) (*authorization.Claims, error) {
	f.called = true
	return &authorization.Claims{Subject: "from-provider"}, nil
}

func TestMapperRoutesTokens(t *testing.T) {
	k, path := testKey(t)
	v := NewVerifier(k, filepath.Join(filepath.Dir(path), "revoked"))
	issued, _ := k.Create("orders-app", []Grant{{"orders", "write"}}, time.Hour, time.Now())
	provider := &fakeProvider{}
	m := NewMapper(v, provider)
	if c, err := m.GetClaims(&authorization.AuthInfo{AuthToken: "Bearer " + issued.Token}); err != nil ||
		c.Subject != "orders-app" || provider.called {
		t.Fatalf("API key = %+v, %v, provider called %v", c, err, provider.called)
	}
	idp := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "https://idp.example"})
	idpToken, _ := idp.SignedString([]byte("k"))
	if c, err := m.GetClaims(&authorization.AuthInfo{AuthToken: "Bearer " + idpToken}); err != nil ||
		c.Subject != "from-provider" {
		t.Fatalf("provider token = %+v, %v", c, err)
	}
	if c, err := m.GetClaims(&authorization.AuthInfo{}); err != nil || c.Subject != "" {
		t.Fatalf("no token = %+v, %v", c, err)
	}
	if _, err := m.GetClaims(&authorization.AuthInfo{AuthToken: "Basic x"}); err == nil {
		t.Error("a non-bearer header was accepted")
	}
	if _, err := NewMapper(v, nil).GetClaims(&authorization.AuthInfo{AuthToken: "Bearer " + idpToken}); err == nil ||
		!strings.Contains(err.Error(), "not one of this runtime's API keys") {
		t.Fatalf("provider token without a provider = %v", err)
	}
}
