# workflow-cli

Status: in progress · P0 (cluster, namespaces, search attributes, profiles) since 0.1.0

## What it does

`workflow-cli` is the runtime's command line client for operators: it checks the engine's services, lists the nodes
behind each, and manages namespaces and search attributes. Customers who can run only WSO2-supported tools use it
instead of the Temporal CLI. Its commands and flags follow the Temporal CLI's, so `temporal operator namespace list`
is `workflow-cli operator namespace list`.

It is a separate binary in a module of its own (`cli/`), not a command of `workflow-runtime`: the server binary
holds no client code, and the client holds no server code. It talks to the engine over the Temporal gRPC API only
(`go.temporal.io/api`, and Temporal's admin API stubs for the node list), never to the database, and reads no
`runtime.toml`.

User guide: [Manage the engine with workflow-cli](../user-guide/manage-with-workflow-cli.md) and the
[reference](../user-guide/reference/workflow-cli.md).

## Configuration

| Setting | Default | Meaning |
|---|---|---|
| `--address`, `WORKFLOW_ADDRESS`, `address` | `localhost:7233` | The frontend, or one service's port for `cluster health` |
| `--namespace`, `WORKFLOW_NAMESPACE`, `namespace` | `default` | The namespace |
| `--api-key`, `--api-key-file`, `WORKFLOW_API_KEY`, `WORKFLOW_API_KEY_FILE`, `apiKey`, `apiKeyFile` | | Bearer token from the identity provider |
| `--tls`, `--tls-ca-path`, `--tls-cert-path`, `--tls-key-path`, `--tls-server-name` (and `WORKFLOW_TLS*`, `tls.*`) | off | TLS, on with any TLS setting |
| `--profile`, `WORKFLOW_PROFILE` | `default` | The profile in the profile file |
| `--config-file`, `WORKFLOW_CONFIG_FILE` | `<user config dir>/wso2-workflow/cli.toml` | The profile file (TOML, `[profile.<name>]` tables) |
| `-o` | `text` | `text` or `json` (protojson) |
| `--command-timeout` | `30s` | Limit per command |

Precedence, as in the Temporal CLI: flag, environment variable, profile, default.

## Behavior

- `operator cluster health [--service]` calls gRPC health with the name each Temporal service registers
  (`temporal.api.workflowservice.v1.WorkflowService` for the frontend and internal frontend, `...HistoryService`,
  `...MatchingService`, `...WorkerService`); not serving exits with 1. History reports serving only after it holds
  its shards, plus 5 seconds.
- `operator cluster describe` calls the admin API's `DescribeCluster` for the node list; when the token may not
  (`PermissionDenied`), it falls back to `GetClusterInfo` and says the node list needs an admin token.
- `operator namespace create` registers the namespace (retention 720h by default, as the release plan's namespaces)
  and adds `WorkflowKind` (Keyword), retrying while the new namespace is not yet in the engine's namespace cache
  (`NotFound`, `Unavailable`). An existing namespace (`AlreadyExists`) is reported and left unchanged, and the
  attribute is still ensured; the engine skips attributes that exist.
- A token goes only over TLS, or in plain text to a loopback address; otherwise the command refuses before dialing.
- The profile file is written atomically, mode 0600 in a 0700 directory; unknown properties in it are errors.
  `config get` shows an API key as `(set)`.
- The container image includes it, so compose health checks and the try-out's namespace setup use it
  (`distribution/docker/compose.yml`).

## Limits

- Only the P0 commands: workflow, task queue, schedule, batch and dead-letter commands come later (P1, P2).
- A token is read once per command; refreshing an expiring one is up to the caller (`apiKeyFile` rewritten by a
  token helper works).

## Not covered yet

P1: `workflow list|describe|show|signal|cancel|terminate`, `task-queue describe`. P2: `workflow reset`, batch
operations, schedules, dead-letter queues and shards (Temporal's `tdbg`). A token command for the API server, once
it exists.

## Tests

| Test | Covers |
|---|---|
| `cli/internal/command/command_test.go` | Every command against an in-memory fake engine (bufconn): health per service and exit codes, describe with and without the admin API, namespace create (cache retry, idempotence), describe, list over pages, update, search attributes, profile/environment/flag precedence and the bearer header, `config` commands and file mode, error messages |
| `cli/internal/command/dial_test.go` | Mutual TLS against a real TLS server, refusal without a client certificate or TLS, token refused in plain text off-loopback, CA, client certificate and key file errors |
| `cli/internal/clientconfig/clientconfig_test.go` | Profile file parsing, unknown keys, environment variables, TLS on/off rules, writing and deleting properties, profile names that would nest refused with the file left readable |
| `tools/internal/enginetest/workflowcli_test.go` | Against the real engine: every P0 command on `start-dev`; on a secured engine, profiles with admin and namespace tokens, refusals, a token never sent in plain text, and the health of each internal port with a node certificate |
