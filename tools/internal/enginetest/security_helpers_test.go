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
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pki is a test certificate authority writing PEM files into a directory.
type pki struct {
	t    *testing.T
	dir  string
	key  *rsa.PrivateKey
	cert *x509.Certificate
}

func newPKI(t *testing.T, dir, name string) *pki {
	t.Helper()
	key := rsaKey(t)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, IsCA: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	p := &pki{t: t, dir: dir, key: key, cert: cert}
	p.write(name+".pem", "CERTIFICATE", der)
	return p
}

// issue writes <name>.crt and <name>.key for 127.0.0.1 and localhost, usable by servers and clients.
func (p *pki) issue(name string) (certFile, keyFile string) {
	p.t.Helper()
	key := rsaKey(p.t)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.cert, &key.PublicKey, p.key)
	if err != nil {
		p.t.Fatal(err)
	}
	return p.write(name+".crt", "CERTIFICATE", der), p.write(name+".key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
}

func (p *pki) caFile(name string) string { return filepath.Join(p.dir, name+".pem") }

func (p *pki) write(name, kind string, der []byte) string {
	p.t.Helper()
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		p.t.Fatal(err)
	}
	return path
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// identityProvider serves an OpenID Connect discovery document and a key set, and signs RS256 tokens.
type identityProvider struct {
	t      *testing.T
	key    *rsa.PrivateKey
	issuer string
}

func newIdentityProvider(t *testing.T) *identityProvider {
	t.Helper()
	p := &identityProvider{t: t, key: rsaKey(t)}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.issuer = srv.URL + "/oauth2/token"
	mux.HandleFunc("/oauth2/token/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": p.issuer, "jwks_uri": srv.URL + "/oauth2/jwks"})
	})
	mux.HandleFunc("/oauth2/jwks", func(w http.ResponseWriter, _ *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "k1",
			"alg": "RS256", "use": "sig", "n": b64(p.key.N.Bytes()), "e": b64(big.NewInt(int64(p.key.E)).Bytes())}}})
	})
	return p
}

// token signs claims with key (the provider's own unless forged), adding the issuer and an hour's expiry.
func (p *identityProvider) token(claims map[string]any, key *rsa.PrivateKey) string {
	p.t.Helper()
	if key == nil {
		key = p.key
	}
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = p.issuer
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			p.t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"}) + "." + enc(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		p.t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}
