---
title: Set up the database
description: Prepare PostgreSQL or MySQL for the Workflow Runtime, including Amazon RDS and Aurora.
keywords: [wso2 integrator, workflow runtime, database, postgresql, mysql, amazon rds, aurora, schema]
---

# Set up the database

The runtime keeps workflow state in two databases on one server: `temporal` (workflow state and history) and
`temporal_visibility` (workflow search). On start, the engine creates both when they are missing and installs or
upgrades their schemas, so a new installation and an upgrade need no separate step.

## Supported databases

| Database | Versions | Use |
|---|---|---|
| PostgreSQL | 12 or later | Production |
| MySQL | 8.0.17 or later | Production |
| Amazon RDS for PostgreSQL or MySQL, Amazon Aurora PostgreSQL, Aurora MySQL 3 | As above | Production on AWS |
| SQLite | Bundled | Trying out and development only (one node, one connection) |

## Prepare the server

1. Create a user for the runtime.
2. Either let the runtime create the databases, or create `temporal` and `temporal_visibility` yourself and give
   the user full rights on both.

   - **The runtime creates them:** the user needs the right to create databases, and on PostgreSQL the right to
     connect to the `postgres` maintenance database, where the engine runs `CREATE DATABASE`.
   - **You create them:** the user needs nothing outside the two databases. On PostgreSQL make it their owner (or
     grant `CONNECT` and `CREATE` on them and on their `public` schema); the engine skips creation when it cannot
     reach the maintenance database.

   ```sql
   -- PostgreSQL: the runtime creates the databases
   CREATE USER workflow WITH PASSWORD '<password>' CREATEDB;
   -- PostgreSQL: you create them
   CREATE USER workflow WITH PASSWORD '<password>';
   CREATE DATABASE temporal OWNER workflow;
   CREATE DATABASE temporal_visibility OWNER workflow;
   -- MySQL (either way; with these grants alone, create the databases yourself)
   CREATE USER 'workflow'@'%' IDENTIFIED BY '<password>';
   GRANT ALL PRIVILEGES ON temporal.* TO 'workflow'@'%';
   GRANT ALL PRIVILEGES ON temporal_visibility.* TO 'workflow'@'%';
   ```

   On MySQL, database names cannot be reserved words such as `order` (8.4's, and 8.0's such as `member`) when the engine manages the schema, because
   Temporal's schema tool writes them unquoted.

3. Configure `[database]` in `conf/runtime.toml` ([Configure the runtime](configure.md)).

On the first start the engine logs `Setting up Temporal's schema versioning` for each database. Later starts leave
current databases alone and apply only the schema versions a newer engine brings. A start interrupted while
setting up versioning is finished on the next start; nodes that start together take turns through a lock held
in the workflow database. Workflows that are running when you upgrade continue on the new engine; every pull
request checks this with a workflow started on Temporal 1.30.9, the release before the engine's schema change.

## Amazon RDS and Aurora

RDS and Aurora are managed PostgreSQL and MySQL; configure them like any other server, with TLS:

1. Download the RDS certificate bundle for your region from AWS.
2. Add it:

   ```toml
   [database]
   type = "postgresql"
   host = "<instance>.<id>.<region>.rds.amazonaws.com"
   user = "workflow"
   password = "$env{WORKFLOW_DB_PASSWORD}"

   [database.tls]
   enabled = true
   caFile = "conf/certs/rds-global-bundle.pem"
   enableHostVerification = true
   ```

3. Allow the engine's nodes in the database's security group.

## When a DBA applies schema changes

If the runtime's user may not change schemas, turn schema management off and apply the schemas with the schema tool
shipped next to the engine, before the first start and after every upgrade:

```toml
[database]
manageSchema = false
```

```bash
export SQL_PASSWORD='<password>'   # read by the tool, so the password stays out of the command line
sql() { bin/workflow-engine-sql --plugin postgres12 --ep db.example.com -p 5432 -u dba "$@"; }
sql --db temporal create-database
sql --db temporal_visibility create-database
sql --db temporal setup-schema -v 0.0
sql --db temporal update-schema --schema-name postgresql/v12/temporal
sql --db temporal_visibility setup-schema -v 0.0
sql --db temporal_visibility update-schema --schema-name postgresql/v12/visibility
```

For MySQL use `--plugin mysql8`, port 3306 and the schema names `mysql/v8/temporal` and `mysql/v8/visibility`.
With `manageSchema = false`, the engine refuses to start on a missing or outdated schema.

:::info
With schema management on, database names may use letters, digits and underscores only, starting with a letter or
an underscore.
:::
