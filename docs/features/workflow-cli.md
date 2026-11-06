# workflow-cli

Status: in progress · P0 (cluster, namespaces, search attributes, profiles) and P1 (workflows, task queues) since 0.1.0

## What it does

`workflow-cli` is the runtime's command line client for operators: it checks the engine's services, lists the nodes
behind each, manages namespaces and search attributes, and lists, inspects, signals, cancels and terminates workflows. Customers who can run only WSO2-supported tools use it
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

Precedence, as in the Temporal CLI: flag, environment variable, profile, default. The token and the token file are one
setting (`clientconfig.ReplaceCredential`): a level that sets either replaces both, and within a level the token wins.

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
- `workflow list` and `count` take Temporal's visibility query; `list` pages through 100 at a time up to `--limit`
  (100; 0 for all). `show` pages through the whole history. `signal` encodes each `--input` as a `json/plain`
  payload after checking it is JSON; `signal` and `cancel` send a fresh request ID, and every change carries the
  identity `workflow-cli@<host>`, which the workflow's history records.
- `task-queue describe` calls `DescribeTaskQueue` once for workflow and once for activity tasks and lists their
  pollers.
- A token goes only over TLS, or in plain text to a loopback address; otherwise the command refuses before dialing.
- The profile file is written atomically, mode 0600 in a 0700 directory; unknown properties in it are errors.
  `config get` shows an API key as `(set)`.
- The container image includes it, so compose health checks and the try-out's namespace setup use it
  (`distribution/docker/compose.yml`).

## Limits

- Workflow start, reset, batch, schedule and dead-letter commands come later (P2).
- A token is read once per command; refreshing an expiring one is up to the caller (`apiKeyFile` rewritten by a
  token helper works).

## Not covered yet

P2: `workflow reset`, batch operations, schedules, dead-letter queues and shards (Temporal's `tdbg`). A token command for the API server, once
it exists.

## Tests

| Test | Covers |
|---|---|
| `cli/internal/command/command_test.go` | Every command against an in-memory fake engine (bufconn): health per service and exit codes, describe with and without the admin API, namespace create (cache retry, idempotence), describe, list over pages, update, search attributes, profile/environment/flag precedence and the bearer header, a token file replacing an earlier level's token, `config` commands and file mode, error messages |
| `cli/internal/command/workflow_test.go` | `workflow list` over pages with a query and `--limit`, `count`, `describe`, `show` over pages, `signal` payloads, identity and request ID, `describe` and `show` sending `-r`, `cancel` and `terminate` with reasons and run IDs, confirmations on stderr with `-o json`, `task-queue describe` with and without pollers, error messages |
| `cli/internal/command/dial_test.go` | Mutual TLS against a real TLS server, refusal without a client certificate or TLS, token refused in plain text off-loopback, CA, client certificate and key file errors |
| `cli/internal/clientconfig/clientconfig_test.go` | Profile file parsing, unknown keys, environment variables, TLS on/off rules, writing and deleting properties, profile names that would nest refused with the file left readable |
| `tools/internal/enginetest/workflowcli_test.go` | Against the real engine: every P0 and P1 command on `start-dev` (workflows started on a task queue no worker polls: listed by `WorkflowKind`, described, signalled, cancelled and terminated, their history shown); on a secured engine, profiles with admin and namespace tokens, refusals, a token never sent in plain text, a namespace token listing only its own namespace and counting a workflow started in it, and the health of each internal port with a node certificate |
