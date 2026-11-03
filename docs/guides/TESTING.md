# Testing

## Layers

| Layer | Where | Runs | Gate |
|---|---|---|---|
| Go unit tests | Next to the code in each module | `make test`, `make test-coverage`, every pull request | 75% statement coverage outside `cmd/` wrappers |
| Launcher tests on Windows | `engine/internal/launcher` (the engine runs as a child process there) | The `windows-latest` job of every pull request | All pass |
| Tool and script tests | `tools/internal/...` (the coverage gate, the suite runner, the shell scripts run for real) | `make test`, every pull request | All pass |
| Engine tests | `tools/internal/enginetest`: the real engine binary driven by the Temporal CLI | `WORKFLOW_ENGINE=build/workflow-engine go test ./internal/enginetest` in `tools/`; the SQLite smoke job of every pull request | All pass |
| Ballerina smoke suite | `tests/ballerina/smoke` | `make test-smoke`, every pull request (SQLite, PostgreSQL, MySQL and `start-dev`) | All pass |
| Ballerina full suite | `tests/ballerina/full` | `make test-full`, on `main`, nightly, on demand | All pass |
| Upgrade run | `tests/ballerina/upgrade`, `balsuite upgrade` | The PostgreSQL and MySQL smoke jobs of every pull request | The workflow finishes |
| License and vulnerability checks | `make license-check`, `make vulncheck` | Every pull request and daily | No findings |
| SBOM | `make sbom` | Every pull request, daily, and in every distribution | Builds for both binaries |

## Go unit tests

- Same package as the code, `*_test.go`, fixtures in `testdata/`.
- Replace collaborators with small fakes through a field or parameter (`engine/internal/cli` takes the Temporal
  server constructor as a field, so tests run the whole start path without a server).
- `make test-coverage` writes profiles to `build/coverage/` and fails below 75%. `cmd/` packages are thin `main`
  wrappers and are excluded; the Ballerina suites exercise the real binaries.
- `make test-race` runs the unit tests with the race detector; CI runs it on every pull request.

## Engine tests

`tools/internal/enginetest` starts the built `workflow-engine` on free ports and drives it with the Temporal CLI, for
behavior a configuration test cannot show, such as a SQLite file surviving a restart or `start-dev` needing no setup.
A lock file in the temporary directory keeps concurrent runs on one Unix host from picking the same ports; on Windows,
run one at a time. The tests skip unless `WORKFLOW_ENGINE` names the engine binary and `temporal` is on `PATH`:

```sh
make engine
cd tools && WORKFLOW_ENGINE=$PWD/../build/workflow-engine go test -count=1 -v ./internal/enginetest
```

## Ballerina suites

The suites run real Ballerina programs on jBallerina against the engine binary built from the same commit. They
are what proves the runtime runs `ballerina/workflow` integrations.

`go run ./tools/cmd/balsuite <smoke|full> --engine build/workflow-engine` (what `make test-smoke` and `make test-full` run):

1. Starts `workflow-engine` from `tests/engine/runtime-<database>.toml` with `--allow-no-auth` (`--database`, default
   `sqlite`). For `postgresql` and `mysql` the engine creates both databases and their schemas on start; the
   database must be running on 15432 or 13306, user `workflow` (PostgreSQL) or `root` (MySQL), password in
   `WORKFLOW_TEST_DB_PASSWORD`. `--database dev` runs `start-dev --port 17233` instead: in memory, with no
   configuration file.
2. Waits until `temporal operator cluster health` reports `SERVING` on `localhost:17233`.
3. Creates the `default` namespace and the `WorkflowKind` search attribute `ballerina/workflow` uses; with `dev` it
   creates nothing, so the run proves that `start-dev` needs no setup.
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

### Upgrade run

`go run ./tools/cmd/balsuite upgrade --engine build/workflow-engine --database postgresql|mysql` proves that a
workflow started on the previous Temporal release continues on this engine. It needs Docker and the same database
as the suites, and starts from Temporal 1.30.9 (`--old-temporal`), the newest release whose schema is older than the
engine's (1.31 already ships the engine's schema versions):

1. Creates two databases and installs 1.30.9's schemas with `temporalio/admin-tools:1.30.9`.
2. Runs `temporalio/server:1.30.9` on `localhost:17233` against them, with the engine's cluster name and shard count,
   and creates `default` with `WorkflowKind`.
3. Runs the `before` group of `tests/ballerina/upgrade`: an order workflow runs its first activity and waits for a
   decision. The runner checks that the history holds the completed activity, then stops the old server.
4. Starts `workflow-engine` on the same databases, which upgrades their schemas, and checks that both versions moved.
5. Runs the `after` group: the decision is sent, the workflow runs its second activity on the engine and completes.
6. Drops both databases. With `ENGINE_LOG_DIR` set, the old server's and the engine's logs are kept there.

### Everything in Docker

`make test-docker` runs what GitHub Actions runs, on any machine with Docker and nothing else installed (Compose
v2.20.2 or later, for optional `depends_on`):
`make check` and `make sbom`, then the smoke and full suites on SQLite, PostgreSQL 16 and MySQL 8.4.

```sh
make test-docker                                              # gates, then both suites on all three databases
make test-docker DOCKER_TARGET=smoke DATABASES=postgresql     # one suite on one database
make test-docker DOCKER_TARGET=gates                          # make check and make sbom only
```

`tests/docker/compose.yml` starts the selected databases (PostgreSQL on 15432, MySQL on 13306; neither for
`DOCKER_TARGET=gates` or SQLite only) in one network namespace with the runner, so `tests/engine/runtime-*.toml`
work unchanged. Each checkout runs as its own Compose project, so runs in different worktrees do not stop each
other's containers; runs in the same checkout share `build/docker/`, so run them one at a time. The runner image (`tests/docker/Dockerfile`) has the Go toolchain,
the Temporal CLI and Ballerina on Java 25; the pinned Go tools are installed into a volume on first use. Each run works on a fresh copy of the source tree,
uncommitted changes included, with empty databases; engine logs land in `build/docker/`. Go, tool and
Ballerina caches are kept in Docker volumes (`workflow-runtime-tests-*`) shared by every checkout. If a run reports
a damaged module cache (for example after the Docker disk filled up), remove them with
`docker volume rm $(docker volume ls -q --filter name=workflow-runtime-tests-)`.

### Adding a Ballerina test

1. Add the workflow and activities to the suite's `workflows.bal`.
2. Add the test to the matching `tests/<area>_test.bal`, or a new file for a new area, with
   `@test:Config {groups: ["full", "<area>"]}` (or `["smoke"]`).
3. Use only the public `ballerina/workflow` and `ballerina/workflow.management` API.
4. Give each run its own input or ID so the suite can rerun against the same engine.
5. Run `make test-full` (or `make test-smoke`) and list the new test in the feature's page in `docs/features/`.

CI installs the latest Swan Lake release with `ballerina-platform/setup-ballerina` and a pinned Temporal CLI. Use
jBallerina until the Go-based Ballerina can run `ballerina/workflow`.
