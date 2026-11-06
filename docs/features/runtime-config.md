# Runtime configuration

Status: available · since 0.1.0 (in development)

## What it does

`conf/runtime.toml` is the one file customers edit. The engine reads its `[engine]` and `[database]` tables and
generates Temporal's server configuration from them, so nobody writes Temporal's YAML by hand. The generated
configuration stays in memory: the database password is never written to disk.

```sh
workflow-engine start --runtime-config conf/runtime.toml
workflow-engine render-config --runtime-config conf/runtime.toml   # print the generated configuration, password hidden
```

`--config-file <temporal.yaml>` still starts the engine from a Temporal configuration used as is; pass one or the
other, not both.

## Configuration

### `[database]`

| Key | Default | Meaning |
|---|---|---|
| `type` | required | `postgresql`, `mysql` or `sqlite` |
| `host` | required for PostgreSQL and MySQL | Database host |
| `port` | 5432 (PostgreSQL), 3306 (MySQL) | Database port |
| `user`, `password` | empty | Credentials. `password` is a literal or `$env{NAME}` (read from the environment) |
| `name` | `temporal` | Database for Temporal's workflow state |
| `visibilityName` | `temporal_visibility` | Database for Temporal's visibility (instance lists and search); must differ from `name` |
| `path` | | SQLite only: the database file. Both stores share it, and its schema is created on first start |
| `inMemory` | false | SQLite only: keep everything in memory (tests and trying out); use instead of `path` |
| `maxConns` | 20 (1 for SQLite) | Connections per store; must be 1 for an in-memory SQLite database, where each connection would get its own empty database; that one connection is never retired |
| `manageSchema` | `true` | Create the databases and bring their schemas up to date on start ([database schema](database-schema.md)) |
| `[database.tls]` `enabled`, `caFile`, `certFile` and `keyFile` (both or neither), `serverName` (MySQL only) | off | TLS to the database (PostgreSQL and MySQL; refused for SQLite) |
| `[database.tls]` `enableHostVerification` | `true` | Verify the server's certificate and host name; `false` turns verification off, which lets anyone between the engine and the database impersonate it |

### `[runtime]`

| Key | Default | Meaning |
|---|---|---|
| `profiles` | `["all"]` | What this node runs: `all` or `engine` (every engine service), `frontend` (with the internal frontend), `history`, `matching`, `worker`, and `api` (the API server; the engine starts nothing for it). Profiles combine |

Nodes find each other through the shared database, so a distributed deployment is the same `runtime.toml` on every
node with a different `profiles` and its own `broadcastAddress`.

### `[engine]`

| Key | Default | Meaning |
|---|---|---|
| `bindAddress` | `127.0.0.1` | Address every service listens on |
| `broadcastAddress` | `bindAddress` | IP address other nodes and clients use to reach this node; required when `bindAddress` is `0.0.0.0`, and never `0.0.0.0` itself. `auto` picks this host's first non-loopback address (containers) |
| `historyShards` | 512 (1 for SQLite) | Temporal history shards. **Cannot be changed after the first start** |
| `logLevel` | `info` | `debug`, `info`, `warn` or `error` |
| `logFile` | standard output | Write the log to this file instead; a missing directory is created, and a file the engine cannot write stops the start |
| `[engine.ports]` `frontend`, `frontendHttp`, `internalFrontend`, `history`, `matching`, `worker` | 7233, 7243, 7236, 7234, 7235, 7239 | Service ports; each gRPC service's membership port is its port minus 300 (6933, 6936, …), at most 32767 on PostgreSQL and MySQL |
| `[engine.tls]`, `[engine.auth]` | off | TLS and identity-provider tokens; see [engine security](engine-security.md) |
| `[engine.metrics]` `prometheusAddress` | off | `host:port` serving Prometheus metrics at `/metrics` |

Other tables (`[users]`, the portals) and other `[runtime]` keys belong to other components and are ignored here.

## Behavior

- PostgreSQL maps to Temporal's `postgres12` plugin, MySQL to `mysql8` and SQLite to `sqlite`.
- The generated configuration always has Temporal's internal frontend, and `start` runs it with the default
  services: Temporal's own services (the worker, history) reach the frontend through it, which is what keeps them
  working once an authorizer is on. The internal frontend is for in-process calls only; integrations use `frontend`.
- Unknown keys in `[engine]` or `[database]` are rejected, so a typo fails at start instead of being ignored.
- The engine refuses to start on invalid settings: an unknown database type, a missing host, invalid or duplicate
  ports (membership ports included), a SQLite database with both or neither of `path` and `inMemory`, an
  in-memory SQLite database with more than one connection, the same name for both databases, TLS settings without
  TLS enabled, half a client certificate pair, TLS for a SQLite database, `serverName` for a PostgreSQL database, or a
  `$env{NAME}` whose variable is not set.
- With `[database.tls]` enabled, the server's certificate is verified unless `enableHostVerification = false`.
- The generated configuration is then checked with Temporal's own validation.
- `$secret{...}` references are rejected with a clear message until secure-vault support exists.
- For PostgreSQL and MySQL, `start` creates the databases and brings their schemas up to date before Temporal
  starts, unless `manageSchema = false` ([database schema](database-schema.md)).

## Limits

- `historyShards` cannot change after the first start.
- Both stores use the same database server and credentials; only their database names differ.
- A password is a literal or `$env{NAME}`; secure-vault references are not supported yet.
- SQLite uses one connection and is for development and tests.
- PostgreSQL verifies the database certificate against `host`: Temporal's PostgreSQL connection has no server name
  setting, so connect by the name on the certificate rather than an IP address or tunnel.

## Not covered yet

Cassandra and secure-vault secrets.

## Tests

| Test | Covers |
|---|---|
| `engine/internal/runtimeconfig/testdata/config/*.txtar` (`fixtures_test.go`) | One file per case: the input `runtime.toml` and the whole rendered Temporal configuration, or the exact error. Add a case as a file; `go test ./internal/runtimeconfig -update` writes the expected output to review |
| `tools/internal/enginetest/sqlite_test.go` | A real engine on a new SQLite file creates the schema, keeps a namespace and a waiting workflow across a restart, and closes the workflow after it |
| `engine/internal/runtimeconfig/runtimeconfig_test.go` | What a redacted rendering cannot show: resolved environment passwords, redaction without changing the configuration, reading the file |
| `engine/internal/runtimeconfig/postgresqltls_test.go` | Temporal's PostgreSQL session verifies the certificate against the host, not `serverName`; it fails once Temporal passes the name through, so the rule can go |
| `engine/internal/runtimeconfig/security_test.go` | Profiles, TLS, auth and metrics |
| `engine/internal/cli/cli_test.go` | `start --runtime-config`, the authorizer check, one configuration only, `render-config`, profiles and the claim mapper |
| `tests/ballerina/smoke` and `full`, with `--database sqlite\|postgresql\|mysql` | A real engine started from `runtime.toml` runs workflows on each database |
