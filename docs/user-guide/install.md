---
title: Install the runtime
description: Install the Workflow Runtime from its zip on Linux, macOS or Windows, and run it as a service.
keywords: [wso2 integrator, workflow runtime, install, zip, systemd, linux, windows]
---

# Install the runtime

The runtime ships as a zip per operating system and architecture (Linux, macOS and Windows on amd64 and arm64).
The engine is a single static binary: no Java or other runtime is needed.

:::info Prerequisites
- A PostgreSQL or MySQL server for production ([Set up the database](database.md)).
- Your identity provider's key set URL and the groups to grant ([Secure the engine](secure-the-engine.md)).
- A certificate and key for the engine's address, for TLS.
:::

## Install

1. Download `wso2-workflow-runtime-<version>-<os>-<arch>.zip` and extract it.

   ```bash
   unzip wso2-workflow-runtime-<version>-linux-amd64.zip
   cd wso2-workflow-runtime-<version>-linux-amd64
   ```

2. Edit `conf/runtime.toml` ([Configure the runtime](configure.md)). Relative paths in it, such as
   `conf/certs/engine.crt`, resolve against this directory.
3. Put the database password in the environment variable the file names:

   ```bash
   export WORKFLOW_DB_PASSWORD=<password>
   ```

4. Start the runtime:

   ```bash
   bin/workflow-runtime          # bin\workflow-runtime.exe on Windows
   ```

   The engine creates its databases on the first start, then listens on port 7233.

5. Create the namespace your integrations use, with the `WorkflowKind` search attribute
   ([Connect an integration](connect-an-integration.md#before-you-start)). The engine does not create it.

| Command | Does |
|---|---|
| `bin/workflow-runtime` or `bin/workflow-runtime start` | Starts the engine from `conf/runtime.toml` |
| `bin/workflow-runtime start --allow-no-auth` | Starts without `[engine.auth]`; development on your own machine only |
| `bin/workflow-runtime dev` | Development mode: in-memory SQLite, no authentication, the `default` namespace ([Quick start](quick-start.md)) |
| `bin/workflow-runtime render-config` | Prints the generated configuration, password hidden |
| `bin/workflow-runtime version` | Prints the runtime's and the embedded Temporal server's versions |
| `bin/workflow-cli ...` | The client: health checks, the cluster's nodes, namespaces and search attributes ([Manage the engine with workflow-cli](manage-with-workflow-cli.md)) |

Set `WORKFLOW_RUNTIME_CONFIG` to use another configuration file.

## Run as a service on Linux

The Linux zip includes `systemd/workflow-runtime.service`, written for `/opt/wso2-workflow-runtime` and a
`workflow` user.

1. Extract the zip so that `bin/`, `conf/` and `systemd/` are directly under `/opt/wso2-workflow-runtime`, and
   create the user:

   ```bash
   sudo unzip wso2-workflow-runtime-<version>-linux-amd64.zip -d /opt
   sudo mv /opt/wso2-workflow-runtime-<version>-linux-amd64 /opt/wso2-workflow-runtime
   sudo useradd --system --home /opt/wso2-workflow-runtime workflow
   sudo chown -R workflow:workflow /opt/wso2-workflow-runtime
   ```

2. Put secrets in `/etc/wso2-workflow-runtime/env`, readable by root only:

   ```bash
   sudo mkdir -p /etc/wso2-workflow-runtime
   echo 'WORKFLOW_DB_PASSWORD=<password>' | sudo tee /etc/wso2-workflow-runtime/env
   sudo chmod 600 /etc/wso2-workflow-runtime/env
   ```

3. Install and start the unit:

   ```bash
   sudo cp systemd/workflow-runtime.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now workflow-runtime
   journalctl -u workflow-runtime -f
   ```

## Files

| Path | What |
|---|---|
| `bin/` | `workflow-runtime` (the command you run), `workflow-engine`, `workflow-engine-sql` (schema tool for DBAs), and `workflow-cli` (the client) |
| `conf/runtime.toml` | The configuration |
| `systemd/` | Example service unit (Linux) |
| `sbom/` | CycloneDX software bill of materials per binary |
| `third-party/` | Licenses and notices of everything shipped |
