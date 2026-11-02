---
title: Troubleshoot
description: Common Workflow Runtime errors and how to fix them.
keywords: [wso2 integrator, workflow runtime, troubleshooting, errors]
---

# Troubleshoot

| Message | Cause | Fix |
|---|---|---|
| `no authorizer configured; set one or pass --allow-no-auth for development` | No `[engine.auth]` | Configure your identity provider ([Secure the engine](secure-the-engine.md)), or use `--allow-no-auth` on your own machine only |
| `unknown runtime configuration keys: engine.logLvl` | A typo in `runtime.toml` | Correct the key; see the [reference](reference/runtime-toml.md) |
| `engine.logFile logs/engine.log: open logs/engine.log: permission denied` | The engine's user cannot create or write the log file | Give that user write access to the directory, or change `logFile` |
| `database.password refers to environment variable WORKFLOW_DB_PASSWORD, which is not set` | The variable is missing in the engine's environment | Export it, or set it in the service's environment file |
| `engine.broadcastAddress is required when engine.bindAddress is 0.0.0.0` | Listening on every interface without saying which address others use | Set `broadcastAddress` to this node's IP |
| `database.tls is not supported for a sqlite database` | `[database.tls]` with `type = "sqlite"` | Remove `[database.tls]`; SQLite is a local file |
| `database.tls.serverName is not supported for a postgresql database` | `serverName` with `type = "postgresql"`, which cannot use it | Remove `serverName` and set `host` to the DNS name on the database certificate |
| `engine.tls.certFile: ... no such file or directory` | A wrong path | Relative paths resolve against the runtime's directory (`workflow-runtime` changes to it) |
| `engine.ports.<service> <port>: its membership port <port> is above 32767` | Temporal's PostgreSQL and MySQL schemas store membership ports (each service port minus 300) in a 16-bit column | Use service ports at or below 33067 |
| `engine.broadcastAddress "..." must be an IP address or "auto"` | A host name as the address | Use the node's IP, or `auto` |
| `engine.broadcastAddress "auto": this host has no non-loopback address` | The container or host has no network interface yet | Attach the container to a network |
| `unable to create the claim mapper: unable to discover the identity provider's key set: ...` | The engine cannot read `<issuer>/.well-known/openid-configuration`, or it names another issuer (a trailing slash counts) | Check `issuer` (it must equal the tokens' `iss` exactly) and the network, or set `jwksUrls` |
| `engine.auth.jwksUrls: "http://..." must use https` (or the issuer, or a discovered `jwks_uri`) | A key set or discovery document over plain http to another host | Use the provider's https URL |
| `engine.metrics.prometheusAddress "..." has an invalid port` | A port that is not a number from 0 to 65535 | Correct the port |
| `engine.auth.type = "jwt" needs engine.tls` | Tokens without TLS | Add `[engine.tls]` ([Secure the engine](secure-the-engine.md)) |
| `engine.auth needs engine.tls.internode when engine.bindAddress is ...` | Auth on an engine reachable from other machines without mutual TLS between its nodes | Add `[engine.tls.internode]` ([Secure the engine](secure-the-engine.md#turn-on-tls)) |
| `rule for "...": grant write, not worker` | A rule with `role = "worker"` | Use `write`; it lets an integration's workers poll |
| Client: `Request unauthorized` | The token maps to no role, or failed a check | Read the engine log: it names the reason (expired, issuer, unknown key, no matching group); check `rolesClaim` and the rules |
| Client: `x509: certificate is not trusted` or `certificate is valid for ..., not ...` | The client does not trust the CA, or the certificate lacks the address the client uses | Give the client the CA (`authCaCert`, `--tls-ca-path`); reissue the certificate with the right DNS name or IP |
| `unable to prepare the database schema: ...` | The engine could not reach the server, create a database, or apply the schema | Check `[database]`, the network and the user's rights ([Set up the database](database.md)) |
| `unable to read DB schema version` or `database "temporal" does not exist` | `manageSchema = false` and the schema is missing or outdated | Apply it with the schema tool ([Set up the database](database.md#when-a-dba-applies-schema-changes)) |
| `database.name "..." must be letters, digits and underscores` | A name the engine cannot create on MySQL | Rename the database, or set `manageSchema = false` |
