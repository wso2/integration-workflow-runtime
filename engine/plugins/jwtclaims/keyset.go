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
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"go.temporal.io/server/common/authorization"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
)

// maxRedirects is net/http's own limit.
const maxRedirects = 10

// Client returns the HTTP client for discovery and key sets. It follows a redirect only to a URL CheckURL accepts, so
// an https endpoint cannot hand the request to plain http.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if err := CheckURL(req.URL.String()); err != nil {
		return fmt.Errorf("refused a redirect: %w", err)
	}
	return nil
}

// KeySet provides the RS256 and ES256 keys of JWKS URLs, refreshed on an interval, as Temporal's default provider
// does. It fetches through Client: Temporal's provider uses http.Get, which follows a redirect to plain http.
type KeySet struct {
	urls   []string
	client *http.Client
	logger log.Logger

	mu  sync.RWMutex
	rsa map[string]*rsa.PublicKey
	ec  map[string]*ecdsa.PublicKey

	stop     chan struct{}
	stopOnce sync.Once
}

var _ authorization.TokenKeyProvider = (*KeySet)(nil)

// NewKeySet reads the keys once and then every refresh (none when 0). A failed read is logged and the previous keys
// stay, so a provider that is briefly down does not stop the engine.
func NewKeySet(urls []string, refresh time.Duration, client *http.Client, logger log.Logger) *KeySet {
	k := &KeySet{urls: urls, client: client, logger: logger, rsa: map[string]*rsa.PublicKey{},
		ec: map[string]*ecdsa.PublicKey{}, stop: make(chan struct{})}
	if err := k.Refresh(); err != nil {
		logger.Error("unable to read the token keys", tag.Error(err))
	}
	if refresh > 0 {
		go k.refreshEvery(refresh)
	}
	return k
}

func (k *KeySet) refreshEvery(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-k.stop:
			return
		case <-ticker.C:
			if err := k.Refresh(); err != nil {
				k.logger.Error("unable to refresh the token keys", tag.Error(err))
			}
		}
	}
}

// Refresh reads every URL and replaces the keys only when all of them were read.
func (k *KeySet) Refresh() error {
	rsaKeys, ecKeys := map[string]*rsa.PublicKey{}, map[string]*ecdsa.PublicKey{}
	for _, u := range k.urls {
		if strings.TrimSpace(u) == "" {
			continue
		}
		if err := k.read(u, rsaKeys, ecKeys); err != nil {
			return err
		}
	}
	k.mu.Lock()
	k.rsa, k.ec = rsaKeys, ecKeys
	k.mu.Unlock()
	return nil
}

func (k *KeySet) read(u string, rsaKeys map[string]*rsa.PublicKey, ecKeys map[string]*ecdsa.PublicKey) error {
	if err := CheckURL(u); err != nil {
		return err
	}
	res, err := k.client.Get(u)
	if err != nil {
		return fmt.Errorf("unable to read %s: %w", u, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unable to read %s: %s", u, res.Status)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(res.Body).Decode(&set); err != nil {
		return fmt.Errorf("invalid key set at %s: %w", u, err)
	}
	for _, key := range set.Keys {
		switch pub := key.Key.(type) {
		case *rsa.PublicKey:
			rsaKeys[key.KeyID] = pub
		case *ecdsa.PublicKey:
			ecKeys[key.KeyID] = pub
		default:
			k.logger.Warn(fmt.Sprintf("ignoring key %q of unsupported type %T from %s", key.KeyID, key.Key, u))
		}
	}
	return nil
}

// RsaKey returns the RS256 key with the key ID kid.
func (k *KeySet) RsaKey(alg, kid string) (*rsa.PublicKey, error) {
	if !strings.EqualFold(alg, jwt.SigningMethodRS256.Name) {
		return nil, fmt.Errorf("unexpected signing algorithm: %s", alg)
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if key, ok := k.rsa[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("RSA key not found for key ID: %s", kid)
}

// EcdsaKey returns the ES256 key with the key ID kid.
func (k *KeySet) EcdsaKey(alg, kid string) (*ecdsa.PublicKey, error) {
	if !strings.EqualFold(alg, jwt.SigningMethodES256.Name) {
		return nil, fmt.Errorf("unexpected signing algorithm: %s", alg)
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if key, ok := k.ec[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("ECDSA key not found for key ID: %s", kid)
}

// HmacKey refuses HMAC: a shared secret is not a key set.
func (k *KeySet) HmacKey(alg, _ string) ([]byte, error) {
	return nil, fmt.Errorf("unsupported key type HMAC for: %s", alg)
}

// SupportedMethods is RS256 and ES256, as Temporal's default provider.
func (k *KeySet) SupportedMethods() []string {
	return []string{jwt.SigningMethodRS256.Name, jwt.SigningMethodES256.Name}
}

// Close stops the refresh.
func (k *KeySet) Close() { k.stopOnce.Do(func() { close(k.stop) }) }
