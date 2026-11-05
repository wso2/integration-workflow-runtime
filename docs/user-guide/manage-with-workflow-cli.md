---
title: Manage the engine with workflow-cli
description: Check the engine, manage namespaces, and find, inspect and act on workflows with workflow-cli.
keywords: [wso2 integrator, workflow runtime, workflow-cli, cli, namespace, search attribute, health check, cluster]
---

# Manage the engine with workflow-cli

`workflow-cli` is the runtime's command line client. It checks the engine's health, lists the nodes behind each
service, manages namespaces and their search attributes, and finds, inspects and acts on the workflows integrations
run. It is a client like an integration: it talks only to
the engine's frontend (or, for health checks, to one service's port), never to the database.

It is in the distribution's `bin/` directory and in the container image. Copy the one file to any machine that can
reach the engine; it needs nothing else.

:::tip If you know the Temporal CLI
The commands and flags are the Temporal CLI's: `temporal operator namespace create` is
`workflow-cli operator namespace create`, with the same `--address`, `--namespace`, `--tls-*` and `--api-key` flags.
:::

## Connect to the engine

With no settings, `workflow-cli` reaches a development engine on `localhost:7233`. For any other engine, save the
connection in a profile once:

```bash
workflow-cli config set --prop address --value engine.example.com:7233
workflow-cli config set --prop tls.caPath --value certs/engine-ca.pem
workflow-cli config set --prop apiKeyFile --value ~/.config/wso2-workflow/admin.token
```

The token is one from your identity provider whose groups map to a role ([Secure the engine](secure-the-engine.md));
creating namespaces needs `admin`. `workflow-cli` sends it only over TLS, or in plain text only to this machine.

Keep one profile per environment and pick one with `--profile`:

```bash
workflow-cli config set --profile staging --prop address --value engine.staging.example.com:7233
workflow-cli operator namespace list --profile staging
```

A flag wins over an environment variable (`WORKFLOW_ADDRESS`, `WORKFLOW_API_KEY` and so on), which wins over the
profile. The profile file is `<your configuration directory>/wso2-workflow/cli.toml` (`~/.config` on Linux,
`~/Library/Application Support` on macOS, `%AppData%` on Windows), readable by you only. See the
[workflow-cli reference](reference/workflow-cli.md) for every setting.

## Check the engine

```bash
workflow-cli operator cluster health
```

prints `SERVING`, or `NOT_SERVING` and exits with 1. With `--service` and a node's address it checks one service on
its own port, which is what a load balancer or container health check needs:

```bash
workflow-cli operator cluster health --service history --address 10.0.1.21:7234 \
  --tls-ca-path conf/certs/internode-ca.pem --tls-cert-path conf/certs/node.crt --tls-key-path conf/certs/node.key
```

The service ports other than 7233 take only internode certificates when internode TLS is on, as above. History
reports `SERVING` a few seconds after it holds its shards, so use it as a readiness check.

```bash
workflow-cli operator cluster describe
```

shows the cluster's version, history shards and stores, and the nodes behind each service:

```text
Cluster         active
Server version  1.32.1
History shards  512
...

SERVICE            NODES  ADDRESSES
frontend           2      10.0.1.11:7233, 10.0.1.12:7233
history            3      10.0.1.21:7234, 10.0.1.22:7234, 10.0.1.23:7234
```

The node list needs a token with the `admin` role in every namespace (`namespace = "*"`); with another token the
cluster is shown without it.

## Create a namespace for integrations

```bash
workflow-cli operator namespace create --namespace orders
```

creates `orders`, keeps its closed workflows for 30 days, and adds the `WorkflowKind` search attribute
`ballerina/workflow` needs. Running it again changes nothing, so it is safe in setup scripts. Set another
retention with `--retention 168h`.

```bash
workflow-cli operator namespace list
workflow-cli operator namespace describe --namespace orders
workflow-cli operator namespace update --namespace orders --retention 2160h --description "Order processing"
```

## Add search attributes

Integrations that tag workflows with search attributes of their own need them in the namespace first:

```bash
workflow-cli operator search-attribute create --namespace orders --name Customer --type Keyword
workflow-cli operator search-attribute list --namespace orders
```

Types are `Keyword`, `Text`, `Int`, `Double`, `Bool`, `Datetime` and `KeywordList`. An attribute that exists already
is left as it is.

## Find and inspect workflows

```bash
workflow-cli workflow list --namespace orders --query 'ExecutionStatus="Running" AND WorkflowKind="orders.Order"'
workflow-cli workflow count --namespace orders --query 'ExecutionStatus="Failed"'
workflow-cli workflow describe --namespace orders --workflow-id order-1042
workflow-cli workflow show --namespace orders --workflow-id order-1042
```

