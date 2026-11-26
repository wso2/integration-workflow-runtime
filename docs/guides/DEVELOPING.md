# Developing

## Prerequisites

| Tool | Version | For |
|---|---|---|
| Go | 1.26.10 or later (the toolchain line in `go.work` selects the patched release) | Everything |
| Bash | Any | CI glue scripts, the commit hook |
| Ballerina Swan Lake (`bal`) | 2201.14.1 (2201.14.2 cannot load its bundled `ballerina/workflow`) | `make test-smoke`, `make test-full` |
| Temporal CLI (`temporal`) | Any recent release | The Ballerina suite runner |

After cloning: `make install-hooks`. The pinned golangci-lint, go-licenses, govulncheck and cyclonedx-gomod are
installed into `build/tools` the first time a target needs them (`make tools` installs all four); each binary is
named after its version, so a version bump in the `Makefile` installs the new one on the next run.

## Layout

The repository is a Go workspace (`go.work`). Each component is its own module (`engine/`, which also holds the
launcher; `cli/` for `workflow-cli`, which imports no server code; `manager/` as it arrives), so `make` targets loop over the workspace modules. `tools/` is the module for the
repository's own tools: the coverage gate (`covercheck`), the Ballerina suite runner (`balsuite`) and the tests
of the shell scripts in `.github/scripts`.

## Build and run

The quickest engine for local work is development mode: every service on SQLite, no TLS or authorization, and the
`default` namespace with the `WorkflowKind` search attribute already created, like `temporal server start-dev`.

```sh
make build
./build/workflow-engine start-dev           # frontend on 127.0.0.1:7233, in memory
./build/workflow-engine start-dev --port 17233 --db-file data/dev.db --namespace orders --log-level info
```

`--ip` (default `127.0.0.1`) and `--port` (default 7233; the other services take the next ports up to `--port`+10)
set where it listens, `--db-file` keeps the data in a SQLite file instead of memory, `--namespace` adds namespaces
besides `default` (repeatable) and `--log-level` defaults to `warn`. In a distribution, `bin/workflow-runtime dev`
runs the same with the same flags.

To run the engine as it runs in production, from a runtime configuration:

```sh
make build                                  # build/workflow-runtime, workflow-engine, workflow-engine-sql, workflow-cli
make cli                                    # only build/workflow-cli
./build/workflow-engine version
./build/workflow-engine render-config --runtime-config tests/engine/runtime-sqlite.toml   # inspect the generated config
./build/workflow-engine start --runtime-config tests/engine/runtime-sqlite.toml --allow-no-auth
```

`tests/engine/runtime-sqlite.toml` runs every service in one process on in-memory SQLite, on ports 17233 (frontend
gRPC), 17243 (frontend HTTP), 17234, 17235 and 17239. The PostgreSQL and MySQL variants expect a database server on
15432 or 13306 and the password in `WORKFLOW_TEST_DB_PASSWORD`; see [TESTING.md](TESTING.md).

On PostgreSQL and MySQL the engine prepares the databases on every start: it creates the workflow and visibility
databases when missing and applies the schema versions it ships, so the user needs the right to create databases
(or databases a DBA created) and to change their schema. Set `manageSchema = false` under `[database]` to leave
the schema to a DBA; see [database schema](../features/database-schema.md). These configurations create no
namespace: use `workflow-cli` to check health and create one with the `WorkflowKind` search attribute:

```sh
./build/workflow-cli operator cluster health --address localhost:17233
./build/workflow-cli operator namespace create --namespace default --address localhost:17233
```

The [workflow-cli reference](../user-guide/reference/workflow-cli.md) lists every command.

`--allow-no-auth` is for development and tests only: without it the engine refuses to start unless an
authorizer is configured.

## Distribution and image

```sh
make dist                                   # dist/wso2-workflow-runtime-<version>-<os>-<arch>.zip for GOOS/GOARCH
make image IMAGE_PLATFORMS=linux/arm64 IMAGE_ARGS=--load   # build the linux zip, then load the image locally
```

`IMAGE` names the image, `IMAGE_PLATFORMS` lists its platforms (default `linux/amd64,linux/arm64`) and `IMAGE_ARGS`
passes extra `docker buildx` arguments, such as `--push`. In an unzipped distribution, `bin/workflow-runtime` starts
the engine from `conf/runtime.toml`, or from the file `WORKFLOW_RUNTIME_CONFIG` names, and passes `render-config`,
`version` and the engine's flags through. In `runtime.toml`, `engine.logFile` writes the log to a file instead of
standard output, and `engine.broadcastAddress = "auto"` advertises the host's own address, as a container needs; see
the [runtime.toml reference](../user-guide/reference/runtime-toml.md) and
[distribution](../features/distribution.md).

