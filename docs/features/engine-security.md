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
| `[engine.tls.internode]` `serverName` | the internode certificate's DNS name, when it has exactly one | Name every node verifies in the others' certificates. Temporal reaches the internal frontend through its membership resolver, which has no host name, so a name is always needed. Required when the certificate has no DNS name or several; an explicit name must be in the certificate (a DNS name, a wildcard match or an IP address) |

### `[engine.auth]`

| Key | Default | Meaning |
|---|---|---|
| `type` | `none` | `jwt` checks a bearer token on every call; `none` needs `--allow-no-auth` |
| `jwksUrls` | discovered from `issuer` | The identity provider's key set (JWKS) URLs, when it has no OpenID Connect discovery document; https, or http to a loopback host |
| `keyRefreshInterval` | `1m` | How often the key set is fetched again |
| `issuer` | | Required value of `iss`, and where the key set is discovered at start (`<issuer>/.well-known/openid-configuration`); `issuer` or `jwksUrls` is required |
| `audience` | not checked | Required value in `aud` |
| `subjectClaim` | `sub` | Claim naming the caller |
| `rolesClaim` | `permissions` | Claim holding the caller's groups, roles or scopes: a list, or a space- or comma-separated string; a dotted path reads a nested claim (`realm_access.roles`) |
| `valueRegex` | | Without rules: a regular expression with the named groups `namespace` and `role` |
| `leewaySeconds` | 0 | Clock skew allowed on `exp`, `nbf` and `iat` |
| `[[engine.auth.rules]]` `value`, `namespace`, `role` | | Grant `role` (`read`, `write`, `admin`) in `namespace` (or `*` for all) to callers whose roles claim holds `value` |

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
  workflows and lets an integration's workers poll and complete tasks; `admin` adds namespace management. There is no
  worker role: Temporal's default authorizer needs write to poll, so a `worker` rule is refused.
- `type = "jwt"` needs `[engine.tls]`, and, unless `bindAddress` is a loopback address, `[engine.tls.internode]`:
  the internal frontend gives Temporal's own services system access without a token, so it and the other services'
  ports accept only internode certificates.
- Internode TLS also turns on Temporal's `system.enableRingpopTLS` (passed as dynamic configuration at start), so the
  membership ports, which list every node's address, take only internode certificates too.
- A `--service` list without `internal-frontend` on an engine with an authorizer logs a warning: another node must
  run it.
- With neither `issuer` nor `audience`, every unexpired token the key set signs is accepted; the start logs a
  warning.
- With `issuer` and no `jwksUrls`, the start command reads the provider's OpenID Connect discovery document and uses
  its `jwks_uri`; the document must name exactly the same issuer, and the engine does not start if it cannot read it.
- Key sets and discovery documents are fetched only over https (plain http only to a loopback host): a key set
  replaced on the way would let anyone sign tokens the engine trusts. The same rule applies to every redirect, at start
  and on each refresh, so the engine reads key sets itself (`jwtclaims.KeySet`); Temporal's key provider follows any
  redirect.
- Every token is checked for its signature, an expiry, the issuer and audience when set, and a subject. A call
  without a token, or with a token that maps to no role, is refused, except Temporal's health checks.
- Refusals return Temporal's generic "Request unauthorized"; the reason goes to the engine log.
- Temporal's own services reach the frontend through the internal frontend, which is not exposed to clients.
- Integrations connect with `mode = "SELF_HOSTED"`, the token in `authApiKey` and the CA in `authCaCert`
  (`ballerina/workflow`).

## Limits

- Tokens must be signed with RS256 or ES256, as with Temporal's key provider.
- Clients present the token themselves; refreshing an expiring token is the client's job.
- Client certificates authenticate the connection only; roles still come from the token.

## Not covered yet

Roles from client certificate subjects, tokens issued by the Manager, and task-queue-scoped tokens.

## Tests

| Test | Covers |
|---|---|
| `engine/plugins/jwtclaims/discovery_test.go` | Discovery: the key set URL, another issuer or a trailing slash, no key set, a plain-http issuer or key set, invalid documents, unreachable providers; which URLs `CheckURL` accepts |
| `engine/plugins/jwtclaims/keyset_test.go` | Reading RS256 and ES256 keys, keeping them when a refresh fails, refreshing on the interval; a redirect to plain http refused for key sets and discovery, one to https followed |
| `engine/plugins/jwtclaims/jwtclaims_test.go` | Temporal's format, rules on nested claims, scope strings, regex, issuer, audience, expiry, forged and malformed tokens |
| `engine/internal/runtimeconfig/security_test.go` | TLS, auth and metrics mapping and every validation rule |
| `engine/internal/cli/cli_test.go` | A start with identity-provider auth; which claim mapper is built; the services the profiles and `--service` pass to the server, including `--service` with an `api`-only profile |
| `tools/internal/enginetest/security_test.go` | A real engine with TLS, internode mutual TLS and an OpenID Connect provider: an integration token starts and lists in its namespace; another namespace, a reader writing, no token, expired, forged, other-issuer and unmatched tokens, and plaintext are refused; the internal frontend refuses callers without an internode certificate and answers one with it; adding a search attribute works, which goes through the internal frontend with the derived server name; history, matching, worker and every membership port take TLS with a node certificate and refuse callers without one |
