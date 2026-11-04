This document tells AI coding agents (and people) how to work in this repository: what it builds, how it is
organized, and the rules every change must follow. `CLAUDE.md` imports this file, so Claude Code and Codex read
the same instructions.

## What this repository builds

The WSO2 Integration Workflow Runtime: the engine that runs WSO2 Integrator's durable workflows, shipped and
supported by WSO2. The Temporal server runs inside it as an internal component; customers provide a database and
an identity provider and never configure Temporal.

Read [docs/guides/ARCHITECTURE.md](docs/guides/ARCHITECTURE.md) before changing code. In short:

| Component | Path | What it does |
|---|---|---|
| Engine | `engine/` | Our Go binary that embeds Temporal's server (frontend, history, matching, worker) as a library, pinned in `engine/go.mod` |
| Engine endpoint | `engine/` (planned) | The integrations' one port (7233): gRPC to Temporal's frontend, `/runtime/...` HTTPS to the Manager |
| Launcher | `launcher/` (planned) | Starts components per profile, writes Temporal's configuration from `conf/runtime.toml`, enforces one-minor upgrades |
| Manager | `manager/` (planned) | Go service: engine administration, Workflow Management API, SSO sign-in, roles; serves both portals |
| Portals | `portals/` (planned) | Admin Portal and User Portal, static TypeScript apps served by the Manager |
| Distribution | `distribution/` | Zip and Docker layout, `conf/runtime.toml` |
| Tests | `tests/` | Ballerina smoke and full suites run against the engine binary |
| Tools | `tools/` | Go module with the repository's own tools: the coverage gate, the Ballerina suite runner, script tests |
| Third-party | `third-party/` | Generated license inventory and notices shipped with every artifact |

Decision records live in `docs/decisions/`; feature documentation in `docs/features/`.

## Commands

| Command | What it does |
|---|---|
| `make build` | Build `build/workflow-engine` and `build/workflow-engine-sql` |
| `make test` | Go unit tests in every `go.work` module |
| `make test-coverage` | Unit tests plus the coverage gate (75% minimum, `cmd/` wrappers excluded) |
| `make test-race` | Unit tests with the race detector (CI runs it on every pull request) |
| `make tools` | Install the pinned golangci-lint, go-licenses, govulncheck and cyclonedx-gomod into `build/tools` (the targets that need them do this on first use) |
| `make lint` / `make vet` / `make fmt-check` | golangci-lint v2.13.2, go vet, gofmt |
| `make test-smoke` / `make test-full` | Ballerina suites against the engine (needs `bal` and the `temporal` CLI on `PATH`, or `BAL` / `TEMPORAL_CLI` set) |
| `make test-docker` | The CI gates and both suites in Docker, PostgreSQL and MySQL included (`DOCKER_TARGET`, `DATABASES`) |
| `make license-check` / `make vulncheck` | License gate and `govulncheck` |
| `make sbom` | CycloneDX SBOM per binary in `build/sbom/` (included by `make dist`) |
| `make check` | Every gate CI runs, except the Ballerina suites |
| `make dist` | Zip for one `GOOS`/`GOARCH` in `dist/` |
| `make install-hooks` | Install the commit hook (subject format, license headers, gofmt, build, lint) |

Go 1.26.8 or later; the toolchain line in `go.work` and `engine/go.mod` selects the patched Go release.

## Rules for every change

These are requirements, not suggestions. A change that misses one is not done.

1. **Document it in the same commit.**
   - A new or changed feature updates its page in `docs/features/` (create it from `docs/features/TEMPLATE.md`).
   - A design change updates `docs/guides/ARCHITECTURE.md` or adds a decision record in `docs/decisions/`.
   - A new or changed command, flag, configuration key or port updates the README, `docs/guides/DEVELOPING.md`
     and the feature page.
2. **Test it.**
   - Go code: unit tests in the same package, in the same commit. `make test-coverage` must stay at or above 75%,
     and new code should be covered at least as well as the code around it.
   - User-visible behavior (anything an integration, an operator or the API can observe): a Ballerina test.
     Basic behavior that every build must keep goes in `tests/ballerina/smoke`; feature behavior in
     `tests/ballerina/full`.
   - Run `make test-coverage` and, for engine or protocol changes, `make test-smoke` before you finish.
3. **Keep the security defaults.** The engine refuses to start without an authorizer unless `--allow-no-auth` is
   given; never make an open frontend the default, never log or commit secrets, and never disable TLS or token
   checks to make a test pass.
4. **Never copy or patch Temporal's source in this repository.** Use Temporal's public extension points
   (`temporal.With...` server options, `sql.RegisterPlugin`). Bump Temporal only through `engine/go.mod`, one minor
   version at a time; a fix upstream has not shipped goes through the fork and a `replace` line
   ([0001](docs/decisions/0001-temporal-packaging.md)).
