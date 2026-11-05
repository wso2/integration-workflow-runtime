---
title: Secure the engine
description: Turn on TLS and your identity provider's tokens, and map groups to namespace roles.
keywords: [wso2 integrator, workflow runtime, security, tls, jwt, identity provider, permissions]
---

# Secure the engine

A production engine needs two things: TLS on its frontend (port 7233), and tokens from your identity provider on
every call. Without `[engine.auth]` the engine refuses to start; `--allow-no-auth` exists for development on your
own machine only.

## The permission model

The runtime stores no users, groups or roles. Your identity provider owns them and puts them in the tokens it
issues; `conf/runtime.toml` says which group, role or scope gives which permission in which namespace.

| Role | Allows |
|---|---|
| `read` | List and describe workflows and namespaces |
| `write` | `read`, plus start, signal, cancel and terminate workflows, and poll task queues and complete tasks: what an integration (its workers and the code that starts workflows) needs |
| `admin` | Everything, including creating and changing namespaces |

There is no separate worker role: Temporal's authorizer needs `write` for a worker to poll and complete tasks, so a
rule asking for `worker` is refused at start.

A role in namespace `*` applies to every namespace. Roles add up: a caller in two groups gets both roles.

| Caller | Typical grant |
|---|---|
| An integration (its workers and the code that starts workflows) | `write` in its namespace, through a client-credentials token |
| An operator or a CI pipeline | `admin` in `*`, or in one namespace |
| Auditors and dashboards | `read` |

## Turn on TLS

1. Get a certificate and key for the address clients use (its DNS name or IP must be in the certificate).
2. Add them:

   ```toml
   [engine.tls]
   certFile = "conf/certs/engine.crt"
   keyFile = "conf/certs/engine.key"
   ```

3. To also require client certificates (mutual TLS), add `clientCaFile = "conf/certs/clients-ca.pem"`. Client
   certificates authenticate the connection; permissions still come from the token.
4. When the engine listens beyond this machine (`bindAddress` is not `127.0.0.1`), turn on mutual TLS between
   its services and nodes. It is required with `[engine.auth]`:

   ```toml
   [engine.tls.internode]
   enabled = true
   certFile = "conf/certs/node.crt"     # the frontend's certificate when left out
   keyFile = "conf/certs/node.key"
   caFile = "conf/certs/internode-ca.pem"
   ```

   Issue the internode certificates with a DNS name every node shares, such as `workflow-nodes.internal`. The nodes
   verify that name in each other's certificates. When the certificate has that one DNS name, the engine uses it; when
   it also carries the node's own name, set `serverName = "workflow-nodes.internal"`. The engine checks that the
   certificate carries the name you set. A certificate with only IP addresses needs reissuing with the shared name:
   each node's IP differs, so no one address is in every node's certificate.

   :::caution
   Only port 7233 is for integrations. The internal frontend (7236 by default) gives the engine's own services
   system access without a token, and the other service ports (7234, 7235, 7239) and membership ports (6933 to
   6939) are for the engine's nodes. Internode mutual TLS, membership included, lets only holders of a certificate
   from the internode CA reach them; also allow them only between the engine's nodes in your firewall or security
   groups.
   :::

## Turn on your identity provider's tokens

Any OpenID Connect provider works: WSO2 Identity Server, Asgardeo, Amazon Cognito, Microsoft Entra ID, Okta,
Keycloak, Auth0 and others.

1. Find your provider's issuer (the `iss` of its tokens) and the claim that carries groups, roles or scopes.
2. Add `[engine.auth]` and one rule per group. With the issuer alone, the engine reads the provider's OpenID
   Connect discovery document (`<issuer>/.well-known/openid-configuration`) at start to find its key set:

   ```toml
   [engine.auth]
   type = "jwt"
   issuer = "https://idp.example.com/oauth2/token"
   audience = "workflow-engine"        # optional
   rolesClaim = "groups"

   [[engine.auth.rules]]
   value = "workflow-admins"
   namespace = "*"
   role = "admin"

   [[engine.auth.rules]]
   value = "orders-integration"
   namespace = "orders"
   role = "write"
   ```

   `[engine.auth]` needs `[engine.tls]`: the engine refuses to accept tokens in plain text.

   :::caution
   `issuer` alone does not limit tokens to this engine: any unexpired token the provider issued, including tokens
   for its other applications, passes, and its groups grant the roles your rules map. Set `audience` to the value
   your provider puts in tokens for the engine (an API or resource identifier), or map rules only from groups that
   no other application's tokens carry. With neither `issuer` nor `audience`, the engine logs a warning at start.
   :::

   The key set is fetched over https, and so is the discovery document; the engine refuses a plain `http` issuer,
   `jwksUrls` entry or discovered `jwks_uri` unless it points at the same machine, because keys sent in plain text
   could be replaced on the way. A redirect to plain `http` is refused too, at start and on every key refresh. The
   issuer must match the discovery document's `issuer` exactly, trailing slash
   included.

3. Restart the engine.

| Provider | `rolesClaim` | Notes |
|---|---|---|
| WSO2 Identity Server | `roles` or `groups` | Issuer `https://<host>:9443/oauth2/token`. Use JWT access tokens and add the roles or groups claim to the application's access token attributes |
| Asgardeo | `roles` or `groups` | Issuer `https://api.asgardeo.io/t/<organization>/oauth2/token`. Use JWT access tokens and add the claim to the application's access token attributes |
| Amazon Cognito | `cognito:groups` | Issuer `https://cognito-idp.<region>.amazonaws.com/<pool-id>` |
| Microsoft Entra ID | `roles` (app roles) or `groups` | Issuer `https://login.microsoftonline.com/<tenant>/v2.0` |
| Okta | `groups` (add a groups claim to the authorization server) | |
| Keycloak | `realm_access.roles` | A dotted path reads a nested claim |
| Scopes from any provider | `scope` or `scp` | Space- or comma-separated strings work |
| Any other OpenID Connect provider | The claim it puts groups or roles in | Set `issuer`; the key set is discovered |

If the provider has no discovery document, or the engine cannot reach it at start, set the key set URL yourself
with `jwksUrls = ["https://idp.example.com/oauth2/jwks"]`; discovery is then skipped.

If your provider already issues values that name the namespace, skip the rules: `default:write` works as is, and
`valueRegex` (with the named groups `namespace` and `role`) parses other shapes such as `wf-default-write`.

:::info
Tokens must be signed with RS256 or ES256, and must carry an expiry (`exp`) and a subject (`sub`, or the claim
named by `subjectClaim`).
:::

## Check it

With `workflow-cli` and a token from your provider:

```bash
workflow-cli operator namespace list --address engine.example.com:7233 \
  --tls-ca-path ca.pem --api-key "$TOKEN"
```

A token that maps to no role gets `Request unauthorized`; the engine log says why (expired, wrong issuer, unknown
key, no matching group).
