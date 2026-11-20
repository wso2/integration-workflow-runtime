---
title: Workflow Runtime
description: What the WSO2 Integration Workflow Runtime is and how integrations use it.
keywords: [wso2 integrator, workflow runtime, durable workflows, temporal]
---

# Workflow Runtime

The Workflow Runtime runs the durable workflows of WSO2 Integrator. Integrations that use the `ballerina/workflow`
module connect to it, start workflows, and run their workflow and activity code; the runtime keeps every
workflow's state, timers, retries and history, so a workflow survives restarts and can wait for days.

The runtime's engine is built from the open-source Temporal server and is supported by WSO2. You configure it with
one file, `conf/runtime.toml`.

## Parts

| Part | What it does |
|---|---|
| Engine (`workflow-engine`) | Temporal's services: the frontend integrations connect to (port 7233), history, matching, and a worker for the engine's own housekeeping |
| Database | Workflow state and history (the workflow database) and workflow search (the visibility database): PostgreSQL or MySQL in production, SQLite for trying out |
| Your identity provider | Issues the tokens integrations and operators present; the runtime keeps no users or roles of its own |

## How integrations use it

1. An integration's `Config.toml` names the runtime's address, a namespace, and its token
   ([Connect an integration](connect-an-integration.md)).
2. At start, the integration's workers connect to the engine and wait for work on their task queues.
3. The integration starts workflows; the engine schedules each step on a worker and records the result.

Namespaces separate teams or environments. Each has a retention period after which closed workflows are deleted.

## Where to go next

- [Quick start](quick-start.md)
- [Configure the runtime](configure.md)
- [Secure the engine](secure-the-engine.md)
- [Run across several nodes](scale-across-nodes.md)
