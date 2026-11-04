# Distribution

Status: available · since 0.1.0 (in development)

## What it does

Ships the runtime as a zip per operating system and architecture, and as one multi-architecture container image for
every profile. Both carry the same binaries, the one configuration file, SBOMs and third-party licenses.

## Configuration

| Item | Meaning |
|---|---|
| `bin/workflow-runtime` (`.exe` on Windows) | The launcher: starts the engine from `conf/runtime.toml`; `render-config`, `version` and other engine commands pass through. `WORKFLOW_RUNTIME_CONFIG` names another file |
| `conf/runtime.toml` | The one file to edit ([runtime configuration](runtime-config.md), [engine security](engine-security.md)) |
| `systemd/workflow-runtime.service` | Example unit for Linux virtual machines (zip for Linux only) |
| Test image `ghcr.io/wso2/integration-workflow-runtime:<nightly version>` and `:nightly` | Published by every push to `main` (and on demand for a branch) for testing |
| Container image (WSO2's registry from the first release) | Distroless, non-root; mount `runtime.toml` at `/opt/wso2-workflow-runtime/conf/runtime.toml` |
| `distribution/docker/compose.yml` | Tries the runtime with PostgreSQL on one machine |
| `make dist` / `make image` | Build a zip for `GOOS`/`GOARCH`, or the image from the linux zips (`IMAGE`, `IMAGE_PLATFORMS`, `IMAGE_ARGS`) |

## Behavior

- The launcher finds the runtime's home from its own location (symbolic links resolved) and works from there, so
  relative paths in `runtime.toml` (certificates, log file, SQLite file) resolve against it. On Linux and macOS it
  replaces itself with the engine, so signals and the exit code are the engine's; on Windows it runs the engine as
  a child sharing the console.
- The image's entry point is the launcher, so it starts the engine from `/opt/wso2-workflow-runtime/conf/runtime.toml`;
  arguments after the image name are passed to it (for example `--allow-no-auth` when trying it out).
- In a container, set `bindAddress = "0.0.0.0"` and `broadcastAddress = "auto"`: the engine advertises the
  container's own address to the other nodes.
- A release tag builds the zips for Linux, macOS and Windows on amd64 and arm64 and signs them with Sigstore. When
  the repository variable `WORKFLOW_RUNTIME_IMAGE` names the image in WSO2's registry (with the secrets
  `IMAGE_REGISTRY_USERNAME` and `IMAGE_REGISTRY_PASSWORD`), it also publishes the image for linux/amd64 and
  linux/arm64, signed the same way. The image is assembled from the linux zips, so its binaries are the released
  ones.

## Deploying on AWS

| Choice | Recommendation |
|---|---|
| Database | Amazon RDS for PostgreSQL (12 or later) or MySQL (8.0.17 or later), or Aurora PostgreSQL or Aurora MySQL 3; turn on `[database.tls]` with the RDS certificate bundle |
| Compute | EC2 with the zip and the systemd unit, or the image on ECS or EKS |
| Load balancer | A Network Load Balancer (TCP) in front of the frontends on 7233: integrations hold long gRPC connections |
| Several nodes | The same `runtime.toml` everywhere, with `profiles` per node and `broadcastAddress` (or `auto`); open the service ports (7233-7239) and their membership ports (6933-6939) between nodes |
| Identity | Amazon Cognito or the customer's provider in `[engine.auth]`; Cognito groups are in the `cognito:groups` claim |

## Limits

- Without `[engine.auth]`, a start needs `--allow-no-auth`, which is for trying out only.

## Not covered yet

Patches through WSO2 Updates, Helm charts, and supervision and restart outside systemd or a container platform.

## Tests

| Test | Covers |
|---|---|
| `engine/internal/launcher/launcher_test.go` | The launcher's home, working directory, configuration, commands, help and errors |
| `engine/internal/launcher/process*_test.go` | The real handoff in separate processes: arguments, environment, stdin and exit code reach the engine; on Unix the engine replaces the launcher and gets its signals. Runs on Linux, macOS and, in its own CI job, Windows |
| `engine/internal/runtimeconfig/security_test.go` | `broadcastAddress = "auto"` and `logFile` |
