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
- Nodes started together take turns: each takes a lock in the database it prepares (PostgreSQL advisory lock, MySQL
  `GET_LOCK`), first the workflow database and then the visibility database. A node that stops releases the lock
  with its connection.
- A waiting node tries for the lock every half second instead of blocking in the lock call. A blocked call holds a
  transaction open, and PostgreSQL's `CREATE INDEX CONCURRENTLY` in the visibility schema waits for every open
  transaction in its database, so the two nodes would deadlock.
- The lock and that database's schema changes run on one database session, which is never replaced. If the database
  ends the session, the lock and any schema change still running end with it, and every later statement fails: the
  node stops with "lost the schema lock" and cannot migrate alongside the node that takes the lock next.
- Databases are created through the server's maintenance database (`postgres` on PostgreSQL). A user that cannot
  reach it, or cannot create databases, works with databases a DBA created and needs no other database.
- A setup interrupted after creating the version tables but before recording the first version is finished on the
  next start. A version that cannot be read for another reason stops the start instead of being reset.
- If the server cannot be reached or a schema change fails, the engine stops before Temporal starts and prints the
  database and the cause.
- With `manageSchema = false`, or with `--config-file`, Temporal's own start-up check refuses a schema that is
  missing or too old. Apply it with `workflow-engine-sql` (see `engine/README.md`).
- SQLite creates its own schema in the database file; this step skips it.

## Limits

- With `manageSchema = true`, database names are letters, digits and underscores, starting with a letter or an
  underscore, 63 characters at most, and not a MySQL reserved word on MySQL: Temporal's MySQL schema tool writes
  the name unquoted.
- Only `engine/internal/sqlschema` runs SQL of its own; a `depguard` rule in `.golangci.yml` keeps it that way.
- Schema versions only move forward; going back to an older engine needs a database backup.

## Not covered yet

Refusing to skip a Temporal minor version on upgrade (Workflow Runtime 0.3), Cassandra.

## Tests

| Test | Covers |
|---|---|
| `engine/internal/sqlschema/sqlschema_test.go` | Empty, current and older databases, databases the user cannot create, no maintenance database, an interrupted setup, every failure, the lock taken around each database's schema changes on its own session, a session that ends during a schema change or part-way through |
| `engine/internal/sqlschema/sqlschema_db_test.go` | On PostgreSQL and MySQL: three nodes starting together on new databases, then a second start; an interrupted setup; databases at schema 1.0 holding a namespace, a workflow and its visibility row, upgraded with the rows kept; a role limited to its two DBA-created databases; a schema change running when the server ends its session is rolled back, the next node takes the lock, and the first node cannot run another statement on a new session. Runs when `WORKFLOW_TEST_DB_PASSWORD` is set; CI runs the whole package in the PostgreSQL smoke job |
| `engine/internal/runtimeconfig/testdata/config/*mysql-reserved*` | A MySQL reserved word as a database name, refused only when the engine manages the schema |
| `engine/internal/cli/cli_test.go` | The schema prepared before the server starts, a failure stopping the start, `manageSchema = false` and `--config-file` skipping it |
| `tests/ballerina` on PostgreSQL and MySQL | The suites start from empty databases with no schema step in the runner |
| `balsuite upgrade` (`tests/ballerina/upgrade`) on PostgreSQL and MySQL | A workflow started on Temporal 1.30.9 with its schemas (temporal 1.18, visibility 1.13) runs an activity and waits; the engine started on the same databases upgrades them to its versions, and the workflow takes its data event, runs a second activity and completes |
| `tools/internal/balsuite/upgrade_test.go` | The upgrade run's steps in order, the old server's configuration with quotes, backslashes and line breaks in its credentials, every failure, no password in Docker's arguments; `process_unix_test.go`: `bal test` and throwaway containers stopped on a timeout or a canceled run |
