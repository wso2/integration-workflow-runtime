# plugins

Our code that plugs into Temporal without a fork. Planned:

- `sqlserver`, `oracle`, `mariadb`: SQL persistence plugins registered with `sql.RegisterPlugin`, with their schemas.
- Authorization: the claim mapper for Manager-issued worker tokens, if Temporal's default mapper stops being enough.

Each plugin is imported from `cmd/workflow-engine/main.go` and `cmd/workflow-engine-sql/main.go`.
