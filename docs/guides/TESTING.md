# Testing

## Layers

| Layer | Where | Runs | Gate |
|---|---|---|---|
| Go unit tests | Next to the code in each module | `make test`, `make test-coverage`, every pull request | 75% statement coverage outside `cmd/` wrappers |
| Tool and script tests | `tools/internal/...` (the coverage gate, the suite runner, the shell scripts run for real) | `make test`, every pull request | All pass |
| Ballerina smoke suite | `tests/ballerina/smoke` | `make test-smoke`, every pull request | All pass |
| Ballerina full suite | `tests/ballerina/full` | `make test-full`, on `main`, nightly, on demand | All pass |
| License and vulnerability checks | `make license-check`, `make vulncheck` | Every pull request and daily | No findings |
| SBOM | `make sbom` | Every pull request, daily, and in every distribution | Builds for both binaries |

## Go unit tests

- Same package as the code, `*_test.go`, fixtures in `testdata/`.
- Replace collaborators with small fakes through a field or parameter (`engine/internal/cli` takes the Temporal
  server constructor as a field, so tests run the whole start path without a server).
- `make test-coverage` writes profiles to `build/coverage/` and fails below 75%. `cmd/` packages are thin `main`
  wrappers and are excluded; the Ballerina suites exercise the real binaries.
- `make test-race` runs the unit tests with the race detector; CI runs it on every pull request.

## Ballerina suites

The suites run real Ballerina programs on jBallerina against the engine binary built from the same commit. They
are what proves the runtime runs `ballerina/workflow` integrations.

`go run ./tools/cmd/balsuite <smoke|full> --engine build/workflow-engine` (what `make test-smoke` and `make test-full` run):

1. Starts `workflow-engine` from `tests/engine/runtime-<database>.toml` with `--allow-no-auth` (`--database`, default
   `sqlite`). For `postgresql` and `mysql` it first creates both databases and installs Temporal's schemas with
   `workflow-engine-sql`; the database must be running on 15432 or 13306, user `workflow` (PostgreSQL) or `root` (MySQL), password in
   `WORKFLOW_TEST_DB_PASSWORD`.
2. Waits until `temporal operator cluster health` reports `SERVING` on `localhost:17233`.
3. Creates the `default` namespace and the `WorkflowKind` search attribute `ballerina/workflow` uses.
4. Runs `bal test` in the suite directory.
5. Stops the engine and, if anything failed, prints the end of the engine log. The log is written to a temporary
   directory that is removed after the run; set `ENGINE_LOG_DIR` to keep it.

Every step is time-limited: each Temporal CLI call (15 s), engine start-up (`--startup-timeout`, 90 s) and
`bal test` (`--test-timeout`, 30 min). A timeout fails the run and kills `bal test` with its JVM, and the engine is
still stopped.

| Suite | Covers |
|---|---|
| Smoke | A workflow runs activities and returns; a data event resumes a waiting workflow; a failing activity fails the workflow and its status says so; concurrent instances stay independent |
| Full | Activity retries; workflow errors; durable timers; child workflows (start and wait, call); data events in either order; human tasks (approve, reject); execution info and activity history |

### Everything in Docker

`make test-docker` runs what GitHub Actions runs, on any machine with Docker and nothing else installed:
`make check` and `make sbom`, then the smoke and full suites on SQLite, PostgreSQL 16 and MySQL 8.4.

```sh
make test-docker                                              # gates, then both suites on all three databases
make test-docker DOCKER_TARGET=smoke DATABASES=postgresql     # one suite on one database
make test-docker DOCKER_TARGET=gates                          # make check and make sbom only
```

`tests/docker/compose.yml` starts PostgreSQL and MySQL on 15432 and 13306 in one network namespace with the runner,
so `tests/engine/runtime-*.toml` work unchanged. The runner image (`tests/docker/Dockerfile`) has the Go toolchain,
the Temporal CLI and Ballerina on Java 25; the pinned Go tools are installed into a volume on first use. Each run works on a fresh copy of the source tree,
uncommitted changes included, with empty databases; engine logs land in `build/docker/`. Go, tool and
Ballerina caches are kept in Docker volumes between runs. If a run reports a damaged module cache (for example
after the Docker disk filled up), reset them with `docker compose -f tests/docker/compose.yml down --volumes`.

### Adding a Ballerina test

1. Add the workflow and activities to the suite's `workflows.bal`.
2. Add the test to the matching `tests/<area>_test.bal`, or a new file for a new area, with
   `@test:Config {groups: ["full", "<area>"]}` (or `["smoke"]`).
3. Use only the public `ballerina/workflow` and `ballerina/workflow.management` API.
4. Give each run its own input or ID so the suite can rerun against the same engine.
5. Run `make test-full` (or `make test-smoke`) and list the new test in the feature's page in `docs/features/`.

CI installs the latest Swan Lake release with `ballerina-platform/setup-ballerina` and a pinned Temporal CLI. Use
jBallerina until the Go-based Ballerina can run `ballerina/workflow`.
