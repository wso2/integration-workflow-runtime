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
| `--service <name>` (repeatable) | `frontend`, `history`, `matching`, `worker`, plus `internal-frontend` when `services.internal-frontend` is configured | `frontend`, `internal-frontend`, `history`, `matching`, `worker` |
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

## Limits

- One Temporal server version per build (v1.32.1 today); upgrades move one minor version at a time.
- The history shard count is fixed when the database is first set up and cannot change later.
- SQL persistence only; SQLite is for development and tests.

## Not covered yet

The engine endpoint, our authorizer, TLS and token configuration, and the launcher's profiles.

## Tests

| Test | Covers |
|---|---|
| `engine/internal/cli/cli_test.go` | Usage, version, flags, configuration errors, the authorizer, claim mapper and internal frontend refusals, the default services, the start path |
| `tests/ballerina/smoke` | A real engine runs workflows, activities, data events and failures |
| `tests/ballerina/full` | Retries, timers, child workflows, human tasks, execution history |
