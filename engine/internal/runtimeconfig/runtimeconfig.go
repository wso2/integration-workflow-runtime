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

// Package runtimeconfig turns the runtime's conf/runtime.toml into a Temporal server configuration.
//
// Only the [engine] and [database] tables are read here; other tables belong to other components.
package runtimeconfig

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
	"go.temporal.io/server/common/auth"
	"go.temporal.io/server/common/cluster"
	"go.temporal.io/server/common/config"
	"go.temporal.io/server/common/log"
	"gopkg.in/yaml.v3"
)

const (
	typePostgreSQL = "postgresql"
	typeMySQL      = "mysql"
	typeSQLite     = "sqlite"

	defaultStore    = "default"
	visibilityStore = "visibility"
	clusterName     = "active"
	redacted        = "******"

	// Temporal's membership ports sit this far below each service's gRPC port (7233 -> 6933).
	membershipPortOffset = 300
	// History shards cannot be changed after the first start; 512 is Temporal's recommendation for production.
	defaultHistoryShards = 512
)

var (
	pluginNames    = map[string]string{typePostgreSQL: "postgres12", typeMySQL: "mysql8", typeSQLite: "sqlite"}
	defaultDBPorts = map[string]int{typePostgreSQL: 5432, typeMySQL: 3306}
	logLevels      = []string{"debug", "info", "warn", "error"}
)

type settings struct {
	Engine   engineSettings   `toml:"engine"`
	Database databaseSettings `toml:"database"`
}

type engineSettings struct {
	BindAddress      string        `toml:"bindAddress"`
	BroadcastAddress string        `toml:"broadcastAddress"`
	HistoryShards    int32         `toml:"historyShards"`
	LogLevel         string        `toml:"logLevel"`
	Ports            portsSettings `toml:"ports"`
}

type portsSettings struct {
	Frontend         int `toml:"frontend"`
	FrontendHTTP     int `toml:"frontendHttp"`
	InternalFrontend int `toml:"internalFrontend"`
	History          int `toml:"history"`
	Matching         int `toml:"matching"`
	Worker           int `toml:"worker"`
}

type databaseSettings struct {
	Type           string      `toml:"type"`
	Host           string      `toml:"host"`
	Port           int         `toml:"port"`
	User           string      `toml:"user"`
	Password       string      `toml:"password"`
	Name           string      `toml:"name"`
	VisibilityName string      `toml:"visibilityName"`
	Path           string      `toml:"path"`
	InMemory       bool        `toml:"inMemory"`
	MaxConns       int         `toml:"maxConns"`
	TLS            tlsSettings `toml:"tls"`
}

type tlsSettings struct {
	Enabled                bool   `toml:"enabled"`
	CAFile                 string `toml:"caFile"`
	CertFile               string `toml:"certFile"`
	KeyFile                string `toml:"keyFile"`
	ServerName             string `toml:"serverName"`
	EnableHostVerification bool   `toml:"enableHostVerification"`
}

// Load reads a runtime.toml file and returns the validated Temporal server configuration it describes.
func Load(path string) (*config.Config, error) {
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // G304: the path is the operator's --runtime-config flag
	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %w", path, err)
	}
	return parse(data, os.LookupEnv)
}

// Render returns the configuration as YAML with every database password replaced, for inspection.
func Render(cfg *config.Config) ([]byte, error) {
	copied := *cfg
	copied.Persistence.DataStores = make(map[string]config.DataStore, len(cfg.Persistence.DataStores))
	for name, store := range cfg.Persistence.DataStores {
		if store.SQL != nil && store.SQL.Password != "" {
			sql := *store.SQL
			sql.Password = redacted
			store.SQL = &sql
		}
		copied.Persistence.DataStores[name] = store
	}
	return yaml.Marshal(&copied)
}

func parse(data []byte, lookupEnv func(string) (string, bool)) (*config.Config, error) {
	values, err := toml.Parser().Unmarshal(data)
	if err != nil {
		return nil, fmt.Errorf("invalid runtime configuration: %w", err)
	}
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(values, ""), nil); err != nil {
		return nil, fmt.Errorf("invalid runtime configuration: %w", err)
	}
	var s settings
	if err := decodeTable(k, "engine", &s.Engine); err != nil {
		return nil, err
	}
	if err := decodeTable(k, "database", &s.Database); err != nil {
		return nil, err
	}
	s.applyDefaults()
	if err := s.validate(); err != nil {
		return nil, err
	}
	password, err := resolveSecret(s.Database.Password, lookupEnv)
	if err != nil {
		return nil, err
	}
	cfg := s.temporalConfig(password)
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Temporal configuration: %w", err)
	}
	return cfg, nil
}

