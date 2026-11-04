# Engine

Status: available · since 0.1.0 (in development)

## What it does

`workflow-engine` runs Temporal's services (frontend, history, matching and worker, plus the internal frontend when
the configuration defines one) in one binary built from the pinned Temporal server module. Integrations' `ballerina/workflow` workers connect to its frontend.

## Configuration

| Command or flag | Default | Meaning |
|---|---|---|
| `workflow-engine version` | | Prints the product version and the embedded Temporal server version |
| `workflow-engine start --runtime-config <file>` | one of the two | Generate Temporal's configuration from `conf/runtime.toml` ([runtime configuration](runtime-config.md)) |
| `workflow-engine start --config-file <file>` | one of the two | Use a Temporal server configuration (YAML) as is |
| `workflow-engine render-config --runtime-config <file>` | | Print the configuration generated from `runtime.toml`, password hidden |
| `workflow-engine start-dev` | | Development engine: every service on SQLite (in memory, or `--db-file`, whose missing directories it creates), no authorization, listening on `--ip` (127.0.0.1) and `--port` (7233); creates `default` and each `--namespace`, with the `WorkflowKind` search attribute |
| `--service <name>` (repeatable) | From `runtime.toml`'s `[runtime] profiles`; with `--config-file`, `frontend`, `history`, `matching`, `worker`, plus `internal-frontend` when configured | `frontend`, `internal-frontend`, `history`, `matching`, `worker`; overrides the profiles |
| `--allow-no-auth` | off | Start without an authorizer; development and tests only |

## Behavior

- Refuses to start when the configuration sets no authorizer and `--allow-no-auth` is not given.
- Refuses to start when an authorizer is set without a claim mapper: Temporal's no-op claim mapper gives every
  caller system-admin claims, so the authorizer would admit anyone.
- Refuses to start when an authorizer is set and the configuration has no `services.internal-frontend`: with an
  authorizer on, history and worker reach the frontend only through the internal frontend.
- Exits with code 1 and a message when the configuration cannot be loaded or the server cannot start; code 2 for
  an unknown command.
- A `runtime.toml` always generates `services.internal-frontend`, so a start from it runs the internal frontend
  with the default services and passes the internal frontend check.
- Persistence plugins built in: PostgreSQL, MySQL, SQLite.
- For two minutes after start, errors caused by the services joining the membership ring or acquiring shards
  (`Not enough hosts to serve the request`, `shard status unknown`, `context canceled`, the first-start cluster
  metadata race, `Frontend is not healthy yet`) are logged at debug level; afterwards they are errors again.
- `start-dev` keeps its SQLite database open for the whole process (an in-memory database is deleted with its last
  connection) and registers the namespaces in it before the services start.

## Limits

- One Temporal server version per build (v1.32.1 today); upgrades move one minor version at a time.
- The history shard count is fixed when the database is first set up and cannot change later.
- SQL persistence only; SQLite is for development and tests.

## Not covered yet

The engine endpoint, Manager-issued tokens, and the launcher's supervision and restart. TLS, identity-provider
tokens and profiles are covered by [engine security](engine-security.md) and
[runtime configuration](runtime-config.md).

## Tests

| Test | Covers |
|---|---|
| `engine/internal/cli/cli_test.go` | Usage, version, flags, configuration errors, the authorizer, claim mapper and internal frontend refusals, the default services, the start path and `start-dev`'s `--db-file` directory |
| `engine/internal/sqlschema/devnamespaces_test.go` | `start-dev`'s namespaces and their `WorkflowKind` alias, read back from SQLite (in memory and in a file) |
| `tools/internal/enginetest/dev_test.go` | A real `start-dev` with no outside setup: `default` and `--namespace` with `WorkflowKind`, a first workflow found by `WorkflowKind`, and both kept across a restart on the same `--db-file` |
| `tests/ballerina/smoke` | A real engine runs workflows, activities, data events and failures |
| `tests/ballerina/full` | Retries, timers, child workflows, human tasks, execution history |
