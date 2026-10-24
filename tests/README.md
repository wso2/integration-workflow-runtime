# tests

| Path | What |
|---|---|
| `ballerina/run_suite.py` | Starts the engine, prepares the `default` namespace, runs a Ballerina suite, stops the engine |
| `ballerina/smoke` | Smoke suite: the basics every engine build must keep (every pull request) |
| `ballerina/full` | Full suite: workflow features against the runtime (on `main`, nightly, on demand) |
| `engine/temporal-test.yaml` | Engine configuration for the suites: in-memory SQLite, ports 17xxx |

See [docs/guides/TESTING.md](../docs/guides/TESTING.md). Contract tests (the Manager against an integration's own
Workflow Management API) and upgrade tests join here as those components arrive.
