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

// Package command implements workflow-cli, the Workflow Runtime's command line client. Its commands and flags follow
// the Temporal CLI's, so `temporal operator namespace list` is `workflow-cli operator namespace list` here.
package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

const (
	outputText = "text"
	outputJSON = "json"
)

// cli is one run of workflow-cli; tests replace its streams, environment and dialer.
type cli struct {
	version   string
	stdout    io.Writer
	stderr    io.Writer
	lookupEnv func(string) (string, bool)
	dial      func(clientconfig.Settings) (*grpc.ClientConn, error)

	// Global flags.
	address, namespace, apiKey, apiKeyFile         string
	tls                                            bool
	tlsCAPath, tlsCertPath, tlsKeyPath, tlsSrvName string
	profile, configFile, output                    string
	timeout                                        time.Duration
}

// Main runs workflow-cli with args and returns the process exit code.
func Main(version string, args []string) int {
	c := &cli{version: version, stdout: os.Stdout, stderr: os.Stderr, lookupEnv: os.LookupEnv, dial: dial}
	return c.run(args)
}

func (c *cli) run(args []string) int {
	root := c.root()
	root.SetArgs(args)
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(c.stderr, "Error:", err)
		var exit exitError
		if errors.As(err, &exit) {
			return int(exit)
		}
		return 1
	}
	return 0
}

// exitError ends the command with a code and no further message, as `cluster health` does when not serving.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

func (c *cli) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "workflow-cli",
		Short: "Operate a WSO2 Workflow Runtime engine",
		Long: "workflow-cli manages a Workflow Runtime engine: its cluster, namespaces and search attributes, the\n" +
			"workflows integrations run, and the task queues their workers poll.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	f := root.PersistentFlags()
	f.StringVar(&c.address, "address", "", `the engine's frontend, host:port (default "localhost:7233")`)
	f.StringVarP(&c.namespace, "namespace", "n", "", `the namespace (default "default")`)
	f.StringVar(&c.apiKey, "api-key", "", "a token from your identity provider, sent as a bearer token")
	f.StringVar(&c.apiKeyFile, "api-key-file", "", "a file holding the token")
	f.BoolVar(&c.tls, "tls", false, "connect with TLS; implied by any --tls-* flag")
	f.StringVar(&c.tlsCAPath, "tls-ca-path", "", "the CA certificate that signed the engine's certificate")
	f.StringVar(&c.tlsCertPath, "tls-cert-path", "", "a client certificate, when the engine requires one")
	f.StringVar(&c.tlsKeyPath, "tls-key-path", "", "the client certificate's key")
	f.StringVar(&c.tlsSrvName, "tls-server-name", "", "the name to verify in the engine's certificate")
	f.StringVar(&c.profile, "profile", "", `the profile to use (default "default", or WORKFLOW_PROFILE)`)
	f.StringVar(&c.configFile, "config-file", "", "the profile file (default <user config>/wso2-workflow/cli.toml)")
	f.StringVarP(&c.output, "output", "o", outputText, "output format: text or json")
	f.DurationVar(&c.timeout, "command-timeout", 30*time.Second, "limit for each command")

	root.AddCommand(c.versionCommand(), c.operatorCommand(), c.workflowCommand(), c.taskQueueCommand(), c.configCommand())
	return root
}

func (c *cli) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "workflow-cli %s\n", c.version)
			return err
		},
	}
}

func (c *cli) operatorCommand() *cobra.Command {
	op := &cobra.Command{Use: "operator", Short: "Manage the engine: cluster, namespaces, search attributes"}
	op.AddCommand(c.clusterCommand(), c.namespaceCommand(), c.searchAttributeCommand())
	return op
}

// configPath is --config-file, then WORKFLOW_CONFIG_FILE, then the default path.
func (c *cli) configPath() (string, error) {
	if c.configFile != "" {
		return c.configFile, nil
	}
	if v, ok := c.lookupEnv("WORKFLOW_CONFIG_FILE"); ok && v != "" {
		return v, nil
	}
	return clientconfig.DefaultPath()
}

// profileName is --profile, then WORKFLOW_PROFILE, then the default profile.
func (c *cli) profileName() string {
	if c.profile != "" {
		return c.profile
	}
	if v, ok := c.lookupEnv("WORKFLOW_PROFILE"); ok && v != "" {
		return v
	}
	return clientconfig.DefaultProfile
}

// settings resolves the connection: defaults, then the profile, then the environment, then the flags given.
func (c *cli) settings(cmd *cobra.Command) (clientconfig.Settings, error) {
	s := clientconfig.Defaults()
	path, err := c.configPath()
	if err != nil {
		return s, err
	}
	file, err := clientconfig.Open(path)
	if err != nil {
		return s, err
	}
	profile := c.profileName()
	if !file.Has(profile) && profile != clientconfig.DefaultProfile {
		return s, fmt.Errorf("no profile %q in %s", profile, path)
	}
	if err := file.Apply(profile, &s); err != nil {
		return s, err
	}
	if err := clientconfig.ApplyEnv(&s, c.lookupEnv); err != nil {
		return s, err
	}
	flags := cmd.Flags()
	for flag, value := range map[string]*string{
		"address": &s.Address, "namespace": &s.Namespace, "api-key": &s.APIKey, "api-key-file": &s.APIKeyFile,
		"tls-ca-path": &s.TLS.CAPath, "tls-cert-path": &s.TLS.CertPath, "tls-key-path": &s.TLS.KeyPath,
		"tls-server-name": &s.TLS.ServerName,
	} {
		if v, _ := flags.GetString(flag); flags.Changed(flag) {
			*value = v
		}
	}
	if flags.Changed("tls") {
		s.TLS.Enabled = &c.tls
	}
	if c.output != outputText && c.output != outputJSON {
		return s, fmt.Errorf("--output %q must be text or json", c.output)
	}
	return s, nil
}

// connect resolves the settings and dials the engine; the caller closes the connection and cancels the context.
func (c *cli) connect(cmd *cobra.Command) (*grpc.ClientConn, clientconfig.Settings, context.Context, context.CancelFunc,
	error) {
	s, err := c.settings(cmd)
	if err != nil {
		return nil, s, nil, nil, err
	}
	conn, err := c.dial(s)
	if err != nil {
		return nil, s, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), c.timeout)
	return conn, s, ctx, cancel, nil
}
