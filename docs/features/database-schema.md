# Database schema

Status: available · since 0.1.0 (in development)

## What it does

When the engine starts from `conf/runtime.toml` on PostgreSQL or MySQL, it prepares Temporal's two databases before
Temporal's services start: it creates them when they do not exist, installs Temporal's schema in an empty database
and applies the schema versions shipped in the binary. A new installation and an upgrade need no separate step.

## Configuration

| Setting | Default | Meaning |
|---|---|---|
| `[database] manageSchema` | `true` | `false` leaves the databases alone, for installations where a DBA applies schema changes |

The databases are the ones `[database]` names (`name`, `visibilityName`); see
[runtime configuration](runtime-config.md).

## Behavior

- Runs on every `workflow-engine start --runtime-config`; a database that is already current is left unchanged.
- Uses Temporal's own schema tool and the schema files embedded in the binary, the same ones `workflow-engine-sql`
  uses, so the engine and its schema always come from one Temporal version.
- Nodes started together take turns: each takes a lock on the database server (PostgreSQL advisory lock, MySQL
  `GET_LOCK`) while it prepares the schema. A node that stops releases the lock with its connection.
- The user needs the right to create databases only when a database is missing. A user without it works with
  databases a DBA created.
- If the server cannot be reached or a schema change fails, the engine stops before Temporal starts and prints the
  database and the cause.
- With `manageSchema = false`, or with `--config-file`, Temporal's own start-up check refuses a schema that is
  missing or too old. Apply it with `workflow-engine-sql` (see `engine/README.md`).
- SQLite creates its own schema in the database file; this step skips it.

## Limits

- With `manageSchema = true`, database names are letters, digits and underscores, starting with a letter or an
  underscore, 63 characters at most: MySQL's `CREATE DATABASE` takes the name unquoted.
- Schema versions only move forward; going back to an older engine needs a database backup.

## Not covered yet

Refusing to skip a Temporal minor version on upgrade (Workflow Runtime 0.3), Cassandra.

## Tests

| Test | Covers |
|---|---|
| `engine/internal/sqlschema/sqlschema_test.go` | Empty, current and older databases, databases the user cannot create, every failure, the lock statements per database |
| `engine/internal/sqlschema/sqlschema_db_test.go` | Three nodes starting together on new PostgreSQL and MySQL databases, then a second start; runs when `WORKFLOW_TEST_DB_PASSWORD` is set |
| `engine/internal/cli/cli_test.go` | The schema prepared before the server starts, a failure stopping the start, `manageSchema = false` and `--config-file` skipping it |
| `tests/ballerina` on PostgreSQL and MySQL | The suites start from empty databases with no schema step in the runner |
