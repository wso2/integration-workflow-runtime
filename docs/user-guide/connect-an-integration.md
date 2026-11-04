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
  `workflow-cli` (an `admin` token is needed when auth is on;
  [Manage the engine with workflow-cli](manage-with-workflow-cli.md)):

  ```bash
  workflow-cli operator namespace create --namespace orders \
    --address engine.example.com:7233 --tls-ca-path ca.pem --api-key-file admin.token
  ```

  The integration registers `WorkflowKind` itself when it is missing, but its first workflow can then be refused for
  a few seconds until the engine sees the new attribute.

- The integration has a token with `write` in that namespace
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
For a development engine ([Quick start](quick-start.md)) or one started with `--allow-no-auth`, leave out
`authApiKey` and `authCaCert`.
:::

Tokens expire. Give the integration a token whose lifetime covers its run, or a way to refresh it, per your
identity provider's guidance for client-credentials tokens.
