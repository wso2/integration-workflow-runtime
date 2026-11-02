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
| `database.password refers to environment variable WORKFLOW_DB_PASSWORD, which is not set` | The variable is missing in the engine's environment | Export it, or set it in the service's environment file |
| `engine.broadcastAddress is required when engine.bindAddress is 0.0.0.0` | Listening on every interface without saying which address others use | Set `broadcastAddress` to this node's IP |
| `engine.tls.certFile: ... no such file or directory` | A wrong path | Paths are relative to the directory the engine starts in |
| `unable to create the claim mapper: unable to discover the identity provider's key set: ...` | The engine cannot read `<issuer>/.well-known/openid-configuration`, or it names another issuer | Check `issuer` (it must equal the tokens' `iss`) and the network, or set `jwksUrls` |
| Client: `Request unauthorized` | The token maps to no role, or failed a check | Read the engine log: it names the reason (expired, issuer, unknown key, no matching group); check `rolesClaim` and the rules |
| Client: `x509: certificate is not trusted` or `certificate is valid for ..., not ...` | The client does not trust the CA, or the certificate lacks the address the client uses | Give the client the CA (`authCaCert`, `--tls-ca-path`); reissue the certificate with the right DNS name or IP |
| `unable to read DB schema version` or `database "temporal" does not exist` | The databases or schemas are missing | Create them ([Configure the runtime](configure.md#choose-a-database)) |
