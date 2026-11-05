---
title: runtime.toml reference
description: Every key of the Workflow Runtime's conf/runtime.toml.
keywords: [wso2 integrator, workflow runtime, runtime.toml, configuration, reference]
---

# runtime.toml reference

Unknown keys in `[engine]` and `[database]` stop the engine at start. Tables not listed here belong to other
components and are ignored by the engine.

## `[runtime]`

| Key | Default | Description |
|---|---|---|
| `profiles` | `["all"]` | Services this node runs: `all`, `engine`, `frontend`, `history`, `matching`, `worker`; `api` is reserved for the API server. Profiles combine. See [Run across several nodes](../scale-across-nodes.md) |

## `[engine]`

| Key | Default | Description |
|---|---|---|
| `bindAddress` | `127.0.0.1` | IP address every service listens on |
| `broadcastAddress` | `bindAddress` | IP address other nodes and clients use; required when `bindAddress` is `0.0.0.0`. `auto` picks this host's first non-loopback address |
| `historyShards` | `512` (`1` for SQLite) | History shards; cannot change after the first start |
| `logLevel` | `info` | `debug`, `info`, `warn` or `error` |
| `logFile` | standard output | Write the log to this file instead; a missing directory is created, and a file the engine cannot write stops the start |

### `[engine.ports]`

| Key | Default | Description |
|---|---|---|
| `frontend` | `7233` | gRPC port integrations connect to |
| `frontendHttp` | `7243` | HTTP API port of the frontend |
| `internalFrontend` | `7236` | Frontend for the engine's own services |
| `history` | `7234` | History service |
| `matching` | `7235` | Matching service |
| `worker` | `7239` | Worker service |

Each gRPC service also uses a membership port 300 below its port (6933, 6936, 6934, 6935, 6939). On PostgreSQL and
MySQL, membership ports must be at most 32767, so service ports at most 33067.

### `[engine.tls]`

| Key | Default | Description |
|---|---|---|
| `certFile`, `keyFile` | off | Frontend certificate and key (PEM); both or neither |
| `clientCaFile` | off | Require client certificates signed by this CA |

### `[engine.tls.internode]`

| Key | Default | Description |
|---|---|---|
| `enabled` | `false` | TLS between the engine's services and nodes |
| `certFile`, `keyFile` | the frontend's | Certificate the services present to each other |
| `caFile` | required when enabled | CA that signs the internode certificates |
| `serverName` | the certificate's DNS name, when it has exactly one | Name the nodes verify in each other's certificates; required when the certificate has no DNS name or several, and must be a name the certificate carries |

### `[engine.auth]`

`type = "jwt"` needs `[engine.tls]`, and `[engine.tls.internode]` when `bindAddress` is not a loopback address.

| Key | Default | Description |
|---|---|---|
| `type` | `none` | `jwt` to require tokens; `none` needs `--allow-no-auth` |
| `jwksUrls` | discovered from `issuer` | Your identity provider's key set URLs (https; plain http only to a loopback host); set them when the provider has no OpenID Connect discovery document |
| `keyRefreshInterval` | `1m` | How often the key set is fetched again (a duration, at least `1s`) |
| `issuer` | | Required `iss`, exactly as tokens carry it; also where the key set is discovered over https (`<issuer>/.well-known/openid-configuration`). `issuer` or `jwksUrls` is required for `jwt` |
| `audience` | not checked | Required value in `aud` |
| `subjectClaim` | `sub` | Claim naming the caller |
| `rolesClaim` | `permissions` | Claim holding groups, roles or scopes; a dotted path reads a nested claim |
| `valueRegex` | | Without rules: a regex with the named groups `namespace` and `role` |
| `leewaySeconds` | `0` | Clock skew allowed on `exp`, `nbf` and `iat` |

### `[[engine.auth.rules]]`

| Key | Description |
|---|---|
| `value` | A value of the roles claim, such as a group name |
| `namespace` | A namespace, or `*` for every namespace |
| `role` | `read`, `write` or `admin` (an integration needs `write`) |

### `[engine.metrics]`

| Key | Default | Description |
|---|---|---|
| `prometheusAddress` | off | `host:port` serving Prometheus metrics at `/metrics` |

## `[database]`

| Key | Default | Description |
|---|---|---|
| `type` | required | `postgresql`, `mysql` or `sqlite` |
| `host` | required for PostgreSQL and MySQL | Database server host |
| `port` | `5432` / `3306` | Database server port |
| `user`, `password` | empty | Credentials; `password` is a literal or `$env{NAME}` |
| `name` | `temporal` | Workflow database |
| `visibilityName` | `temporal_visibility` | Visibility (search) database; must differ from `name` |
| `path` | | SQLite only: the database file |
| `inMemory` | `false` | SQLite only: keep everything in memory |
| `maxConns` | `20` (`1` for SQLite) | Connections per database; must be `1` for an in-memory SQLite database |
| `manageSchema` | `true` | Create the databases and install or upgrade their schemas on start; `false` when a DBA applies them |

### `[database.tls]`

| Key | Default | Description |
|---|---|---|
| `enabled` | `false` | TLS to the database server (PostgreSQL and MySQL; refused for SQLite) |
| `caFile` | system roots | CA that signs the server's certificate (for Amazon RDS, its certificate bundle) |
| `certFile`, `keyFile` | | Client certificate, when the server requires one |
| `serverName` | the host | MySQL only: name to verify in the server's certificate. PostgreSQL always verifies `host`, so set `host` to the name on the certificate |
| `enableHostVerification` | `true` | Verify the server's certificate and host name; `false` turns verification off |
