---
title: Quick start
description: Run a development Workflow Runtime on your machine and connect an integration in minutes.
keywords: [wso2 integrator, workflow runtime, quick start, development, dev mode, sqlite]
---

# Quick start

Development mode runs the whole engine on your machine with no database server, no TLS and no authentication, and
creates the `default` namespace, the way `temporal server start-dev` does.

:::caution
Development mode is for one developer's machine. It listens on `127.0.0.1` and accepts every caller. Use the
production setup ([Install the runtime](install.md)) for anything shared.
:::

## Start it

1. Extract the runtime's zip and change to its directory.
2. Start development mode:

   ```bash
   bin/workflow-runtime dev          # bin\workflow-runtime.exe dev on Windows
   ```

   It prints `Development engine on 127.0.0.1:7233 (namespaces default; data in memory; no TLS or authorization)`
   and listens on port 7233. The data lives in memory and is lost when you stop it (Ctrl+C).

| Option | Default | Does |
|---|---|---|
| `--db-file <file>` | in memory | Keeps the data in a SQLite file across restarts |
| `--namespace <name>` | | Creates another namespace besides `default`; repeat for more |
| `--port <port>` | `7233` | Frontend port; the engine also uses the next ten ports |
| `--ip <address>` | `127.0.0.1` | Address to listen on |
| `--log-level <level>` | `warn` | `debug`, `info`, `warn` or `error` |

For example, to keep the data and add an `orders` namespace:

```bash
bin/workflow-runtime dev --db-file data/dev.db --namespace orders
```

## Connect an integration

In the integration's `Config.toml`:

```toml
[ballerina.workflow]
mode = "SELF_HOSTED"
url = "localhost:7233"
namespace = "default"
```

Run the integration; its workflows run on your development engine. The namespaces come with the `WorkflowKind`
search attribute `ballerina/workflow` uses, so the first workflow starts without waiting.

## Look at workflows

```bash
temporal workflow list --address localhost:7233 --namespace default
```

## Next steps

- [Install the runtime](install.md) for a shared or production setup.
- [Secure the engine](secure-the-engine.md) before others connect.
