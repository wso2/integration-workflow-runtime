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
| `write` | `read`, plus start, signal, cancel and terminate workflows |
| `worker` | Poll task queues and complete tasks: what an integration's workers need |
| `admin` | Everything, including creating and changing namespaces |

A role in namespace `*` applies to every namespace. Roles add up: a caller in two groups gets both roles.

| Caller | Typical grant |
|---|---|
| An integration (its workers and the code that starts workflows) | `worker` and `write` in its namespace, through a client-credentials token |
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
4. On several nodes, encrypt traffic between them too:

   ```toml
   [engine.tls.internode]
   enabled = true
   caFile = "conf/certs/internode-ca.pem"
   ```

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
   role = "worker"

   [[engine.auth.rules]]
   value = "orders-integration"
   namespace = "orders"
   role = "write"
   ```

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

With the Temporal CLI and a token from your provider:

```bash
temporal operator namespace list --address engine.example.com:7233 \
  --tls-ca-path ca.pem --api-key "$TOKEN"
```

A token that maps to no role gets `Request unauthorized`; the engine log says why (expired, wrong issuer, unknown
key, no matching group).