## Checks

```sh
make check          # build, vet, lint, gofmt, unit tests with the 75% coverage gate, licenses, vulnerabilities
make test-smoke     # Ballerina smoke suite against the engine
make test-full      # Ballerina full suite
make test-docker    # all of the above and both suites on SQLite, PostgreSQL and MySQL, in Docker
go run ./tools/cmd/balsuite upgrade --engine build/workflow-engine --database postgresql   # or mysql
```

`balsuite upgrade` starts a workflow on Temporal 1.30.9 in Docker (`--old-temporal` picks another release,
`--docker` another docker command), then starts the engine on the same databases and checks that the workflow
upgrades its schema and completes.

Set `BAL` or `TEMPORAL_CLI` if those commands are not on `PATH`, for example the `bal` bundled with WSO2
Integrator.

## Dependencies

- Temporal is bumped in `engine/go.mod`, one minor version at a time, and `cli/go.mod` follows in the same change:
  in workspace mode a lower CLI pin changes the engine's module graph. `tools/internal/temporalpin` fails when they differ.
- A dependency with a fix Temporal has not released is raised with `go get <module>@<version>` in `engine/`.
- An advisory whose fix does not build with Temporal goes in `third-party/vulnerability-exceptions.txt`, with the
  reason and when to remove it; `make vulncheck` then reports it as allowed and notes when it is no longer needed.
- After any change: `go mod tidy`, `make licenses`, commit `third-party/go-licenses.csv` and `go.work.sum`, then
  `make license-check` and `make vulncheck`.

## Releases

- Every push to `main` builds a nightly distribution for every platform (Build on push workflow).
- `make release RELEASE_VERSION=v0.1.0 REMOTE=<upstream remote>` tags a clean `main` or `release-X.Y.x` checkout
  and pushes the tag; the Release workflow builds and publishes the zips with checksums and a Sigstore bundle
  (`<file>.sigstore.json`) per file, signed keyless by the workflow. Prereleases use a suffix
  (`v0.1.0-alpha.1`). It refuses any other branch and a tag that already exists locally or on the remote, and
  removes the local tag if the push fails, so a release can be retried. The Release workflow itself also refuses a
  tag whose commit is not on `main` or a `release-X.Y.x` branch.
- Verify a downloaded file with `cosign verify-blob --bundle <file>.sigstore.json --certificate-oidc-issuer
  https://token.actions.githubusercontent.com --certificate-identity-regexp
  '^https://github.com/wso2/integration-workflow-runtime/\.github/workflows/release\.yml@refs/tags/v' <file>`.
- `make dist` builds the same zip locally for the current `GOOS`/`GOARCH`, with a CycloneDX SBOM per binary in
  `sbom/` (`make sbom` builds only the SBOMs).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `go-licenses` or `govulncheck` fails reading the standard library | The tool ran on an older Go than `go.work` selects | Use the `make` targets; they run tools on the selected toolchain |
| `failed to load the module 'ballerina/workflow:…' from its BIR due to: unexpected type: other` | A stale or corrupted compiled cache under `~/.ballerina/repositories/central.ballerina.io/cache-<version>/ballerina/workflow` | Delete that cache directory; it is rebuilt from the downloaded package |
| Ballerina tests fail with `Failed to start workflow` | The namespace lacks the `WorkflowKind` search attribute | Run the suites through `make test-smoke` / `make test-full` (the `balsuite` runner adds it) |
| The Dependency validation check fails on a new `go.mod` line | The module is not in WSO2's dependency registry for the `integrator` scope | Open a PR on [wso2/engineering-governance](https://github.com/wso2/engineering-governance) adding it, or drop the dependency |
| The Dependency validation comment lists a `go.temporal.io` module as PENDING | Every `go.temporal.io` module goes to the registry in one request, not yet sent; until then they are reported but do not fail the check (`PENDING_MODULES` in `dependency-validation.yml`) | Nothing; remove the exception once the registry has them |
| The engine exits about a minute after start with auth on and an explicit `--service` list | No process runs the internal frontend | Add `--service internal-frontend`, or run it in another process; without `--service` the engine starts it |
| Every node of a cluster with auth on exits about a minute after start | No node's `[runtime] profiles` include `frontend` (or `all`/`engine`), so nothing runs the internal frontend | Give at least one node the `frontend` profile; a node without it logs no warning, since history-only nodes are normal |
