# Engine security

Status: available · since 0.1.0 (in development)

## What it does

Secures the engine's frontend (port 7233), the endpoint integrations connect to, without the Manager: TLS on the
connection, and on every call either one of the runtime's own API keys or a token from the customer's identity
provider, mapped to Temporal's namespace roles.

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
| `type` | `none` | `apiKeys` checks the runtime's own API keys; `jwt` checks the identity provider's tokens, and API keys too when `[engine.auth.apiKeys]` is set; `none` needs `--allow-no-auth` |
| `[engine.auth.apiKeys]` `signingKey` | | ECDSA P-256 key (PEM) that signs and verifies API keys, created by the first `api-key create`; the same file on every node, readable by its owner (and group) only; Linux and macOS starts refuse group write and any access by others |
| `[engine.auth.apiKeys]` `revocationList` | `revoked-api-keys.txt` beside `signingKey` | Revoked key IDs, one per line, read again while the engine runs |
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

## Commands

| Command | Does |
|---|---|
| `workflow-engine api-key create --runtime-config <file> --name <name> --namespace <ns>... --role read\|write\|admin [--expires 365d]` | Signs a key and prints it once on standard output; records it, without the token, in `issued-api-keys.txt` beside the revocation list. `--name` has at most 200 characters and no control characters; at most 100 namespaces of up to 255 characters; `--expires` up to about 292 years (what a Go duration holds) |
| `workflow-engine api-key list --runtime-config <file>` | The keys created: ID, name, grants, dates, and whether each is active, expired or revoked |
| `workflow-engine api-key revoke --runtime-config <file> (--id <id> [--force] \| --name <name>) [--reason <text>]` | Adds the key, or every key with that name, to the revocation list. `--id` must be a 32-character key ID in the issued-key record, or `--force` for a key created on another node |

`workflow-runtime api-key ...` runs the same with the distribution's `conf/runtime.toml`.

## Behavior

- **API keys** are JWTs the runtime signs with ES256: issuer `wso2-workflow-runtime`, the key's name as `sub`, its
  ID as `jti`, the signing key's fingerprint as `kid`, an expiry (a year by default), and the roles as Temporal's
  `<namespace>:<role>` values in `permissions`. A token with that issuer is checked against the signing key and the
  revocation list; any other token goes to the identity provider's mapper, or is refused when the engine trusts only
  API keys. A key signed by another runtime's key is refused by its `kid`.
- The revocation list is checked for changes at most every 10 seconds, so a revocation reaches a running engine
  without a restart. The issued-key record holds no tokens: a leaked record grants nothing.
- The first `api-key create` writes the signing key when the file is missing and an empty revocation list, with mode
  0600; on Windows each file the runtime creates gets a protected access list for its creator and SYSTEM. A start
  never creates the key, since a node with a key of its own would refuse the keys the others accept: it stops with an
  error when the key is missing and, on Linux and macOS, when its group can write it or other users can use it. Windows
  has no mode bits, so a key copied in by hand is the operator's to restrict.
- A revocation list missing at start is logged once as a warning. A list that was read and then disappears makes
  every API key fail, with a warning at each check, until it is back: a volume that did not mount must not bring
  revoked keys back. A list other users can write is refused, since they could un-revoke keys.
- The `api-key` commands read only `[engine.auth]` from `runtime.toml`, so they run from an operator's shell without
  the service's database password.
- **Three ways to read roles**, so any identity provider fits:
  - Rules map the provider's group, role or scope names to namespace roles. Use them when the provider knows
    nothing about namespaces, which is the usual case.
  - Without rules, each value is Temporal's own `<namespace>:<role>` (for example `default:write`).
  - `valueRegex` parses values in any other shape that carries both, such as `wf-default-write`.
