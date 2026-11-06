# WSO2 Integration Workflow Runtime

Runs the durable workflows of WSO2 Integrator (`ballerina/workflow`) on an engine built from the open-source
Temporal server.

## Start

1. Create a PostgreSQL or MySQL database server and a user that may create databases (or ask a DBA to create
   `temporal` and `temporal_visibility`).
2. Edit `conf/runtime.toml`: the `[database]` table, and `[engine.tls]` and `[engine.auth]` for production.
3. Put the database password in the environment variable the file names (`WORKFLOW_DB_PASSWORD`).
4. Run `bin/workflow-runtime` (`bin\workflow-runtime.exe` on Windows). The engine creates the databases and their
   schemas, then listens on port 7233.
5. Create the namespace the integrations use, with the `WorkflowKind` search attribute `ballerina/workflow` needs,
   using the [Temporal CLI](https://docs.temporal.io/cli) and a token with the `admin` role:

   ```bash
   OPTS="--address workflow-runtime.example.com:7233 --tls-ca-path certs/engine-ca.pem --api-key $ADMIN_TOKEN"
   temporal operator namespace create --namespace default --retention 720h $OPTS
   temporal operator search-attribute create --namespace default --name WorkflowKind --type Keyword $OPTS
   ```

Without `[engine.auth]` the engine refuses to start; `bin/workflow-runtime start --allow-no-auth` is for trying out
only. `bin/workflow-runtime render-config` prints the generated configuration, password hidden.

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
| `bin/` | `workflow-runtime` (the command you run), `workflow-engine`, and `workflow-engine-sql` (schema tool for DBAs) |
| `conf/runtime.toml` | The one file to edit |
| `systemd/` | Example service unit (Linux) |
| `sbom/` | CycloneDX SBOM per binary |
| `third-party/` | Licenses and notices of everything shipped |
