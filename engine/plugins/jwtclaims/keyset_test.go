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
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/server/common/log"
)

var b64 = base64.RawURLEncoding.EncodeToString

func keySetJSON(t *testing.T, ec *ecdsa.PrivateKey) string {
	t.Helper()
	point, err := ec.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pub := signingKey.PublicKey
	return fmt.Sprintf(`{"keys":[
		{"kty":"RSA","kid":"rsa-1","alg":"RS256","use":"sig","n":%q,"e":%q},
		{"kty":"EC","kid":"ec-1","alg":"ES256","crv":"P-256","x":%q,"y":%q},
		{"kty":"oct","kid":"hmac-1","k":"c2VjcmV0"},
		{"kty":"EC","kid":"off-curve","crv":"P-256","x":%q,"y":%q}]}`,
		b64(pub.N.Bytes()), b64(big.NewInt(int64(pub.E)).Bytes()), b64(point[1:33]), b64(point[33:]),
		b64(point[1:33]), b64(point[1:33]))
}

// tlsClient is Client trusting the test server's certificate.
func tlsClient(srv *httptest.Server) *http.Client {
	c := Client(5 * time.Second)
	c.Transport = srv.Client().Transport
	return c
}

func TestKeySetReadsAndRefreshesKeys(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var down atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(keySetJSON(t, ec)))
	}))
	t.Cleanup(srv.Close)

	k := NewKeySet([]string{srv.URL + "/jwks"}, 0, tlsClient(srv), log.NewNoopLogger())
	t.Cleanup(k.Close)
	if key, err := k.RsaKey("RS256", "rsa-1"); err != nil || !key.Equal(&signingKey.PublicKey) {
		t.Fatalf("RsaKey = %v, %v", key, err)
	}
	if key, err := k.EcdsaKey("ES256", "ec-1"); err != nil || !key.Equal(&ec.PublicKey) {
		t.Fatalf("EcdsaKey = %v, %v", key, err)
	}
	if _, err := k.RsaKey("RS512", "rsa-1"); err == nil {
		t.Fatal("RsaKey accepted another algorithm")
	}
	if _, err := k.RsaKey("RS256", "unknown"); err == nil {
		t.Fatal("RsaKey found an unknown key ID")
	}
	if _, err := k.EcdsaKey("ES256", "off-curve"); err == nil {
		t.Fatal("EcdsaKey accepted a point that is not on the curve")
	}

	down.Store(true)
	if err := k.Refresh(); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Refresh = %v, want the provider's 503", err)
	}
	if _, err := k.RsaKey("RS256", "rsa-1"); err != nil {
		t.Fatalf("a failed refresh dropped the keys: %v", err)
	}
}

// redirectServer is an https server that redirects every request to target.
func redirectServer(t *testing.T, target string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRedirectsToPlainHTTPAreRefused(t *testing.T) {
	// Never reached: the client refuses the redirect before it connects.
	srv := redirectServer(t, "http://idp.example/jwks")

	k := NewKeySet([]string{srv.URL + "/jwks"}, 0, tlsClient(srv), log.NewNoopLogger())
	t.Cleanup(k.Close)
	if err := k.Refresh(); err == nil || !strings.Contains(err.Error(), "refused a redirect") {
		t.Fatalf("key set Refresh = %v, want a refused redirect", err)
	}
	if _, err := Discover(context.Background(), tlsClient(srv), srv.URL); err == nil ||
		!strings.Contains(err.Error(), "refused a redirect") {
		t.Fatalf("Discover = %v, want a refused redirect", err)
	}
}

func TestRedirectsToHTTPSAreFollowed(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(keySetJSON(t, ec)))
	}))
	t.Cleanup(target.Close)
	srv := redirectServer(t, target.URL+"/jwks")

	k := NewKeySet([]string{srv.URL + "/jwks"}, 0, tlsClient(srv), log.NewNoopLogger())
	t.Cleanup(k.Close)
	if _, err := k.RsaKey("RS256", "rsa-1"); err != nil {
		t.Fatalf("an https redirect was not followed: %v", err)
	}
}

func TestKeySetRefusesPlainHTTPURLs(t *testing.T) {
	k := NewKeySet([]string{"http://idp.example/jwks"}, 0, Client(time.Second), log.NewNoopLogger())
	t.Cleanup(k.Close)
	if err := k.Refresh(); err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("Refresh = %v, want a refused URL", err)
	}
}

func TestKeySetRefreshesOnItsInterval(t *testing.T) {
	var reads atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(srv.Close)
	k := NewKeySet([]string{srv.URL}, 10*time.Millisecond, tlsClient(srv), log.NewNoopLogger())
	deadline := time.Now().Add(5 * time.Second)
	for reads.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	k.Close()
	k.Close() // a second Close is harmless
	if reads.Load() < 3 {
		t.Fatalf("the key set was read %d times, want a refresh", reads.Load())
	}
}

func TestInvalidKeysAreRejected(t *testing.T) {
	for name, key := range map[string]jwk{
		"RSA without a modulus": {Kty: "RSA", E: "AQAB"},
		"RSA with an even e":    {Kty: "RSA", N: "AQAB", E: "Ag"},
		"RSA with a huge e":     {Kty: "RSA", N: "AQAB", E: "AQIDBAU"},
		"EC on another curve":   {Kty: "EC", Crv: "P-384", X: "AA", Y: "AA"},
		"EC with short points":  {Kty: "EC", Crv: "P-256", X: "AA", Y: "AA"},
		"EC with bad base64url": {Kty: "EC", Crv: "P-256", X: "!", Y: "!"},
	} {
		var err error
		if key.Kty == "RSA" {
			_, err = key.rsa()
		} else {
			_, err = key.ecdsa()
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
