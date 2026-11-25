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

package balsuite

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// APIKeyEngine is the suite's engine secured with TLS and only the runtime's own API keys, as an integration meets it
// in production without an identity provider.
type APIKeyEngine struct {
	Config         string // the runtime.toml to start the engine with
	CAFile         string // the CA that signed the engine's certificate
	AdminKey       string // an admin key in every namespace, for the runner's namespace setup
	IntegrationKey string // a write key in the suite's namespace, for the integration under test
}

// PrepareAPIKeyEngine writes, in dir, a CA and an engine certificate, a runtime.toml that adds TLS and API keys to
// base (the suite's SQLite configuration), and creates the two keys with the engine's own api-key command.
func PrepareAPIKeyEngine(engine, base, dir string) (APIKeyEngine, error) {
	settings, err := os.ReadFile(base) //nolint:gosec // the suite's configuration
	if err != nil {
		return APIKeyEngine{}, err
	}
	caFile, certFile, keyFile, err := writeCertificates(dir)
	if err != nil {
		return APIKeyEngine{}, fmt.Errorf("unable to create the test certificates: %w", err)
	}
	config := filepath.Join(dir, "runtime.toml")
	secured := string(settings) + fmt.Sprintf(`
[engine.tls]
certFile = %s
keyFile = %s

[engine.auth]
type = "apiKeys"

[engine.auth.apiKeys]
signingKey = %s
`, tomlLiteral(certFile), tomlLiteral(keyFile), tomlLiteral(filepath.Join(dir, "api-key-signing.pem")))
	if err := os.WriteFile(config, []byte(secured), 0o600); err != nil { //nolint:gosec // the runner's own temporary directory
		return APIKeyEngine{}, err
	}
	admin, err := createKey(engine, config, "suite-admin", "admin", "*")
	if err != nil {
		return APIKeyEngine{}, err
	}
	integration, err := createKey(engine, config, "suite-integration", "write", namespace)
	if err != nil {
		return APIKeyEngine{}, err
	}
	return APIKeyEngine{Config: config, CAFile: caFile, AdminKey: admin, IntegrationKey: integration}, nil
}

// CLIFlags are the Temporal CLI's TLS flags; the key goes through CLIEnv, so it stays out of process listings.
func (e APIKeyEngine) CLIFlags() []string { return []string{"--tls-ca-path", e.CAFile} }

// CLIEnv gives the Temporal CLI the admin key.
func (e APIKeyEngine) CLIEnv() []string { return []string{"TEMPORAL_API_KEY=" + e.AdminKey} }

// BalEnv points the suite's ballerina/workflow at the secured engine with the integration's key, overriding its
// Config.toml through Ballerina's configurable environment variables.
func (e APIKeyEngine) BalEnv() []string {
	return []string{
		"BAL_CONFIG_VAR_BALLERINA_WORKFLOW_MODE=SELF_HOSTED",
		"BAL_CONFIG_VAR_BALLERINA_WORKFLOW_URL=" + Address,
		"BAL_CONFIG_VAR_BALLERINA_WORKFLOW_AUTHAPIKEY=" + e.IntegrationKey,
		"BAL_CONFIG_VAR_BALLERINA_WORKFLOW_AUTHCACERT=" + e.CAFile,
	}
}

func createKey(engine, config, name, role, ns string) (string, error) {
	var stderr strings.Builder
	cmd := exec.Command(engine, "api-key", "create", "--runtime-config", config, "--name", name, "--role", role, //nolint:gosec // the engine under test
		"--namespace", ns, "--expires", "1d")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("api-key create %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", fmt.Errorf("api-key create %s printed no key", name)
	}
	return key, nil
}

// writeCertificates writes a CA and an engine certificate for localhost and 127.0.0.1, valid for a day.
func writeCertificates(dir string) (caFile, certFile, keyFile string, err error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "balsuite test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", err
	}
	engineKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "engine"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, caTemplate, &engineKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(engineKey)
	if err != nil {
		return "", "", "", err
	}
	caFile, certFile, keyFile = filepath.Join(dir, "ca.pem"), filepath.Join(dir, "engine.crt"), filepath.Join(dir, "engine.key")
	for file, block := range map[string]*pem.Block{caFile: {Type: "CERTIFICATE", Bytes: caDER},
		certFile: {Type: "CERTIFICATE", Bytes: der}, keyFile: {Type: "EC PRIVATE KEY", Bytes: keyDER}} {
		if err := os.WriteFile(file, pem.EncodeToMemory(block), 0o600); err != nil {
			return "", "", "", err
		}
	}
	return caFile, certFile, keyFile, nil
}

// tomlLiteral writes a path as a TOML literal string, which keeps Windows backslashes as they are.
func tomlLiteral(path string) string { return "'" + path + "'" }
