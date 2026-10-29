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
| `maxConns` | 20 (1 for SQLite) | Connections per store |
| `[database.tls]` `enabled`, `caFile`, `certFile`, `keyFile`, `serverName`, `enableHostVerification` | off | TLS to the database |

### `[engine]`

| Key | Default | Meaning |
|---|---|---|
| `bindAddress` | `127.0.0.1` | Address every service listens on |
| `broadcastAddress` | `bindAddress` | Address other nodes and clients use to reach this node; required when `bindAddress` is `0.0.0.0` |
| `historyShards` | 512 (1 for SQLite) | Temporal history shards. **Cannot be changed after the first start** |
| `logLevel` | `info` | `debug`, `info`, `warn` or `error` |
| `[engine.ports]` `frontend`, `frontendHttp`, `internalFrontend`, `history`, `matching`, `worker` | 7233, 7243, 7236, 7234, 7235, 7239 | Service ports; each gRPC service's membership port is its port minus 300 (6933, 6936, …) |

Other tables (`[runtime]`, `[users]`, the portals) belong to other components and are ignored here.

## Behavior

- PostgreSQL maps to Temporal's `postgres12` plugin, MySQL to `mysql8` and SQLite to `sqlite`.
- The generated configuration always has Temporal's internal frontend, and `start` runs it with the default
  services: Temporal's own services (the worker, history) reach the frontend through it, which is what keeps them
  working once an authorizer is on. The internal frontend is for in-process calls only; integrations use `frontend`.
- Unknown keys in `[engine]` or `[database]` are rejected, so a typo fails at start instead of being ignored.
- The engine refuses to start on invalid settings: an unknown database type, a missing host, invalid or duplicate
  ports (membership ports included), a SQLite database with both or neither of `path` and `inMemory`, the same
  name for both databases, TLS files without TLS enabled, or a `$env{NAME}` whose variable is not set.
- The generated configuration is then checked with Temporal's own validation.
- `$secret{...}` references are rejected with a clear message until secure-vault support exists.
- For PostgreSQL and MySQL, the databases and their schemas must exist; for now create them with
  `workflow-engine-sql` (see `engine/README.md`). Creating them on first start is a separate feature.

## Limits

- `historyShards` cannot change after the first start.
- Both stores use the same database server and credentials; only their database names differ.
- A password is a literal or `$env{NAME}`; secure-vault references are not supported yet.
- SQLite uses one connection and is for development and tests.

## Not covered yet

Authorization and TLS for the engine's own endpoints, profiles (which services a node runs), schema creation and
upgrade on first start, Cassandra, and secure-vault secrets.

## Tests

| Test | Covers |
|---|---|
| `engine/internal/runtimeconfig/runtimeconfig_test.go` | Mapping per database type, defaults, overrides, TLS, environment passwords, every validation rule, Temporal validation, rendering |
| `engine/internal/cli/cli_test.go` | `start --runtime-config`, the authorizer check, one configuration only, `render-config` |
| `tests/ballerina/smoke` and `full`, with `--database sqlite\|postgresql\|mysql` | A real engine started from `runtime.toml` runs workflows on each database |