// decodeTable fills target from one of the tables this package owns, rejecting unknown keys (typos) in it; the
// other tables belong to other components and are left alone.
func decodeTable(k *koanf.Koanf, table string, target any) error {
	var meta mapstructure.Metadata
	conf := koanf.UnmarshalConf{Tag: "toml", DecoderConfig: &mapstructure.DecoderConfig{Metadata: &meta, Result: target}}
	if err := k.UnmarshalWithConf(table, target, conf); err != nil {
		return fmt.Errorf("invalid runtime configuration in [%s]: %w", table, err)
	}
	if len(meta.Unused) == 0 {
		return nil
	}
	unknown := make([]string, len(meta.Unused))
	for i, key := range meta.Unused {
		unknown[i] = table + "." + key
	}
	slices.Sort(unknown)
	return fmt.Errorf("unknown runtime configuration keys: %s", strings.Join(unknown, ", "))
}

func (s *settings) applyDefaults() {
	e, d := &s.Engine, &s.Database
	e.BindAddress = withDefault(e.BindAddress, "127.0.0.1")
	if e.BroadcastAddress == "" && e.BindAddress != "0.0.0.0" {
		e.BroadcastAddress = e.BindAddress
	}
	e.LogLevel = withDefault(e.LogLevel, "info")
	e.Ports.Frontend = withDefault(e.Ports.Frontend, 7233)
	e.Ports.FrontendHTTP = withDefault(e.Ports.FrontendHTTP, 7243)
	e.Ports.InternalFrontend = withDefault(e.Ports.InternalFrontend, 7236)
	e.Ports.History = withDefault(e.Ports.History, 7234)
	e.Ports.Matching = withDefault(e.Ports.Matching, 7235)
	e.Ports.Worker = withDefault(e.Ports.Worker, 7239)

	if d.Type == typeSQLite {
		e.HistoryShards = withDefault(e.HistoryShards, 1)
		d.MaxConns = withDefault(d.MaxConns, 1)
		return
	}
	e.HistoryShards = withDefault(e.HistoryShards, defaultHistoryShards)
	d.Port = withDefault(d.Port, defaultDBPorts[d.Type])
	d.Name = withDefault(d.Name, "temporal")
	d.VisibilityName = withDefault(d.VisibilityName, "temporal_visibility")
	d.MaxConns = withDefault(d.MaxConns, 20)
}

func (s *settings) validate() error {
	var problems []string
	e, d := s.Engine, s.Database
	if _, ok := pluginNames[d.Type]; !ok {
		problems = append(problems, fmt.Sprintf("database.type %q must be one of postgresql, mysql, sqlite", d.Type))
	}
	if !slices.Contains(logLevels, e.LogLevel) {
		problems = append(problems, fmt.Sprintf("engine.logLevel %q must be one of %s", e.LogLevel, strings.Join(logLevels, ", ")))
	}
	if net.ParseIP(e.BindAddress) == nil {
		problems = append(problems, fmt.Sprintf("engine.bindAddress %q must be an IP address", e.BindAddress))
	}
	if e.BroadcastAddress == "" {
		problems = append(problems, "engine.broadcastAddress is required when engine.bindAddress is 0.0.0.0")
	}
	if e.HistoryShards < 1 {
		problems = append(problems, "engine.historyShards must be at least 1")
	}
	problems = append(problems, e.Ports.problems()...)
	if d.MaxConns < 1 {
		problems = append(problems, "database.maxConns must be at least 1")
	}
	switch d.Type {
	case typeSQLite:
		if (d.Path == "") == !d.InMemory {
			problems = append(problems, "a sqlite database needs exactly one of database.path and database.inMemory = true")
		}
	case typePostgreSQL, typeMySQL:
		if d.Host == "" {
			problems = append(problems, "database.host is required")
		}
		if !validPort(d.Port) {
			problems = append(problems, fmt.Sprintf("database.port %d is not a valid port", d.Port))
		}
		if d.Name == d.VisibilityName {
			problems = append(problems, "database.name and database.visibilityName must differ")
		}
	}
	if !d.TLS.Enabled && (d.TLS.CAFile != "" || d.TLS.CertFile != "" || d.TLS.KeyFile != "") {
		problems = append(problems, "database.tls files are set but database.tls.enabled is false")
	}
	if len(problems) > 0 {
		return errors.New("invalid runtime configuration: " + strings.Join(problems, "; "))
	}
	return nil
}

func (p portsSettings) problems() []string {
	named := []struct {
		name string
		port int
	}{
		{"frontend", p.Frontend}, {"frontendHttp", p.FrontendHTTP}, {"internalFrontend", p.InternalFrontend},
		{"history", p.History}, {"matching", p.Matching}, {"worker", p.Worker},
	}
	var problems []string
	seen := map[int]string{}
	claim := func(name string, port int) {
		if other, taken := seen[port]; taken {
			problems = append(problems, fmt.Sprintf("engine.ports: %s and %s both use port %d", other, name, port))
			return
		}
		seen[port] = name
	}
	for _, n := range named {
		if !validPort(n.port) || (n.name != "frontendHttp" && !validPort(n.port-membershipPortOffset)) {
			problems = append(problems, fmt.Sprintf("engine.ports.%s %d is not a valid port", n.name, n.port))
			continue
		}
		claim(n.name, n.port)
		if n.name != "frontendHttp" {
			claim(n.name+" membership", n.port-membershipPortOffset)
		}
	}
	return problems
}