`list` shows the newest 100 by default (`--limit 0` for all). The query is Temporal's visibility query language:
`WorkflowId`, `WorkflowType`, `ExecutionStatus`, `StartTime`, `CloseTime` and any search attribute, such as the
`WorkflowKind` integrations set. `describe` shows the task queue, status and pending activities; `show` lists the
events of its history, the place to look when a workflow does not move. Add `--run-id` for a run other than the
latest.

A token with `read` in the namespace is enough for these.

## Act on a workflow

```bash
workflow-cli workflow signal -n orders -w order-1042 --name approval --input '{"approved": true}'
workflow-cli workflow cancel -n orders -w order-1042 --reason "customer withdrew"
workflow-cli workflow terminate -n orders -w order-1042 --reason "stuck on a retired version"
```

`signal` delivers the event the workflow waits for, each `--input` a JSON argument. `cancel` asks the workflow to
stop and lets it clean up; `terminate` ends it at once, running nothing more. Both record the reason in its
history. These need `write` in the namespace.

## Act on many workflows at once

Give `signal`, `cancel` or `terminate` a `--query` instead of a `--workflow-id`, and the engine works through every
workflow it matches as a batch job:

```bash
workflow-cli workflow terminate -n orders -q 'WorkflowType="orders.Order" AND ExecutionStatus="Running"' \
  --reason "retire version 1"
```

Without `--yes` it only says how many workflows match. With it, it prints the job's ID; follow and stop the job with:

```bash
workflow-cli batch describe -n orders --job-id <job id>
workflow-cli batch list -n orders
workflow-cli batch terminate -n orders --job-id <job id> --reason "wrong query"
```

## Run a workflow again from an earlier point

```bash
workflow-cli workflow reset -n orders -w order-1042 --type LastWorkflowTask --reason "after the payment fix"
```

starts a new run of the workflow from the end of its last workflow task (`FirstWorkflowTask` for its first one, or
`--event-id` for a `WorkflowTaskCompleted` event from `workflow show`), and ends the current run. Signals received
after that point are delivered to the new run again. Use it after fixing the integration's code that made the
workflow fail or stall.

## Start workflows on a schedule

```bash
workflow-cli schedule create -n reports -s nightly-report --cron '0 2 * * *' \
  --workflow-id nightly-report --type reports.Nightly --task-queue reports
workflow-cli schedule list -n reports
workflow-cli schedule describe -n reports -s nightly-report
workflow-cli schedule toggle -n reports -s nightly-report --pause --reason "month-end freeze"
workflow-cli schedule trigger -n reports -s nightly-report
workflow-cli schedule delete -n reports -s nightly-report
```

`--cron` and `--interval 30m` can repeat. `--overlap-policy` says what to do when a run is still open at the next
time (`Skip` by default). Prefix a cron with `CRON_TZ=Europe/London` for a time zone other than UTC. `describe`
shows the calendar as cron fields, in the form the engine stores it (`@daily` shows as `0 0 * * *`), its time
zone, the recent runs and the next one. The engine runs the schedule itself;
the integration only needs to serve the task queue.

## Check an integration's workers

```bash
workflow-cli task-queue describe --namespace orders --task-queue orders
```

lists the workers that polled the task queue in the last five minutes, for workflow and for activity tasks. Workflows
that stay `Running` without progress while this lists no worker are waiting for an integration to poll: one that
is not running, or one that cannot reach the engine.

## Repair the engine

The `admin` commands are for incidents. They call the engine's admin API, so they need a token with the `admin`
role in every namespace (`namespace = "*"`).

**Dead-letter queues.** A history task that keeps failing (for example, while the database was unreachable) is moved
to a dead-letter queue so it stops blocking others. On a healthy engine there are none:

```bash
workflow-cli admin dlq list
workflow-cli admin dlq read --dlq-type transfer
workflow-cli admin dlq merge --dlq-type transfer --yes    # run them again once the cause is fixed
workflow-cli admin dlq purge --dlq-type transfer --yes    # or drop them
workflow-cli admin dlq job describe --job-token <token>
```

`merge` and `purge` work up to the queue's last task, or up to `--last-message-id` (message IDs start at 0, the
ID `dlq read` shows), as a job on the engine; tasks that fail again during a merge return to the queue.

**Shards.** Workflow state is spread over the history shards (`historyShards` in `runtime.toml`), each owned by one
history node at a time:

```bash
workflow-cli admin shard describe --shard-id 42
workflow-cli admin shard close --shard-id 42
```

`describe` names the node that owns the shard. `close` makes that node load the shard again from the database, for
a shard whose workflows stopped making progress, for example after a database failover.

## Use it in scripts

Every command takes `-o json` for the Temporal API's JSON form, and exits with 1 on any error, the message on
standard error.
