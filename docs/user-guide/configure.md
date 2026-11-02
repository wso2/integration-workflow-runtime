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

The runtime uses two databases on the same server: `temporal` (workflow state) and `temporal_visibility`
(search). Create them and their schemas once with the schema tool shipped next to the engine:

```bash
SQL="bin/workflow-engine-sql --plugin postgres12 --ep db.example.com -p 5432 -u workflow --pw <password>"
$SQL --db temporal create-database
$SQL --db temporal_visibility create-database
$SQL --db temporal setup-schema -v 0.0
$SQL --db temporal update-schema --schema-name postgresql/v12/temporal
$SQL --db temporal_visibility setup-schema -v 0.0
$SQL --db temporal_visibility update-schema --schema-name postgresql/v12/visibility
```

For MySQL use `--plugin mysql8`, port 3306 and the schema names `mysql/v8/temporal` and `mysql/v8/visibility`.

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
   broadcastAddress = "10.0.0.5"
   ```

3. For production, add `[engine.tls]` and `[engine.auth]` ([Secure the engine](secure-the-engine.md)).

:::caution
`historyShards` (default 512) cannot change after the first start. Decide it before the first start.
:::

## Check the configuration

Print the configuration the engine generates, with the password hidden:

```bash
WORKFLOW_DB_PASSWORD=<password> bin/workflow-engine render-config --runtime-config conf/runtime.toml
```

The engine checks every key before it starts. Unknown keys (usually typos), invalid ports, missing files and unset
`$env{}` variables stop it with a message naming the key.

## Start the engine

```bash
export WORKFLOW_DB_PASSWORD=<password>
bin/workflow-engine start --runtime-config conf/runtime.toml
```

Without `[engine.auth]` the engine refuses to start. Add `--allow-no-auth` only for development on your own
machine; never expose such an engine on a network.
