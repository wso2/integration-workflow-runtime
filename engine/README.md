# engine

Temporal's services in our own binary. See [0001](../docs/decisions/0001-temporal-packaging.md) and
[docs/features/engine.md](../docs/features/engine.md).

| Path | What |
|---|---|
| `go.mod` | Pins `go.temporal.io/server` and the Go toolchain. Every Temporal or dependency bump is a change here |
| `cmd/workflow-engine` | Thin `main` for the engine command |
| `cmd/workflow-engine-sql` | Temporal's schema tool, built from the same version |
| `internal/cli` | The engine command: flags, configuration, the secure-by-default check, server start |
| `plugins/` | Our extensions through Temporal's public extension points (SQL plugins, authorizer) |
| `patches/` | Notes on any fork patch in use: why, upstream PR, when to drop it |

```sh
./build/workflow-engine start --config-file ../tests/engine/temporal-test.yaml --allow-no-auth
./build/workflow-engine-sql --plugin postgres12 --ep db.example.com -p 5432 -u workflow --db temporal setup-schema -v 0.0
```
