# 0001. Packaging and patching Temporal

Status: proposed · 2026-10-16

## Context

The Workflow Runtime ships Temporal's four services (frontend, history, matching, worker) inside a WSO2
distribution. Customers never install or patch Temporal; WSO2 does, through its update mechanism. This record
compares the ways to bring Temporal into the product and sets out how patches, upgrades and our own fixes flow
through each.

### What the choice must allow

| # | Requirement |
|---|---|
| R1 | Ship under our product name and version, with Temporal as an internal component |
| R2 | Fix a CVE in Temporal's Go dependencies or the Go toolchain without waiting for a Temporal release |
| R3 | Fix a Temporal bug that upstream has not fixed (or will not fix) |
| R4 | Add our own code: SQL plugins (SQL Server, Oracle, MariaDB), authorization, configuration from the runtime's settings |
| R5 | Upgrade Temporal one minor version at a time, with schema migrations |
| R6 | Reproducible builds and a software bill of materials for every release |
| R7 | One binary per OS and architecture for VM installs, and Docker images |
| R8 | Start each service on its own (profiles) |
| R9 | Keep the maintenance cost proportional to how much we change |

## Options

**A. Repackage upstream release binaries.** Download `temporal-server` from Temporal's GitHub releases, verify
the checksum, put it in our zip and image.

**B. Git submodule.** `temporalio/temporal` as a submodule pinned to a release tag; our CI builds it with
upstream's Makefile and applies a series of patch files (`patches/*.patch`) before building.

**C. Full fork.** A WSO2 fork of `temporalio/temporal`, a branch per upstream release line with our commits on top; the product builds
from the fork.

**D. Go module dependency with a thin `main` (recommended).** Our repository holds a small Go program,
`engine/cmd/workflow-engine`, that imports `go.temporal.io/server` at a pinned version in `engine/go.mod` and
starts the services, the way Temporal's own CLI embeds the server. A fork is used only when we must change
Temporal's code, wired in with one `replace` line (option C, on demand).

**E. Vendored source.** Copy Temporal's source tree into our repository.

**F. Upstream Docker images.** Ship `temporalio/server` images and point the launcher at them.

## Comparison

| | A. Binaries | B. Submodule | C. Full fork | D. Go module (+ fork on demand) | E. Vendored | F. Images |
|---|---|---|---|---|---|---|
| R1 own name and version | Renamed file only; reports Temporal's version | Build-time rename | Yes | Yes: own binary, own version, Temporal version reported alongside | Yes | No |
| R2 dependency or Go CVE without upstream | No: wait for Temporal | Yes, by patch file | Yes | Yes: `go get dep@fixed`, rebuild with a patched Go | Yes | No |
| R3 fix Temporal code | No | Yes, patch file rebased on each upgrade | Yes | Yes, through the fork and `replace` only while needed | Yes, merge pain | No |
| R4 our plugins and auth | No | Patch files | Commits in fork | Separate Go packages, registered through Temporal's public extension points: no fork | Yes | No |
| R5 one-minor upgrades and schema | Upstream tools | Upstream tools | Upstream tools | `workflow-engine-sql`, built from the same pinned version | Yes | Upstream tools |
| R6 reproducible build, SBOM | Upstream's binary; SBOM from release metadata | Yes | Yes | Yes: `go.mod`/`go.sum` is the bill of materials | Yes | Upstream |
| R7 VM binary and images | Yes | Yes | Yes | Yes: static binary (`CGO_ENABLED=0`), any base image | Yes | Images only |
| R8 per-service start | Yes (`--service`) | Yes | Yes | Yes (`--service`) | Yes | Yes |
| R9 cost | Lowest, until the first patch we need | Patch rebase every upgrade; submodule friction | Highest: merge every release | Low; rises only while a fork patch is open | High | Lowest, VM installs unsupported |

A and F cannot meet R2 or R3: WSO2 would ship a known CVE until Temporal releases. B and C meet everything but
make every upgrade a rebase or merge, even when we have no patches. E is C without the tooling.

## Decision (proposed): D

`engine/` is a Go module that depends on `go.temporal.io/server`. It builds two binaries:

- `workflow-engine`: starts the services named by `--service` (the launcher passes them per profile) from a
  configuration file the launcher writes from the runtime's own settings. It refuses to start without an
  authorizer unless `--allow-no-auth` is given.
- `workflow-engine-sql`: creates and migrates Temporal's schema (Temporal's `tools/sql`), from the same version.

Checked against Temporal v1.32.1 (the current release):

- The server is embeddable: `temporal.NewServer(temporal.ForServices(...), temporal.WithConfig(...), ...)`,
  the same entry point upstream's `cmd/server` uses.
