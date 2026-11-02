---
title: Connect an integration
description: Point a ballerina/workflow integration at the Workflow Runtime.
keywords: [wso2 integrator, workflow runtime, ballerina workflow, connect, config.toml]
---

# Connect an integration

An integration that uses the `ballerina/workflow` module connects to the runtime with a few lines in its
`Config.toml`.

## Before you start

- The namespace exists, with the `WorkflowKind` search attribute `ballerina/workflow` uses. Create both once with
  the Temporal CLI (an `admin` token is needed when auth is on):

  ```bash
  OPTS="--address engine.example.com:7233 --tls-ca-path ca.pem --api-key $ADMIN_TOKEN"
  temporal operator namespace create --namespace orders --retention 720h $OPTS
  temporal operator search-attribute create --namespace orders --name WorkflowKind --type Keyword $OPTS
  ```

  The integration registers `WorkflowKind` itself when it is missing, but its first workflow can then be refused for
  a few seconds until the engine sees the new attribute.

- The integration has a token with `worker` and `write` in that namespace
  ([Secure the engine](secure-the-engine.md)), and the CA certificate that signed the engine's certificate.

## Configure the integration

```toml
[ballerina.workflow]
mode = "SELF_HOSTED"
url = "engine.example.com:7233"
namespace = "orders"
authApiKey = "<token>"
authCaCert = "certs/engine-ca.pem"
```

Start the integration. Its workers connect at start; a wrong address, an untrusted certificate or a token without
the needed roles stops them with an error naming the cause.

:::tip
For an engine started with `--allow-no-auth` on your own machine, leave out `authApiKey` and `authCaCert`.
:::

Tokens expire. Give the integration a token whose lifetime covers its run, or a way to refresh it, per your
identity provider's guidance for client-credentials tokens.
