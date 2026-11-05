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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// discoveryTimeout bounds the OpenID Connect discovery request made at start.
const discoveryTimeout = 15 * time.Second

// Discover reads the provider's OpenID Connect discovery document (<issuer>/.well-known/openid-configuration) and
// returns its key set URL. The document must name the same issuer, as the OpenID Connect specification requires.
func Discover(ctx context.Context, client *http.Client, issuer string) (string, error) {
	if err := CheckURL(issuer); err != nil {
		return "", fmt.Errorf("invalid issuer: %w", err)
	}
	url := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("invalid issuer %q: %w", issuer, err)
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to read %s: %w", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unable to read %s: %s", url, res.Status)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxDocumentBytes)).Decode(&doc); err != nil {
		return "", fmt.Errorf("invalid discovery document at %s: %w", url, err)
	}
	// Exactly: tokens are checked against the configured issuer as written, trailing slash included.
	if doc.Issuer != issuer {
		return "", fmt.Errorf("the discovery document at %s names issuer %q, not %q", url, doc.Issuer, issuer)
	}
	if doc.JWKSURI == "" {
		return "", errors.New("the discovery document at " + url + " has no jwks_uri")
	}
	if err := CheckURL(doc.JWKSURI); err != nil {
		return "", fmt.Errorf("the discovery document at %s names an unsafe jwks_uri: %w", url, err)
	}
	return doc.JWKSURI, nil
}

// CheckURL accepts an https URL, and plain http only to a loopback host: keys and discovery documents fetched over
// the network in plain text could be replaced on the way, letting anyone sign tokens the engine trusts.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not an absolute URL", raw)
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && isLoopback(u.Hostname()):
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("%q must use https; plain http is accepted only for a loopback host", raw)
	default:
		return fmt.Errorf("%q must be an https URL", raw)
	}
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
