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
	"strings"

	"go.temporal.io/server/common/authorization"
)

// Mapper is an authorization.ClaimMapper for the runtime's keys. Tokens from an identity provider go to the
// mapper it wraps, when the runtime trusts one.
type Mapper struct {
	keys     *Verifier
	provider authorization.ClaimMapper
}

var _ authorization.ClaimMapper = (*Mapper)(nil)

// NewMapper verifies the runtime's keys with keys and hands every other token to provider, which may be nil.
func NewMapper(keys *Verifier, provider authorization.ClaimMapper) *Mapper {
	return &Mapper{keys: keys, provider: provider}
}

// GetClaims maps a call's token. A call without a token gets no roles.
func (m *Mapper) GetClaims(info *authorization.AuthInfo) (*authorization.Claims, error) {
	if info == nil || info.AuthToken == "" {
		return &authorization.Claims{}, nil
	}
	scheme, raw, ok := strings.Cut(info.AuthToken, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return nil, errors.New("the authorization header is not a bearer token")
	}
	raw = strings.TrimSpace(raw)
	if IsAPIKey(raw) {
		return m.keys.Claims(raw)
	}
	if m.provider == nil {
		return nil, errors.New("the token is not one of this runtime's API keys")
	}
	return m.provider.GetClaims(info)
}