5. **Dependencies stay clean.** After any `go.mod` change run `make licenses` and commit the regenerated
   `third-party/go-licenses.csv` and `go.work.sum`; `make license-check` and `make vulncheck` must pass. A module
   whose license needs a manual decision goes in `third-party/license-exceptions.txt` with the reason. A new
   direct dependency must be in [WSO2's dependency registry](https://github.com/wso2/engineering-governance/blob/main/dependency-registry/go.yaml)
   for the `integrator` scope; the Dependency validation workflow blocks the PR otherwise.
6. **Actions are pinned by commit SHA** with the version in a comment; Dependabot proposes the bumps.
7. **Every new source file starts with the WSO2 license header** (`Copyright (c) <year>, WSO2 LLC.
   (http://www.wso2.com).`, Apache 2.0). Files that adapt Temporal code also name Temporal's MIT copyright.
8. **Commit messages and PR titles follow Conventional Commits** (below).

## Coding style

- Go code is formatted with `gofmt -s` and passes golangci-lint (`.golangci.yml`).
- Don't export symbols unless another package needs them. `cmd/` packages stay thin: a `main` that calls an
  `internal/` package, so the logic is testable.
- Constructors set every field; maps are initialized to empty maps, never left nil.
- Inject what a test needs to replace (clocks, servers, clients) through a field or parameter, not a global.
- Wrap errors with context (`fmt.Errorf("unable to load configuration: %w", err)`); error strings start lowercase.
- Comments explain why, not what. Don't comment each line: extract a well-named function instead. Keep a comment
  to one line under 120 characters where possible; put longer rationale in the commit message or the docs.
- Bash for CI glue (`.github/scripts/*.sh`, the hook), Go for anything with logic (`tools/`, run with
  `go run ./tools/cmd/<tool>`); no Python or JavaScript scripts. A shell script with logic gets its tests in
  `tools/internal/scripts/`, which runs the script for real.
- Binary and product names never use "Temporal" (`workflow-engine`, not `temporal-server`).

## Tests

### Go unit tests

- Table-driven where it helps; fixtures in a `testdata/` directory next to the test.
- Prefer small fakes that implement an interface (see `engine/internal/cli/cli_test.go`) over mocking libraries.
- Use `t.TempDir()` for files; never write into the repository from a test.

### Ballerina suites

- `go run ./tools/cmd/balsuite <smoke|full> --engine build/workflow-engine [--database sqlite|postgresql|mysql]`
  starts the engine from `tests/engine/runtime-<database>.toml` (ports 17xxx, `--allow-no-auth`), creating the SQL
  schemas first for PostgreSQL and MySQL; then it creates the `default` namespace and the `WorkflowKind` search
  attribute, runs `bal test` in the suite, and stops the engine.
- Each suite is a Ballerina package: workflows and activities in `workflows.bal`, tests in `tests/*_test.bal`
  (one file per feature area), connection settings in `tests/Config.toml`.
- Tests use the public `ballerina/workflow` API only (`workflow:run`, `workflow:sendData`,
  `workflow:waitForResult`, `workflow:getStatus`, `workflow:completeHumanTask`, `ballerina/workflow.management`).
- jBallerina is used until the Go-based Ballerina can run `ballerina/workflow`; CI installs the latest Swan Lake
  release with `ballerina-platform/setup-ballerina`.
- Give every test its own input or ID (`uniqueKey`) so suites can rerun against the same engine.

## Documentation

- `docs/guides/` explains the system: `ARCHITECTURE.md` (components and how they connect), `DEVELOPING.md`
  (build, run, debug), `TESTING.md` (test layers and how to add tests).
- `docs/features/` has one page per feature, from `TEMPLATE.md`: what it does, configuration, behavior, limits,
  tests that cover it. Write for someone who has not read the code.
- `docs/decisions/` holds decision records (`NNNN-title.md`): context, options, decision, consequences. Add one
  for any choice that is hard to reverse.

## Commit messages and PR titles

Every commit subject and PR title follows [Conventional Commits](https://www.conventionalcommits.org/), checked by
`.github/scripts/check_commit_subject.sh` in the commit hook and the Lint PR workflow (which checks the PR
title with `amannn/action-semantic-pull-request`):

```text
<type>(<optional scope>): <description>
```

- Types: `feat`, `fix`, `build`, `chore`, `ci`, `docs`, `style`, `refactor`, `perf`, `test`, `revert`.
- Scope: the component (`engine`, `launcher`, `manager`, `portals`, `dist`, `tests`, `deps`).
- The description starts with a lowercase verb in the imperative mood ("add", not "added"), has no final period,
  and the subject is at most 72 characters (aim for 50).
- A breaking change uses `!` before the colon (`feat(engine)!: ...`).
- The body explains what and why, wrapped at 72 characters.
- Do not add `Co-Authored-By` or other attribution trailers.

Examples: `feat(engine): add the engine endpoint on port 7233`, `fix(deps): raise golang.org/x/net to v0.60.0`.

## Releases

- Every push to `main` builds a nightly distribution (`<next>-<YYYYMMDD>-<HHMM00>-<sha>`).
- A release is a `vMAJOR.MINOR.PATCH[-PRERELEASE]` tag: `make release RELEASE_VERSION=v0.1.0 REMOTE=<upstream>`.
  The Release workflow builds every platform and publishes the zips with checksums, each with a Sigstore bundle
  (`cosign sign-blob`, keyless). It refuses a tag whose commit is not on `main` or a `release-X.Y.x` branch.
- Point releases come from `release-X.Y.x` branches.

## Issues and pull requests

- Issues are tracked in [wso2/product-integrator](https://github.com/wso2/product-integrator/issues) with the
  `Area/Workflow` label; this repository has no issue tracker.
- A pull request references its public issue (`Resolves wso2/product-integrator#123`) and fills in the PR template.