// resolveSecret returns a literal value, or the variable named by $env{NAME}.
func resolveSecret(value string, lookupEnv func(string) (string, bool)) (string, error) {
	switch {
	case strings.HasPrefix(value, "$env{") && strings.HasSuffix(value, "}"):
		name := strings.TrimSuffix(strings.TrimPrefix(value, "$env{"), "}")
		resolved, ok := lookupEnv(name)
		if !ok {
			return "", fmt.Errorf("database.password refers to environment variable %s, which is not set", name)
		}
		return resolved, nil
	case strings.HasPrefix(value, "$secret{"):
		return "", errors.New("database.password: $secret{...} references are not supported yet; use $env{NAME}")
	default:
		return value, nil
	}
}

func (s *settings) temporalConfig(password string) *config.Config {
	e := s.Engine
	frontend := net.JoinHostPort(e.BroadcastAddress, strconv.Itoa(e.Ports.Frontend))
	frontendHTTP := net.JoinHostPort(e.BroadcastAddress, strconv.Itoa(e.Ports.FrontendHTTP))
	return &config.Config{
		Global: config.Global{
			Membership: config.Membership{MaxJoinDuration: 30 * time.Second, BroadcastAddress: e.BroadcastAddress},
		},
		Persistence: config.Persistence{
			DefaultStore:     defaultStore,
			VisibilityStore:  visibilityStore,
			NumHistoryShards: e.HistoryShards,
			DataStores: map[string]config.DataStore{
				defaultStore:    {SQL: s.Database.sqlStore(s.Database.Name, password)},
				visibilityStore: {SQL: s.Database.sqlStore(s.Database.VisibilityName, password)},
			},
		},
		Log: log.Config{Stdout: true, Level: e.LogLevel},
		ClusterMetadata: &cluster.Config{
			FailoverVersionIncrement: 10,
			MasterClusterName:        clusterName,
			CurrentClusterName:       clusterName,
			ClusterInformation: map[string]cluster.ClusterInformation{
				clusterName: {Enabled: true, InitialFailoverVersion: 1, RPCAddress: frontend, HTTPAddress: frontendHTTP},
			},
		},
		DCRedirectionPolicy: config.DCRedirectionPolicy{Policy: "noop"},
		// The internal frontend is how Temporal's own services reach the frontend once an authorizer is on.
		Services: map[string]config.Service{
			"frontend":          {RPC: e.rpc(e.Ports.Frontend, e.Ports.FrontendHTTP)},
			"internal-frontend": {RPC: e.rpc(e.Ports.InternalFrontend, 0)},
			"history":           {RPC: e.rpc(e.Ports.History, 0)},
			"matching":          {RPC: e.rpc(e.Ports.Matching, 0)},
			"worker":            {RPC: e.rpc(e.Ports.Worker, 0)},
		},
	}
}

func (e engineSettings) rpc(grpcPort, httpPort int) config.RPC {
	return config.RPC{
		GRPCPort:       grpcPort,
		MembershipPort: grpcPort - membershipPortOffset,
		BindOnIP:       e.BindAddress,
		HTTPPort:       httpPort,
	}
}

func (d databaseSettings) sqlStore(name, password string) *config.SQL {
	if d.Type == typeSQLite {
		return d.sqliteStore()
	}
	return &config.SQL{
		User:            d.User,
		Password:        password,
		PluginName:      pluginNames[d.Type],
		DatabaseName:    name,
		ConnectAddr:     net.JoinHostPort(d.Host, strconv.Itoa(d.Port)),
		ConnectProtocol: "tcp",
		MaxConns:        d.MaxConns,
		MaxIdleConns:    d.MaxConns,
		MaxConnLifetime: time.Hour,
		TLS:             d.TLS.temporalTLS(),
	}
}

// sqliteStore keeps both stores in one database: a file that creates its own schema, or memory for tests.
func (d databaseSettings) sqliteStore() *config.SQL {
	name := d.Path
	attributes := map[string]string{"cache": "private", "setup": "true", "journal_mode": "wal", "synchronous": "2"}
	if d.InMemory {
		name = "default"
		attributes = map[string]string{"mode": "memory", "cache": "private"}
	}
	return &config.SQL{
		PluginName:        pluginNames[typeSQLite],
		DatabaseName:      name,
		ConnectAddr:       "localhost",
		ConnectProtocol:   "tcp",
		ConnectAttributes: attributes,
		MaxConns:          d.MaxConns,
		MaxIdleConns:      d.MaxConns,
		MaxConnLifetime:   time.Hour,
	}
}

func (t tlsSettings) temporalTLS() *auth.TLS {
	if !t.Enabled {
		return nil
	}
	return &auth.TLS{
		Enabled:                true,
		CaFile:                 t.CAFile,
		CertFile:               t.CertFile,
		KeyFile:                t.KeyFile,
		ServerName:             t.ServerName,
		EnableHostVerification: t.EnableHostVerification,
	}
}

func validPort(port int) bool { return port > 0 && port < 65536 }

func withDefault[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}
