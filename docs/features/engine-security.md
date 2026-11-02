# Engine security

Status: available · since 0.1.0 (in development)

## What it does

Secures the engine's frontend (port 7233), the endpoint integrations connect to, without the Manager: TLS on the
connection, and tokens from the customer's own identity provider mapped to Temporal's namespace roles.

## Configuration

### `[engine.tls]`

| Key | Default | Meaning |
|---|---|---|
| `certFile`, `keyFile` | off | The frontend's certificate and key (PEM). Clients connect with TLS and trust its CA |
| `clientCaFile` | off | Require client certificates signed by this CA (mutual TLS) |
| `[engine.tls.internode]` `enabled` | `false` | TLS between the engine's services and nodes |
| `[engine.tls.internode]` `certFile`, `keyFile` | the frontend's | Certificate the services present to each other |
| `[engine.tls.internode]` `caFile` | required when enabled | CA that signs the internode certificates |
| `[engine.tls.internode]` `serverName` | | Name to verify in internode certificates, when it differs from the host |

### `[engine.auth]`

| Key | Default | Meaning |
|---|---|---|
| `type` | `none` | `jwt` checks a bearer token on every call; `none` needs `--allow-no-auth` |
| `jwksUrls` | required for `jwt` | The identity provider's key set (JWKS) URLs |
| `keyRefreshInterval` | `1m` | How often the key set is fetched again |
| `issuer` | not checked | Required value of `iss` |
| `audience` | not checked | Required value in `aud` |
| `subjectClaim` | `sub` | Claim naming the caller |
| `rolesClaim` | `permissions` | Claim holding the caller's groups, roles or scopes: a list, or a space- or comma-separated string; a dotted path reads a nested claim (`realm_access.roles`) |
| `valueRegex` | | Without rules: a regular expression with the named groups `namespace` and `role` |
| `leewaySeconds` | 0 | Clock skew allowed on `exp`, `nbf` and `iat` |
| `[[engine.auth.rules]]` `value`, `namespace`, `role` | | Grant `role` (`read`, `write`, `worker`, `admin`) in `namespace` (or `*` for all) to callers whose roles claim holds `value` |

```toml
[engine.tls]
certFile = "conf/certs/engine.crt"
keyFile = "conf/certs/engine.key"

[engine.auth]
type = "jwt"
jwksUrls = ["https://cognito-idp.us-east-1.amazonaws.com/<pool>/.well-known/jwks.json"]
issuer = "https://cognito-idp.us-east-1.amazonaws.com/<pool>"
rolesClaim = "cognito:groups"

[[engine.auth.rules]]
value = "workflow-admins"
namespace = "*"
role = "admin"

[[engine.auth.rules]]
value = "orders-integration"
namespace = "default"
role = "write"
```

## Behavior

- **Three ways to read roles**, so any identity provider fits:
  - Rules map the provider's group, role or scope names to namespace roles. Use them when the provider knows
    nothing about namespaces, which is the usual case.
  - Without rules, each value is Temporal's own `<namespace>:<role>` (for example `default:write`).
  - `valueRegex` parses values in any other shape that carries both, such as `wf-default-write`.
- A role in namespace `*` (or Temporal's `temporal-system`) applies to every namespace and to cluster operations.
- Roles add up across values. `read` lets a caller list and describe; `write` adds starting and signalling
  workflows; `worker` lets an integration's worker poll; `admin` adds namespace management.
- Every token is checked for its signature, an expiry, the issuer and audience when set, and a subject. A call
  without a token, or with a token that maps to no role, is refused, except Temporal's health checks.
- Refusals return Temporal's generic "Request unauthorized"; the reason goes to the engine log.
- Temporal's own services reach the frontend through the internal frontend, which is not exposed to clients.
- Integrations connect with `mode = "SELF_HOSTED"`, the token in `authApiKey` and the CA in `authCaCert`
  (`ballerina/workflow`).

## Limits

- Tokens must be signed with RS256 or ES256, which Temporal's key provider supports.
- Clients present the token themselves; refreshing an expiring token is the client's job.
- Client certificates authenticate the connection only; roles still come from the token.

## Not covered yet

Roles from client certificate subjects, tokens issued by the Manager, and task-queue-scoped tokens.

## Tests

| Test | Covers |
|---|---|
| `engine/plugins/jwtclaims/jwtclaims_test.go` | Temporal's format, rules on nested claims, scope strings, regex, issuer, audience, expiry, forged and malformed tokens |
| `engine/internal/runtimeconfig/security_test.go` | TLS, auth and metrics mapping and every validation rule |
| `engine/internal/cli/cli_test.go` | A start with identity-provider auth; which claim mapper is built |
