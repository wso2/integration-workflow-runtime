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

package command

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

// selfSigned writes a certificate for 127.0.0.1 and engine.test, with its key, and returns their paths.
func selfSigned(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "engine.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"engine.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "engine.crt"), filepath.Join(dir, "engine.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// tlsEngine serves gRPC health over TLS that requires a client certificate signed by the same certificate.
func tlsEngine(t *testing.T, certPath, keyPath string) string {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pemData, _ := os.ReadFile(certPath)
	pool.AppendCertsFromPEM(pemData)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert},
		ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12})))
	hs := health.NewServer()
	hs.SetServingStatus(healthServices["frontend"], healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestDialOverMutualTLS(t *testing.T) {
	certPath, keyPath := selfSigned(t)
	addr := tlsEngine(t, certPath, keyPath)
	run := func(args ...string) (int, string) {
		c := &cli{version: "test", stdout: &strings.Builder{}, stderr: &strings.Builder{},
			lookupEnv: func(k string) (string, bool) {
				if k == "WORKFLOW_CONFIG_FILE" {
					return filepath.Join(t.TempDir(), "cli.toml"), true
				}
				return "", false
			}, dial: dial}
		code := c.run(append([]string{"operator", "cluster", "health", "--address", addr}, args...))
		return code, c.stderr.(*strings.Builder).String()
	}
	if code, errOut := run("--tls-ca-path", certPath, "--tls-cert-path", certPath, "--tls-key-path", keyPath,
		"--tls-server-name", "engine.test", "--api-key", "t"); code != 0 {
		t.Fatalf("mutual TLS health = %d: %s", code, errOut)
	}
	if code, errOut := run("--tls-ca-path", certPath); code != 1 || !strings.Contains(errOut, "unable to check") {
		t.Fatalf("without a client certificate = %d: %s", code, errOut)
	}
	if code, errOut := run(); code != 1 || !strings.Contains(errOut, "unable to check") {
		t.Fatalf("plain text to a TLS engine = %d: %s", code, errOut)
	}
}

func TestDialOptionErrors(t *testing.T) {
	certPath, _ := selfSigned(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notPEM := filepath.Join(t.TempDir(), "ca.txt")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	on := true
	for name, tt := range map[string]struct {
		s    clientconfig.Settings
		want string
	}{
		"no port":                   {clientconfig.Settings{Address: "engine"}, "must be host:port"},
		"key in plain text":         {clientconfig.Settings{Address: "engine:7233", APIKey: "t"}, "only over TLS"},
		"missing CA":                {clientconfig.Settings{Address: "e:1", TLS: clientconfig.TLS{CAPath: "/nope"}}, "CA certificate"},
		"CA without a certificate":  {clientconfig.Settings{Address: "e:1", TLS: clientconfig.TLS{CAPath: notPEM}}, "no PEM certificate"},
		"half a client certificate": {clientconfig.Settings{Address: "e:1", TLS: clientconfig.TLS{CertPath: certPath}}, "both --tls-cert-path"},
		"unreadable client key":     {clientconfig.Settings{Address: "e:1", TLS: clientconfig.TLS{CertPath: certPath, KeyPath: "/nope"}}, "client certificate"},
		"missing key file":          {clientconfig.Settings{Address: "e:1", APIKeyFile: "/nope", TLS: clientconfig.TLS{Enabled: &on}}, "unable to read the API key"},
		"empty key file":            {clientconfig.Settings{Address: "e:1", APIKeyFile: empty, TLS: clientconfig.TLS{Enabled: &on}}, "is empty"},
	} {
		if _, err := dialOptions(tt.s); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tt.want)
		}
	}
	keyFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(keyFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]clientconfig.Settings{
		"key to loopback in plain text": {Address: "127.0.0.1:7233", APIKey: "t"},
		"key to localhost":              {Address: "localhost:7233", APIKeyFile: keyFile},
		"key over TLS":                  {Address: "engine:7233", APIKey: "t", TLS: clientconfig.TLS{Enabled: &on}},
	} {
		if _, err := dialOptions(s); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if key, err := apiKey(clientconfig.Settings{APIKeyFile: keyFile}); err != nil || key != "file-token" {
		t.Errorf("apiKey from a file = %q, %v", key, err)
	}
}

func TestDialSendsNothingUntilACall(t *testing.T) {
	conn, err := dial(clientconfig.Settings{Address: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("a call to a closed port succeeded")
	}
}
