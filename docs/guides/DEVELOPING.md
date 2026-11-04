# Developing

## Prerequisites

| Tool | Version | For |
|---|---|---|
| Go | 1.26.8 or later (the toolchain line in `go.work` selects the patched release) | Everything |
| Bash | Any | CI glue scripts, the commit hook |
| Ballerina Swan Lake (`bal`) | Latest | `make test-smoke`, `make test-full` |
| Temporal CLI (`temporal`) | Any recent release | The Ballerina suite runner |

After cloning: `make install-hooks`. The pinned golangci-lint, go-licenses, govulncheck and cyclonedx-gomod are
installed into `build/tools` the first time a target needs them (`make tools` installs all four); each binary is
named after its version, so a version bump in the `Makefile` installs the new one on the next run.

## Layout

The repository is a Go workspace (`go.work`). Each component is its own module (`engine/` today; `launcher/` and
`manager/` as they arrive), so `make` targets loop over the workspace modules. `tools/` is the module for the
repository's own tools: the coverage gate (`covercheck`), the Ballerina suite runner (`balsuite`) and the tests
of the shell scripts in `.github/scripts`.

## Build and run

```sh
make build                                  # build/workflow-engine, build/workflow-engine-sql
./build/workflow-engine version
./build/workflow-engine render-config --runtime-config tests/engine/runtime-sqlite.toml   # inspect the generated config
./build/workflow-engine start --runtime-config tests/engine/runtime-sqlite.toml --allow-no-auth
```

`tests/engine/runtime-sqlite.toml` runs every service in one process on in-memory SQLite, on ports 17233 (frontend
gRPC), 17243 (frontend HTTP), 17234, 17235 and 17239. The PostgreSQL and MySQL variants expect a database on 15432
or 13306 and the password in `WORKFLOW_TEST_DB_PASSWORD`; see [TESTING.md](TESTING.md). Point the Temporal CLI at `localhost:17233` to inspect it:

```sh
temporal operator cluster health --address localhost:17233
temporal operator namespace create --namespace default --address localhost:17233
```

`--allow-no-auth` is for development and tests only: without it the engine refuses to start unless an
authorizer is configured.

## Checks

```sh
make check          # build, vet, lint, gofmt, unit tests with the 75% coverage gate, licenses, vulnerabilities
make test-smoke     # Ballerina smoke suite against the engine
make test-full      # Ballerina full suite
make test-docker    # all of the above and both suites on SQLite, PostgreSQL and MySQL, in Docker
```

Set `BAL` or `TEMPORAL_CLI` if those commands are not on `PATH`, for example the `bal` bundled with WSO2
Integrator.

## Dependencies

- Temporal is bumped only in `engine/go.mod`, one minor version at a time.
- A dependency with a fix Temporal has not released is raised with `go get <module>@<version>` in `engine/`.
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
| The engine exits about a minute after start with auth on and an explicit `--service` list | No process runs the internal frontend | Add `--service internal-frontend`, or run it in another process; without `--service` the engine starts it |
