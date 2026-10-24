# Testing

## Layers

| Layer | Where | Runs | Gate |
|---|---|---|---|
| Go unit tests | Next to the code in each module | `make test`, `make test-coverage`, every pull request | 75% statement coverage outside `cmd/` wrappers |
| Script tests | `.github/scripts/test_*.py` | `make test-scripts`, every pull request | All pass |
| Ballerina smoke suite | `tests/ballerina/smoke` | `make test-smoke`, every pull request | All pass |
| Ballerina full suite | `tests/ballerina/full` | `make test-full`, on `main`, nightly, on demand | All pass |
| License and vulnerability checks | `make license-check`, `make vulncheck` | Every pull request and daily | No findings |

## Go unit tests

- Same package as the code, `*_test.go`, fixtures in `testdata/`.
- Replace collaborators with small fakes through a field or parameter (`engine/internal/cli` takes the Temporal
  server constructor as a field, so tests run the whole start path without a server).
- `make test-coverage` writes profiles to `build/coverage/` and fails below 75%. `cmd/` packages are thin `main`
  wrappers and are excluded; the Ballerina suites exercise the real binaries.

## Ballerina suites

The suites run real Ballerina programs on jBallerina against the engine binary built from the same commit. They
are what proves the runtime runs `ballerina/workflow` integrations.

`tests/ballerina/run_suite.py <smoke|full> --engine build/workflow-engine`:

1. Starts `workflow-engine` on `tests/engine/temporal-test.yaml` with `--allow-no-auth`.
2. Waits until `temporal operator cluster health` reports `SERVING` on `localhost:17233`.
3. Creates the `default` namespace and the `WorkflowKind` search attribute `ballerina/workflow` uses.
4. Runs `bal test` in the suite directory.
5. Stops the engine and, if anything failed, prints the end of the engine log (`ENGINE_LOG_DIR` keeps it).

| Suite | Covers |
|---|---|
| Smoke | A workflow runs activities and returns; a data event resumes a waiting workflow; a failing activity fails the workflow and its status says so; concurrent instances stay independent |
| Full | Activity retries; workflow errors; durable timers; child workflows (start and wait, call); data events in either order; human tasks (approve, reject); execution info and activity history |

### Adding a Ballerina test

1. Add the workflow and activities to the suite's `workflows.bal`.
2. Add the test to the matching `tests/<area>_test.bal`, or a new file for a new area, with
   `@test:Config {groups: ["full", "<area>"]}` (or `["smoke"]`).
3. Use only the public `ballerina/workflow` and `ballerina/workflow.management` API.
4. Give each run its own input or ID so the suite can rerun against the same engine.
5. Run `make test-full` (or `make test-smoke`) and list the new test in the feature's page in `docs/features/`.

CI installs the latest Swan Lake release with `ballerina-platform/setup-ballerina` and a pinned Temporal CLI. Use
jBallerina until the Go-based Ballerina can run `ballerina/workflow`.
