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

package runtimeconfig

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/metrics"

	"github.com/wso2/integration-workflow-runtime/engine/plugins/jwtclaims"
)

// ClaimMapperName marks the generated configuration as using jwtclaims; the start command builds that mapper.
const ClaimMapperName = "wso2-jwt"

const authTypeJWT = "jwt"

// authTypeAPIKeys trusts only the runtime's own API keys, with no identity provider.
const authTypeAPIKeys = "apiKeys"

// Default files of [engine.auth.apiKeys], next to the signing key unless set.
const (
	defaultRevocationList = "revoked-api-keys.txt"
	issuedRecordFile      = "issued-api-keys.txt"
)

type engineTLSSettings struct {
	CertFile     string               `toml:"certFile"`
	KeyFile      string               `toml:"keyFile"`
	ClientCAFile string               `toml:"clientCaFile"`
	Internode    internodeTLSSettings `toml:"internode"`
}

type internodeTLSSettings struct {
	Enabled    bool   `toml:"enabled"`
	CertFile   string `toml:"certFile"`
	KeyFile    string `toml:"keyFile"`
	CAFile     string `toml:"caFile"`
	ServerName string `toml:"serverName"`
}

type authSettings struct {
	Type          string         `toml:"type"`
	JWKSURLs      []string       `toml:"jwksUrls"`
	KeyRefresh    string         `toml:"keyRefreshInterval"`
	Issuer        string         `toml:"issuer"`
	Audience      string         `toml:"audience"`
	SubjectClaim  string         `toml:"subjectClaim"`
	RolesClaim    string         `toml:"rolesClaim"`
	ValueRegex    string         `toml:"valueRegex"`
	LeewaySeconds int            `toml:"leewaySeconds"`
	Rules         []ruleSettings `toml:"rules"`
	APIKeys       apiKeySettings `toml:"apiKeys"`
}

// apiKeySettings turn on the runtime's own API keys, alone (type "apiKeys") or beside an identity provider ("jwt").
type apiKeySettings struct {
	SigningKey     string `toml:"signingKey"`
	RevocationList string `toml:"revocationList"`
}

// APIKeys are the files of the runtime's own API keys.
type APIKeys struct {
	// SigningKey signs and verifies the keys; every node needs the same file.
	SigningKey string
	// RevocationList holds revoked key IDs, one per line; it is re-read while the engine runs.
	RevocationList string
	// IssuedRecord lists the keys created, without their tokens, next to the revocation list.
	IssuedRecord string
}

func (k apiKeySettings) on() bool { return k.SigningKey != "" }

func (k apiKeySettings) files() *APIKeys {
	if !k.on() {
		return nil
	}
	dir := filepath.Dir(k.SigningKey)
	revoked := k.RevocationList
	if revoked == "" {
		revoked = filepath.Join(dir, defaultRevocationList)
	}
	// The record sits with the revocation list, which is writable even where the key is a read-only mounted secret.
	return &APIKeys{SigningKey: k.SigningKey, RevocationList: revoked,
		IssuedRecord: filepath.Join(filepath.Dir(revoked), issuedRecordFile)}
}

type ruleSettings struct {
	Value     string `toml:"value"`
	Namespace string `toml:"namespace"`
	Role      string `toml:"role"`
}

type metricsSettings struct {
	PrometheusAddress string `toml:"prometheusAddress"`
}

func (t engineTLSSettings) enabled() bool { return t.CertFile != "" || t.KeyFile != "" }

func (t engineTLSSettings) problems() []string {
	var problems []string
	if (t.CertFile == "") != (t.KeyFile == "") {
		problems = append(problems, "engine.tls needs both certFile and keyFile")
	}
	if t.ClientCAFile != "" && !t.enabled() {
		problems = append(problems, "engine.tls.clientCaFile needs engine.tls.certFile and keyFile")
	}
	in := t.Internode
	if in.Enabled {
		if in.CAFile == "" {
			problems = append(problems, "engine.tls.internode.caFile is required when internode TLS is enabled")
		}
		if (in.CertFile == "") != (in.KeyFile == "") {
			problems = append(problems, "engine.tls.internode needs both certFile and keyFile")
		}
		if in.CertFile == "" && !t.enabled() {
			problems = append(problems, "engine.tls.internode needs its own certFile and keyFile, or engine.tls's")
		}
	} else if in.CertFile != "" || in.KeyFile != "" || in.CAFile != "" || in.ServerName != "" {
		problems = append(problems, "engine.tls.internode files are set but engine.tls.internode.enabled is false")
	}
	for _, f := range []struct{ key, path string }{
		{"engine.tls.certFile", t.CertFile}, {"engine.tls.keyFile", t.KeyFile}, {"engine.tls.clientCaFile", t.ClientCAFile},
		{"engine.tls.internode.certFile", in.CertFile}, {"engine.tls.internode.keyFile", in.KeyFile},
		{"engine.tls.internode.caFile", in.CAFile},
	} {
		if f.path == "" {
			continue
		}
		if _, err := os.Stat(f.path); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f.key, err))
		}
	}
	return problems
}

