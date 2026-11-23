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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

// dial opens a connection to the engine with TLS and the API key the settings ask for; nothing is sent until a call.
func dial(s clientconfig.Settings) (*grpc.ClientConn, error) {
	opts, err := dialOptions(s)
	if err != nil {
		return nil, err
	}
	// Go's dialer tries every address of a name in turn, so "localhost" reaching an IPv4-only engine reports the
	// engine's answer rather than a refused [::1].
	opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}))
	return grpc.NewClient("passthrough:///"+s.Address, opts...)
}

// maxResponseSize matches the Temporal Go SDK: a history page holds up to 256 events of up to 2 MiB each, far above
// gRPC's default 4 MiB.
const maxResponseSize = 128 << 20

func dialOptions(s clientconfig.Settings) ([]grpc.DialOption, error) {
	if _, _, err := net.SplitHostPort(s.Address); err != nil {
		return nil, fmt.Errorf("address %q must be host:port", s.Address)
	}
	transport := insecure.NewCredentials()
	if s.TLS.On() {
		cfg, err := tlsConfig(s.TLS)
		if err != nil {
			return nil, err
		}
		transport = credentials.NewTLS(cfg)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(transport),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxResponseSize))}
	if !s.TLS.On() {
		opts = append(opts, grpc.WithChainUnaryInterceptor(hintTLS))
	}

	key, err := apiKey(s)
	if err != nil {
		return nil, err
	}
	if key != "" {
		// A token in plain text could be read on the way; send it unencrypted only to this machine.
		if !s.TLS.On() && !isLoopback(s.Address) {
			return nil, fmt.Errorf("an API key is sent only over TLS to %s; add --tls or --tls-ca-path", s.Address)
		}
		auth := "Bearer " + key
		opts = append(opts, grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any,
			cc *grpc.ClientConn, invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, "authorization", auth), method, req, reply, cc,
				callOpts...)
		}))
	}
	return opts, nil
}

// hintTLS names the likely cause when a plain-text call finds an engine that speaks TLS: the server closes the
// connection before answering gRPC's preface.
func hintTLS(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable &&
		(strings.Contains(st.Message(), "server preface") || strings.Contains(st.Message(), "EOF")) {
		return status.Error(codes.Unavailable, st.Message()+"; the engine may require TLS: add --tls or --tls-ca-path")
	}
	return err
}

func tlsConfig(t clientconfig.TLS) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName}
	if t.CAPath != "" {
		pem, err := os.ReadFile(t.CAPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read the CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate", t.CAPath)
		}
		cfg.RootCAs = pool
	}
	if (t.CertPath == "") != (t.KeyPath == "") {
		return nil, errors.New("a client certificate needs both --tls-cert-path and --tls-key-path")
	}
	if t.CertPath != "" {
		cert, err := tls.LoadX509KeyPair(t.CertPath, t.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read the client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// apiKey is --api-key, or the first line of the key file.
func apiKey(s clientconfig.Settings) (string, error) {
	if s.APIKey != "" || s.APIKeyFile == "" {
		return s.APIKey, nil
	}
	data, err := os.ReadFile(s.APIKeyFile)
	if err != nil {
		return "", fmt.Errorf("unable to read the API key: %w", err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	key := strings.TrimSpace(line)
	if key == "" {
		return "", fmt.Errorf("%s is empty", s.APIKeyFile)
	}
	return key, nil
}

func isLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
