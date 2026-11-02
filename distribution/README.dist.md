# WSO2 Integration Workflow Runtime

Runs the durable workflows of WSO2 Integrator (`ballerina/workflow`) on an engine built from the open-source
Temporal server.

## Start

1. Create a PostgreSQL or MySQL database server and a user that may create databases (or ask a DBA to create
   `temporal` and `temporal_visibility`).
2. Edit `conf/runtime.toml`: the `[database]` table, and `[engine.tls]` and `[engine.auth]` for production.
3. Put the database password in the environment variable the file names (`WORKFLOW_DB_PASSWORD`).
4. Run `bin/workflow-runtime.sh` (`bin\workflow-runtime.bat` on Windows). The engine creates the databases and their
   schemas, then listens on port 7233.

Without `[engine.auth]` the engine refuses to start; `bin/workflow-runtime.sh start --allow-no-auth` is for trying out
only. `bin/workflow-runtime.sh render-config` prints the generated configuration, password hidden.

## Connect an integration

In the integration's `Config.toml`:

```toml
[ballerina.workflow]
mode = "SELF_HOSTED"
url = "workflow-runtime.example.com:7233"
namespace = "default"
authApiKey = "<token from your identity provider>"
authCaCert = "certs/engine-ca.pem"
```

## Contents

| Path | What |
|---|---|
| `bin/` | `workflow-runtime` start script, `workflow-engine`, and `workflow-engine-sql` (schema tool for DBAs) |
| `conf/runtime.toml` | The one file to edit |
| `systemd/` | Example service unit (Linux) |
| `sbom/` | CycloneDX SBOM per binary |
| `third-party/` | Licenses and notices of everything shipped |