// defaultInternodeServerName settles the name every node verifies in the others' internode certificates. Temporal
// reaches the internal frontend through its membership resolver, a target with no host name, so without a server name
// that connection fails, and with it the search attribute, namespace and system worker calls behind it. The name
// defaults to the certificate's DNS name when it has exactly one; an explicit name must be in the certificate.
func (t *engineTLSSettings) defaultInternodeServerName() error {
	in := &t.Internode
	if !in.Enabled {
		return nil
	}
	file := in.CertFile
	if file == "" {
		file = t.CertFile
	}
	cert, err := readCertificate(file)
	if err != nil {
		return fmt.Errorf("engine.tls.internode: %w", err)
	}
	if in.ServerName != "" {
		if err := cert.VerifyHostname(in.ServerName); err != nil {
			return fmt.Errorf("engine.tls.internode.serverName %q is not a name in %s", in.ServerName, file)
		}
		return nil
	}
	switch len(cert.DNSNames) {
	case 1:
		in.ServerName = cert.DNSNames[0]
		return nil
	case 0:
		return fmt.Errorf("engine.tls.internode.serverName is required: %s has no DNS name; issue the internode "+
			"certificates with a DNS name every node shares, or set serverName to a name every node's certificate "+
			"carries", file)
	default:
		return fmt.Errorf("engine.tls.internode.serverName is required: %s has several DNS names (%s); set it to "+
			"the one every node's certificate carries", file, strings.Join(cert.DNSNames, ", "))
	}
}

func readCertificate(file string) (*x509.Certificate, error) {
	data, err := os.ReadFile(file) //nolint:gosec // the configured certificate
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s holds no PEM certificate", file)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return cert, nil
}

func (t engineTLSSettings) temporalTLS() config.RootTLS {
	var root config.RootTLS
	if t.enabled() {
		root.Frontend.Server = config.ServerTLS{CertFile: t.CertFile, KeyFile: t.KeyFile}
		if t.ClientCAFile != "" {
			root.Frontend.Server.ClientCAFiles = []string{t.ClientCAFile}
			root.Frontend.Server.RequireClientAuth = true
		}
	}
	if in := t.Internode; in.Enabled {
		cert, key := in.CertFile, in.KeyFile
		if cert == "" {
			cert, key = t.CertFile, t.KeyFile
		}
		root.Internode.Server = config.ServerTLS{
			CertFile: cert, KeyFile: key, ClientCAFiles: []string{in.CAFile}, RequireClientAuth: true,
		}
		root.Internode.Client = config.ClientTLS{RootCAFiles: []string{in.CAFile}, ServerName: in.ServerName}
		// Temporal's own workers present the internode certificate to the internal frontend.
		root.SystemWorker = config.WorkerTLS{CertFile: cert, KeyFile: key, Client: root.Internode.Client}
	}
	return root
}

func (a authSettings) on() bool { return a.Type == authTypeJWT || a.Type == authTypeAPIKeys }

// securityProblems are the rules that join auth, TLS and the listen address.
func (e engineSettings) securityProblems() []string {
	if !e.Auth.on() {
		return nil
	}
	var problems []string
	if !e.TLS.enabled() {
		problems = append(problems, fmt.Sprintf(`engine.auth.type = %q needs engine.tls (certFile and keyFile): tokens `+
			"would cross the network in plain text", e.Auth.Type))
	}
	// The internal frontend gives Temporal's own services system access without a token, and the other services'
	// ports are open to anyone who reaches them, so beyond this machine they need internode mutual TLS.
	if ip := net.ParseIP(e.BindAddress); !e.TLS.Internode.Enabled && (ip == nil || !ip.IsLoopback()) {
		problems = append(problems, fmt.Sprintf("engine.auth needs engine.tls.internode when engine.bindAddress is %s: "+
			"the internal frontend and the other services' ports accept calls without a token, so they need mutual TLS "+
			"between the engine's nodes", e.BindAddress))
	}
	return problems
}

