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
2. Either let the runtime create the databases (give the user the right to create databases), or create
   `temporal` and `temporal_visibility` yourself and give the user full rights on both.

   ```sql
   -- PostgreSQL
   CREATE USER workflow WITH PASSWORD '<password>' CREATEDB;
   -- MySQL
   CREATE USER 'workflow'@'%' IDENTIFIED BY '<password>';
   GRANT ALL PRIVILEGES ON temporal.* TO 'workflow'@'%';
   GRANT ALL PRIVILEGES ON temporal_visibility.* TO 'workflow'@'%';
   ```

3. Configure `[database]` in `conf/runtime.toml` ([Configure the runtime](configure.md)).

On the first start the engine logs `Setting up Temporal's schema versioning` for each database. Later starts leave
current databases alone and apply only the schema versions a newer engine brings.

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
SQL="bin/workflow-engine-sql --plugin postgres12 --ep db.example.com -p 5432 -u dba --pw <password>"
$SQL --db temporal create-database
$SQL --db temporal_visibility create-database
$SQL --db temporal setup-schema -v 0.0
$SQL --db temporal update-schema --schema-name postgresql/v12/temporal
$SQL --db temporal_visibility setup-schema -v 0.0
$SQL --db temporal_visibility update-schema --schema-name postgresql/v12/visibility
```

For MySQL use `--plugin mysql8`, port 3306 and the schema names `mysql/v8/temporal` and `mysql/v8/visibility`.
With `manageSchema = false`, the engine refuses to start on a missing or outdated schema.

:::info
With schema management on, database names may use letters, digits and underscores only, starting with a letter or
an underscore.
:::
