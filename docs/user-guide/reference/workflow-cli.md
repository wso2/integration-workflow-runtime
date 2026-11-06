---
title: workflow-cli reference
description: Every workflow-cli command, flag, environment variable and profile setting.
keywords: [wso2 integrator, workflow runtime, workflow-cli, reference, flags, profiles]
---

# workflow-cli reference

## Commands

| Command | Does |
|---|---|
| `operator cluster health [--service <name>]` | Checks one service's gRPC health at `--address`: `frontend` (default), `internal-frontend`, `history`, `matching` or `worker`. Prints `SERVING`, or exits with 1 |
| `operator cluster describe` | The cluster's name, ID, version, history shards and stores, and the nodes behind each service (the node list needs an `admin` token for every namespace) |
| `operator namespace create [--retention <duration>] [--description <text>]` | Creates `--namespace` (retention 720h, 30 days, by default) with the `WorkflowKind` search attribute; an existing namespace is left unchanged |
| `operator namespace list` | Every namespace with its state, retention and description |
| `operator namespace describe` | One namespace |
| `operator namespace update [--retention <duration>] [--description <text>]` | Changes the retention or the description; the engine cannot clear a description, so an empty one is refused |
| `operator search-attribute create --name <name> --type <type> ...` | Adds search attributes, each `--name` with the `--type` in the same position; existing ones are left unchanged |
| `operator search-attribute list` | The namespace's custom and system search attributes |
| `workflow list [--query <query>] [--limit <n>]` | Workflows, newest first, with status, ID, type and start time; `--limit` 100 by default, 0 for all |
| `workflow count [--query <query>]` | How many workflows match |
| `workflow describe -w <id> [-r <run>]` | A workflow's run, type, task queue, status, times, history length, and pending activities and child workflows |
| `workflow show -w <id> [-r <run>]` | Its event history: ID, time and type of each event |
| `workflow signal (-w <id> \| -q <query> -y) --name <signal> [-i <json>]...` | Sends a signal, each `--input` a JSON argument (`json/plain`) |
| `workflow cancel (-w <id> \| -q <query> -y) [--reason <text>]` | Requests cancellation; the workflow can clean up. `--reason` is required with `--query` |
| `workflow terminate (-w <id> \| -q <query> -y) [--reason <text>]` | Ends the workflow at once. `--reason` is required with `--query` |
| `workflow reset -w <id> (--type FirstWorkflowTask\|LastWorkflowTask \| --event-id <n>) --reason <text>` | A new run from the end of that workflow task |
| `batch list` / `batch describe --job-id <id>` / `batch terminate --job-id <id> --reason <text>` | The batch jobs a `--query` started: their state and counts; stop one |
| `schedule create -s <id> (--cron <expr> \| --interval <duration>)... --workflow-id <id> --type <type> -t <task queue> [-i <json>]... [--overlap-policy <policy>] [--paused] [--notes <text>]` | Creates a schedule |
| `schedule list` / `schedule describe -s <id>` | Schedules with their type, state and next run; one with its calendar as cron fields, time zone, intervals and recent runs |
| `schedule toggle -s <id> (--pause \| --unpause) [--reason <text>]` / `schedule trigger -s <id>` / `schedule delete -s <id>` | Pause or unpause, start once now, delete |
| `task-queue describe -t <task queue>` | The workers that polled it recently, for workflow and activity tasks |
| `config set --prop <property> --value <value>` | Sets a property of the profile, creating the profile |
| `config get [--prop <property>]` | Shows the profile (an API key as `(set)`), or one property |
| `config delete --prop <property>` | Removes a property |
| `config delete-profile` | Removes the profile |
| `config list` | The profile names |
| `version` | The version |

Durations are Go durations: `720h`, `90m`, `36h30m`.

## Flags for every command

| Flag | Default | Meaning |
|---|---|---|
| `--address` | `localhost:7233` | The engine's frontend, `host:port`, or one service's port for `cluster health` |
| `-n`, `--namespace` | `default` | The namespace |
| `--api-key` | | A token from your identity provider, sent as `Authorization: Bearer` |
| `--api-key-file` | | A file holding the token (its first line) |
| `--tls` | off | Connect with TLS; on with any other `--tls-*` flag |
| `--tls-ca-path` | the system's | The CA that signed the engine's certificate |
| `--tls-cert-path`, `--tls-key-path` | | A client certificate, for an engine that requires one or for the internal ports |
| `--tls-server-name` | the address's host | The name to verify in the engine's certificate |
| `--profile` | `default` | The profile to use: letters, digits, `-` and `_` |
| `--config-file` | see below | The profile file |
| `-o`, `--output` | `text` | `text` or `json`. With `json`, a command that changes something prints its confirmation on stderr and nothing on stdout; the exit status is the result |
| `--command-timeout` | `30s` | Limit for each command |

A token is sent only over TLS, or in plain text only to a loopback address.

## Environment variables and profiles

Each setting comes from, in order: the flag, its environment variable, the profile, the default.

The token and the token file count as one setting: a flag or environment variable that gives either one replaces
both from the levels below, so `--api-key-file` is used even when the profile holds an `apiKey`. When one level
gives both, the token wins.

| Profile property | Environment variable | Flag |
|---|---|---|
| `address` | `WORKFLOW_ADDRESS` | `--address` |
| `namespace` | `WORKFLOW_NAMESPACE` | `--namespace` |
| `apiKey` | `WORKFLOW_API_KEY` | `--api-key` |
| `apiKeyFile` | `WORKFLOW_API_KEY_FILE` | `--api-key-file` |
| `tls.enabled` | `WORKFLOW_TLS` | `--tls` |
| `tls.caPath` | `WORKFLOW_TLS_CA_PATH` | `--tls-ca-path` |
| `tls.certPath` | `WORKFLOW_TLS_CERT_PATH` | `--tls-cert-path` |
| `tls.keyPath` | `WORKFLOW_TLS_KEY_PATH` | `--tls-key-path` |
| `tls.serverName` | `WORKFLOW_TLS_SERVER_NAME` | `--tls-server-name` |

`WORKFLOW_PROFILE` chooses the profile and `WORKFLOW_CONFIG_FILE` the file. The file is
`<user configuration directory>/wso2-workflow/cli.toml`, written readable by its owner only:

```toml
[profile.default]
address = "engine.example.com:7233"
namespace = "orders"
apiKeyFile = "/home/ops/.config/wso2-workflow/admin.token"

[profile.default.tls]
caPath = "/home/ops/certs/engine-ca.pem"
```

An unknown property in the file is an error, so a typo does not go unnoticed.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Done; for `cluster health`, the service is serving |
| 1 | An error, or a service that is not serving |
