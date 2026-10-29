# tests

| Path | What |
|---|---|
| `ballerina/smoke` | Smoke suite: the basics every engine build must keep (every pull request) |
| `ballerina/full` | Full suite: workflow features against the runtime (on `main`, nightly, on demand) |
| `docker/` | `make test-docker`: the CI gates and both suites in Docker with PostgreSQL and MySQL |
| `engine/runtime-<database>.toml` | runtime.toml for the suites (ports 17xxx): in-memory SQLite, PostgreSQL on 15432, MySQL on 13306 |

The runner that starts the engine, prepares the `default` namespace, runs a suite and stops the engine is
`tools/cmd/balsuite` (`make test-smoke`, `make test-full`). See [docs/guides/TESTING.md](../docs/guides/TESTING.md). Contract tests (the Manager against an integration's own
Workflow Management API) and upgrade tests join here as those components arrive.
