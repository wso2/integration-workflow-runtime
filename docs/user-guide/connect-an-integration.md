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

- The integration has a token with `write` in that namespace, and the CA certificate that signed the engine's
  certificate. With the runtime's API keys, create one on a node
  ([Secure the engine](secure-the-engine.md#use-the-runtimes-api-keys)):

  ```bash
  bin/workflow-runtime api-key create --name orders-integration --namespace orders --role write
  ```

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

Tokens expire, and the integration reads `authApiKey` once, at start. An API key lasts a year by default (`--expires`
sets it): create a new key and restart the integration before the old one expires. A token from your identity
provider usually lasts minutes to hours, so give the integration one whose lifetime covers its run.
