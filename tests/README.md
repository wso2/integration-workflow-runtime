# tests

| Path | What |
|---|---|
| `ballerina/smoke` | Smoke suite: the basics every engine build must keep (every pull request) |
| `ballerina/full` | Full suite: workflow features against the runtime (on `main`, nightly, on demand) |
| `ballerina/upgrade` | Upgrade run: a workflow started on Temporal 1.30.9 finishes on the engine (`balsuite upgrade`) |
| `docker/` | `make test-docker`: the CI gates and both suites in Docker with PostgreSQL and MySQL |
| `engine/runtime-<database>.toml` | runtime.toml for the suites (ports 17xxx): in-memory SQLite, PostgreSQL on 15432, MySQL on 13306 |

The runner that starts the engine, prepares the `default` namespace, runs a suite and stops the engine is
`tools/cmd/balsuite` (`make test-smoke`, `make test-full`). See [docs/guides/TESTING.md](../docs/guides/TESTING.md). Contract tests (the Manager against an integration's own
Workflow Management API) join here as that component arrives.