- A role in namespace `*` (or Temporal's `temporal-system`) applies to every namespace and to cluster operations.
- Roles add up across values. `read` lets a caller list and describe; `write` adds starting and signalling
  workflows and lets an integration's workers poll and complete tasks; `admin` adds namespace management. There is no
  worker role: Temporal's default authorizer needs write to poll, so a `worker` rule is refused.
- `type = "jwt"` or `"apiKeys"` needs `[engine.tls]`, and, unless `bindAddress` is a loopback address, `[engine.tls.internode]`:
  the internal frontend gives Temporal's own services system access without a token, so it and the other services'
  ports accept only internode certificates.
- Internode TLS also turns on Temporal's `system.enableRingpopTLS` (passed as dynamic configuration at start), so the
  membership ports, which list every node's address, take only internode certificates too.
- A `--service` list without `internal-frontend` on an engine with an authorizer logs a warning: another node must
  run it.
- With neither `issuer` nor `audience`, those two claims are not checked; the start logs a warning. A token still
  needs a valid signature from the key set, an expiry, a subject and a mapped role.
- With `issuer` and no `jwksUrls`, the start command reads the provider's OpenID Connect discovery document and uses
  its `jwks_uri`; the document must name exactly the same issuer, and the engine does not start if it cannot read it.
- Key sets and discovery documents are fetched only over https (plain http only to a loopback host): a key set
  replaced on the way would let anyone sign tokens the engine trusts. The same rule applies to every redirect, at start
  and on each refresh, so the engine reads key sets itself (`jwtclaims.KeySet`); Temporal's key provider follows any
  redirect.
- Each discovery document and key set read is limited to 1 MiB, and a redirect never goes from https to http. Only
  RS256 and ES256 keys are kept, and only when their `use`, `key_ops` and `alg`, if published, allow verifying that
  algorithm's signatures; an invalid entry never replaces a valid key with the same `kid`. A response without a `keys` member (an error page served with status 200) is refused, and the previous keys stay.
- Every token is checked for its signature, an expiry, the issuer and audience when set, and a subject; one issued in
  the future (`iat` beyond `leewaySeconds`) is refused. A call without a token, or with a token that maps to no role,
  is refused, except Temporal's health checks.
- Refusals return Temporal's generic "Request unauthorized"; the reason goes to the engine log.
- Temporal's own services reach the frontend through the internal frontend, which is not exposed to clients.
- Integrations connect with `mode = "SELF_HOSTED"`, the token in `authApiKey` and the CA in `authCaCert`
  (`ballerina/workflow`).

## Limits

- Tokens must be signed with RS256 or ES256, as with Temporal's key provider.
- Clients present the token themselves; refreshing an expiring token is the client's job. `ballerina/workflow` reads
  `authApiKey` once, so an integration needs a key that outlives its run: an API key, or a long-lived provider token.
- API keys, the revocation list and the issued-key record are files on each node; with several nodes the operator
  copies them. Revoking needs a node's shell, not a remote call.
- Client certificates authenticate the connection only; roles still come from the token.

## Not covered yet

Roles from client certificate subjects, keys created through an API (the Manager's), and task-queue-scoped keys.

## Tests

| Test | Covers |
|---|---|
| `engine/plugins/jwtclaims/discovery_test.go` | Discovery: the key set URL, another issuer or a trailing slash, no key set, a plain-http issuer or key set, invalid documents, unreachable providers; which URLs `CheckURL` accepts |
| `engine/plugins/jwtclaims/keyset_test.go` | Reading RS256 and ES256 keys, keeping them when a refresh fails, refreshing on the interval; a redirect to plain http refused for key sets and discovery, from https to loopback http refused, one to https followed; keys for encryption or another algorithm dropped, a bad entry not replacing a good key with the same ID; oversized key sets and sets without `keys` refused |
| `engine/plugins/jwtclaims/jwtclaims_test.go` | Temporal's format, rules on nested claims, scope strings, regex, issuer, audience, expiry, tokens issued in the future, forged and malformed tokens |
| `engine/plugins/apikeys/apikeys_test.go` | Signing keys created once (mode 0600) and read back as SEC 1 or PKCS#8, other curves and kinds refused; keys created and verified with their roles, `*` as system roles, expiry; keys from another signing key, forged and ID-less keys refused; a revocation reaching a running verifier, and a removed list refusing every key until it is back; key files its group can write or others can use, and lists others can write, refused; revocations of malformed IDs or of the key itself refused; the issued-key record, which holds no token; the mapper routing API keys, provider tokens and calls without a token |
| `engine/plugins/apikeys/lock_windows_test.go` (the `windows-latest` CI job) | The signing key, revocation list and issued-key record get a protected access list for the current user and SYSTEM only |
| `engine/internal/runtimeconfig/security_test.go` | TLS, auth and metrics mapping and every validation rule, including `apiKeys` alone and beside an identity provider |
| `engine/internal/cli/apikey_test.go` | `api-key create`, `list` and `revoke` (by name, by ID, and `--force` for an ID from another node) end to end with the claim mapper the start builds; every refusal; lifetimes; a start with both an identity provider and API keys |
| `engine/internal/cli/cli_test.go` | A start with identity-provider auth; which claim mapper is built; the services the profiles and `--service` pass to the server, including `--service` with an `api`-only profile |
| `tools/internal/enginetest/cluster_test.go` | Two secured nodes on one SQLite file, one with frontend, matching and worker and one with frontend and history, form one engine: a workflow started through the first runs on the second's history, over TLS membership and internode TLS |
| `tools/internal/balsuite/apikeys_test.go` and the `smoke suite on apikeys` CI job | The Ballerina smoke suite against an engine with TLS and only API keys: `ballerina/workflow` connects with `SELF_HOSTED`, `authApiKey` and `authCaCert`, as in production without an identity provider |
| `tools/internal/enginetest/apikeys_test.go` | A real engine with TLS and only API keys: keys from `api-key create` grant admin, write and read to the Temporal CLI and `workflow-cli`; a reader writing, another namespace, an integration key creating a namespace, a key from another runtime and no key are refused; a key revoked while the engine runs stops working within seconds and the others keep working |
| `tools/internal/enginetest/security_test.go` | A real engine with TLS, internode mutual TLS and an OpenID Connect provider: an integration token starts and lists in its namespace; another namespace, a reader writing, no token, expired, forged, other-issuer and unmatched tokens, and plaintext are refused; the internal frontend refuses callers without an internode certificate and answers one with it; adding a search attribute works, which goes through the internal frontend with the derived server name; history, matching, worker and every membership port take TLS with a node certificate and refuse callers without one |
