---
title: Configure the runtime
description: Configure the Workflow Runtime's database and engine in conf/runtime.toml, and start it.
keywords: [wso2 integrator, workflow runtime, configuration, runtime.toml, postgresql, mysql]
---

# Configure the runtime

All settings live in one file, `conf/runtime.toml`. The engine reads it at start and builds the Temporal server
configuration in memory, so passwords never reach the disk. Every key is listed in the
[runtime.toml reference](reference/runtime-toml.md).

## Choose a database

| Database | Use |
|---|---|
| PostgreSQL 12 or later | Production |
| MySQL 8.0.17 or later | Production |
| SQLite (a file, or memory) | Trying out and development only: one connection, one node |

The runtime uses two databases on one server and creates them, and their schemas, on start. See
[Set up the database](database.md) for the user's rights, Amazon RDS and Aurora, and installations where a DBA
applies schema changes.

## Write the configuration

1. Set the `[database]` table:

   ```toml
   [database]
   type = "postgresql"
   host = "db.example.com"
   user = "workflow"
   password = "$env{WORKFLOW_DB_PASSWORD}"
   ```

   `$env{NAME}` reads the password from an environment variable at start. Use `[database.tls]` when the server
   requires TLS.

2. Leave `[engine]` at its defaults on a single machine. To accept connections from other machines, listen on
   every interface and name the address clients use:

   ```toml
   [engine]
   bindAddress = "0.0.0.0"
   broadcastAddress = "10.0.0.5"   # or "auto" to use this host's address
   ```

3. For production, add `[engine.tls]` and `[engine.auth]` ([Secure the engine](secure-the-engine.md)).

:::caution
`historyShards` (default 512; 1 for SQLite) cannot change after the first start. Decide it before the first start.
:::

## Check the configuration

Print the configuration the engine generates, with the password hidden:

```bash
WORKFLOW_DB_PASSWORD=<password> bin/workflow-runtime render-config
```

The engine checks every key before it starts. Unknown keys (usually typos), invalid ports, missing files and unset
`$env{}` variables stop it with a message naming the key.

## Start the engine

```bash
export WORKFLOW_DB_PASSWORD=<password>
bin/workflow-runtime
```

`workflow-runtime` works from the runtime's directory, so relative paths in `runtime.toml` resolve against it. See
[Install the runtime](install.md) for the commands and for running it as a service.

Without `[engine.auth]` the engine refuses to start. Add `--allow-no-auth` only for development on your own
machine; never expose such an engine on a network.