func (a authSettings) problems() []string {
	idp := len(a.JWKSURLs) > 0 || len(a.Rules) > 0 || a.Issuer != "" || a.Audience != "" || a.ValueRegex != ""
	switch a.Type {
	case "", "none":
		if idp || a.APIKeys.on() || a.APIKeys.RevocationList != "" {
			return []string{`engine.auth settings are present but engine.auth.type is not "jwt" or "apiKeys"`}
		}
		return nil
	case authTypeAPIKeys:
		var problems []string
		if !a.APIKeys.on() {
			problems = append(problems, `engine.auth.type = "apiKeys" needs engine.auth.apiKeys.signingKey`)
		}
		if idp {
			problems = append(problems, `engine.auth has identity-provider settings, but type = "apiKeys" trusts only the `+
				`runtime's own keys; set type = "jwt" to trust both`)
		}
		return problems
	case authTypeJWT:
	default:
		return []string{fmt.Sprintf(`engine.auth.type %q must be "jwt", "apiKeys" or "none"`, a.Type)}
	}
	var problems []string
	if a.APIKeys.RevocationList != "" && !a.APIKeys.on() {
		problems = append(problems, "engine.auth.apiKeys.revocationList needs engine.auth.apiKeys.signingKey")
	}
	if len(a.JWKSURLs) == 0 && a.Issuer == "" {
		problems = append(problems, "engine.auth needs the identity provider's issuer (its key set is discovered) or jwksUrls")
	}
	for _, u := range a.JWKSURLs {
		if err := jwtclaims.CheckURL(u); err != nil {
			problems = append(problems, "engine.auth.jwksUrls: "+err.Error())
		}
	}
	if err := jwtclaims.CheckURL(a.Issuer); len(a.JWKSURLs) == 0 && a.Issuer != "" && err != nil {
		problems = append(problems, "engine.auth.issuer (its key set is discovered from it): "+err.Error())
	}
	if a.KeyRefresh != "" {
		if d, err := time.ParseDuration(a.KeyRefresh); err != nil || d < time.Second {
			problems = append(problems, fmt.Sprintf("engine.auth.keyRefreshInterval %q must be a duration of at least 1s", a.KeyRefresh))
		}
	}
	if a.LeewaySeconds < 0 {
		problems = append(problems, "engine.auth.leewaySeconds must not be negative")
	}
	if _, err := jwtclaims.New(nil, a.claimSettings()); err != nil {
		problems = append(problems, "engine.auth: "+err.Error())
	}
	return problems
}

// claimSettings is what the start command passes to jwtclaims.
func (a authSettings) claimSettings() jwtclaims.Settings {
	rules := make([]jwtclaims.Rule, len(a.Rules))
	for i, r := range a.Rules {
		rules[i] = jwtclaims.Rule{Value: r.Value, Namespace: r.Namespace, Role: r.Role}
	}
	return jwtclaims.Settings{
		Issuer: a.Issuer, Audience: a.Audience, SubjectClaim: a.SubjectClaim, RolesClaim: a.RolesClaim,
		ValueRegex: a.ValueRegex, Rules: rules, Leeway: time.Duration(a.LeewaySeconds) * time.Second,
	}
}

func (a authSettings) temporalAuthorization() config.Authorization {
	if !a.on() {
		return config.Authorization{}
	}
	refresh := time.Minute
	if a.KeyRefresh != "" {
		refresh, _ = time.ParseDuration(a.KeyRefresh)
	}
	auth := config.Authorization{Authorizer: "default", ClaimMapper: ClaimMapperName}
	if a.Type == authTypeJWT {
		auth.JWTKeyProvider = config.JWTKeyProvider{KeySourceURIs: a.JWKSURLs, RefreshInterval: refresh}
	}
	return auth
}

func (m metricsSettings) problems() []string {
	if m.PrometheusAddress == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(m.PrometheusAddress)
	if err != nil || port == "" {
		return []string{fmt.Sprintf("engine.metrics.prometheusAddress %q must be host:port", m.PrometheusAddress)}
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return []string{fmt.Sprintf("engine.metrics.prometheusAddress %q has an invalid port", m.PrometheusAddress)}
	}
	return nil
}

func (m metricsSettings) temporalMetrics() *metrics.Config {
	if m.PrometheusAddress == "" {
		return nil
	}
	return &metrics.Config{Prometheus: &metrics.PrometheusConfig{
		Framework: metrics.FrameworkOpentelemetry, ListenAddress: m.PrometheusAddress, HandlerPath: "/metrics",
	}}
}

// ErrNoServices is returned by the CLI when profiles select no engine service and no --service is given.
var ErrNoServices = errors.New("runtime.profiles names no engine service; this node runs only other components")
