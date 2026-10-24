# Developing

## Prerequisites

| Tool | Version | For |
|---|---|---|
| Go | 1.26.8 or later (the toolchain line in `go.work` selects the patched release) | Everything |
| Python | 3.10 or later | Scripts, the Ballerina suite runner, the commit hook |
| golangci-lint | v2.13.2 (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`) | `make lint`, the commit hook |
| Ballerina Swan Lake (`bal`) | Latest | `make test-smoke`, `make test-full` |
| Temporal CLI (`temporal`) | Any recent release | The Ballerina suite runner |

After cloning: `make install-hooks`.

## Layout

The repository is a Go workspace (`go.work`). Each component is its own module (`engine/` today; `launcher/` and
`manager/` as they arrive), so `make` targets loop over the workspace modules.

## Build and run

```sh
make build                                  # build/workflow-engine, build/workflow-engine-sql
./build/workflow-engine version
./build/workflow-engine start --config-file tests/engine/temporal-test.yaml --allow-no-auth
```

The test configuration runs every service in one process on in-memory SQLite, on ports 17233 (frontend gRPC),
17243 (frontend HTTP), 17234, 17235 and 17239. Point the Temporal CLI at `localhost:17233` to inspect it:

```sh
temporal operator cluster health --address localhost:17233
temporal operator namespace create --namespace default --address localhost:17233
```

`--allow-no-auth` is for development and tests only: without it the engine refuses to start unless an
authorizer is configured.

## Checks

```sh
make check          # build, vet, lint, gofmt, unit tests with the 75% coverage gate, script tests, licenses, vulnerabilities
make test-smoke     # Ballerina smoke suite against the engine
make test-full      # Ballerina full suite
```

Set `BAL` or `TEMPORAL_CLI` if those commands are not on `PATH`, for example the `bal` bundled with WSO2
Integrator.

## Dependencies

- Temporal is bumped only in `engine/go.mod`, one minor version at a time.
- A dependency with a fix Temporal has not released is raised with `go get <module>@<version>` in `engine/`.
- After any change: `go mod tidy`, `make licenses`, commit `third-party/go-licenses.csv`, then `make license-check`
  and `make vulncheck`.

## Releases

- Every push to `main` builds a nightly distribution for every platform (Build on push workflow).
- `make release RELEASE_VERSION=v0.1.0 REMOTE=<upstream remote>` tags a clean `main` and pushes the tag; the
  Release workflow builds and publishes the zips with checksums. Prereleases use a suffix (`v0.1.0-alpha.1`).
- `make dist` builds the same zip locally for the current `GOOS`/`GOARCH`.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `go-licenses` or `govulncheck` fails reading the standard library | The tool ran on an older Go than `go.work` selects | Use the `make` targets; they run tools on the selected toolchain |
| `failed to load the module 'ballerina/workflow:…' from its BIR due to: unexpected type: other` | A stale or corrupted compiled cache under `~/.ballerina/repositories/central.ballerina.io/cache-<version>/ballerina/workflow` | Delete that cache directory; it is rebuilt from the downloaded package |
| Ballerina tests fail with `Failed to start workflow` | The namespace lacks the `WorkflowKind` search attribute | Run the suites through `tests/ballerina/run_suite.py`, which adds it |
| The engine exits about a minute after start with auth on | Temporal's internal frontend is not running | Start the `internal-frontend` service alongside `frontend` |
