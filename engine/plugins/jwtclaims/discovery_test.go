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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discoveryServer(t *testing.T, body func(base string) string, status int) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body(srv.URL))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscover(t *testing.T) {
	srv := discoveryServer(t, func(base string) string {
		return `{"issuer":"` + base + `/oauth2/token","jwks_uri":"` + base + `/oauth2/jwks"}`
	}, http.StatusOK)
	got, err := Discover(context.Background(), srv.Client(), srv.URL+"/oauth2/token")
	if err != nil || got != srv.URL+"/oauth2/jwks" {
		t.Fatalf("Discover = %q, %v", got, err)
	}
}

func TestDiscoverErrors(t *testing.T) {
	tests := map[string]struct {
		body   func(string) string
		status int
		want   string
	}{
		"other issuer": {func(string) string { return `{"issuer":"https://evil","jwks_uri":"https://evil/jwks"}` }, 200, "names issuer"},
		"issuer with a trailing slash": {func(b string) string { return `{"issuer":"` + b + `/oauth2/token/","jwks_uri":"` + b + `/jwks"}` },
			200, "names issuer"},
		"plain jwks_uri": {func(b string) string {
			return `{"issuer":"` + b + `/oauth2/token","jwks_uri":"http://idp.example/jwks"}`
		},
			200, "unsafe jwks_uri"},
		"no jwks":   {func(b string) string { return `{"issuer":"` + b + `/oauth2/token"}` }, 200, "no jwks_uri"},
		"not json":  {func(string) string { return "<html>" }, 200, "invalid discovery document"},
		"not found": {func(string) string { return "" }, 404, "404"},
	}
	for name, tt := range tests {
		srv := discoveryServer(t, tt.body, tt.status)
		if _, err := Discover(context.Background(), srv.Client(), srv.URL+"/oauth2/token"); err == nil ||
			!strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tt.want)
		}
	}
	if _, err := Discover(context.Background(), http.DefaultClient, "http://127.0.0.1:1/x"); err == nil {
		t.Error("an unreachable issuer was accepted")
	}
	if _, err := Discover(context.Background(), http.DefaultClient, "://bad"); err == nil {
		t.Error("an invalid issuer was accepted")
	}
	if _, err := Discover(context.Background(), http.DefaultClient, "http://idp.example/oauth2/token"); err == nil ||
		!strings.Contains(err.Error(), "must use https") {
		t.Errorf("a plain http issuer: err = %v", err)
	}
}

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://idp.example/jwks":   true,
		"http://127.0.0.1:8080/jwks": true,
		"http://[::1]:8080/jwks":     true,
		"http://localhost/jwks":      true,
		"http://idp.example/jwks":    false,
		"http://10.0.0.5/jwks":       false,
		"ftp://idp.example/jwks":     false,
		"idp.example/jwks":           false,
		"":                           false,
	} {
		if err := CheckURL(raw); (err == nil) != ok {
			t.Errorf("CheckURL(%q) = %v, want ok = %v", raw, err, ok)
		}
	}
}