- Extension points without a fork: `sql.RegisterPlugin` for new SQL databases, `temporal.WithCustomDataStoreFactory`,
  `temporal.WithAuthorizer`, `temporal.WithClaimMapper`, `temporal.WithAudienceGetter`.
- Temporal itself builds with `CGO_ENABLED=0` (its Makefile default), so the engine is a static binary.
- v1.32.1 requires Go 1.26.8 or later.
- Built and run on 2026-10-16 (macOS arm64): `workflow-engine version` reports `0.1.0-SNAPSHOT (Temporal server
  1.32.1)`; started on Temporal's SQLite development config, it served cluster health, created a namespace and
  listed workflows through the Temporal CLI. It refuses to start with no authorizer unless `--allow-no-auth`.
- Size: `workflow-engine` is 208 MB and `workflow-engine-sql` 58 MB unstripped; `-ldflags "-s -w"` (no symbol
  tables) is the usual reduction for release builds.

What D does not remove: we still triage Temporal and Go CVEs, own the Go toolchain version, and keep an eye on
the embedding API (Temporal does not promise it is stable; a bump can need small changes in `main.go`).

## Patching and upgrade playbook

| Case | What we do | Ships as |
|---|---|---|
| **1. Temporal releases a patch** (bug or CVE fix in a minor we ship) | Bump `go.temporal.io/server` in `engine/go.mod`, rebuild, run contract and upgrade tests | Engine update |
| **2. CVE in a Go dependency, Temporal has not released** | `go get <module>@<fixed>` in `engine/go.mod` (Go picks the highest required version), rebuild, run the tests; upstream PR to bump it there too | Engine update |
| **3. CVE in the Go toolchain or standard library** | Rebuild with the patched Go release. Only possible because we build | Engine update |
| **4. Temporal bug, not fixed upstream** | Commit the fix on the fork's `release/v1.32.x-wso2` branch, tag `v1.32.1-wso2.1`, add `replace go.temporal.io/server => <fork module> v1.32.1-wso2.1`; open the upstream PR (Temporal's CLA applies). Drop the `replace` when upstream releases the fix | Engine update |
| **5. Temporal will not take our change** | Keep it in the fork and carry it forward on each upgrade; prefer an extension point (case 7) where one exists | Engine update |
| **6. Upgrade to the next minor** | One minor at a time, as Temporal requires. Bump the pin, run `workflow-engine-sql update-schema` against a copy of the previous release's database in the upgrade tests. The launcher refuses to start an engine more than one minor ahead of the version recorded in the Runtime DB | Product release |
| **7. Our own feature** (SQL Server or Oracle plugin, authorizer, config) | A package under `engine/plugins/`, registered from `main.go`. No fork | Product release |
| **8. Fix for an older product release** | Each product release line has a maintenance branch (`release/1.0.x`) with its own `engine/go.mod` pin; backport there | Update for that line |
| **9. Advisory with no fix available** | Mitigate by configuration (disable the feature, network rule), document it in the advisory, track upstream | Configuration change |

Delivery: the engine is one static binary, so an update replaces it whole. The launcher restarts the affected
profile one node at a time; Manager and portal updates do not restart the engine, and the reverse.

Versions: `workflow-engine version` prints the product version and the embedded Temporal version; both go into the
release notes and the SBOM. A fork build carries the `-wso2.N` suffix, so a support engineer can see at once that
a release contains our patch.

## Fork conventions (case 4 and 5 only)

- The fork's repository is to be decided, and is created when the first patch is needed, not before. Builds of
  this repository must be able to fetch it, so it has to be public or reachable from CI.
- Branch per upstream line: `release/v1.32.x-wso2`, starting at the upstream tag.
- Tags `v1.32.1-wso2.1`, `-wso2.2`, … The fork's `go.mod` keeps `module go.temporal.io/server`, which is what
  `replace` needs.
- Every fork commit references its upstream PR or the reason it cannot go upstream.
- Fork changes stay under Temporal's MIT license (see 0002), so they can go upstream unchanged.

## Consequences

- The product repository stays small: `engine/` is two `main` packages and our plugins.
- Every Temporal or dependency bump is a reviewed `go.mod` change, which is also the audit trail for security
  fixes.
- Go build, vulnerability scanning (`govulncheck`) and a license check run in CI on every pull request.
- The fork exists only while we carry a patch, so most upgrades are a version bump.

## Open points

- How long each Temporal minor stays supported in our maintenance branches.
- How engine updates replace the Go binary on customer VMs, alongside the WSO2 update tooling.
