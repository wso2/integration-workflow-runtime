---
title: Run across several nodes
description: Split the Workflow Runtime's services across machines with profiles.
keywords: [wso2 integrator, workflow runtime, profiles, high availability, scaling, cluster]
---

# Run across several nodes

The same distribution runs on one machine or many. Each node runs the profiles you give it, and nodes find each
other through the shared database: there is no list of peers to maintain.

## Profiles

| Profile | Runs |
|---|---|
| `all` (default), `engine` | Every engine service |
| `frontend` | The frontend integrations connect to, and the internal frontend the engine's own services use |
| `history` | Workflow state and timers; the busiest tier |
| `matching` | Task queues |
| `worker` | The engine's housekeeping |

## Set up the nodes

1. Use the same `conf/runtime.toml` on every node (same `[database]`, same `historyShards`, same `[engine.auth]`).
2. On each node, set its profiles and its own address:

   ```toml
   [runtime]
   profiles = ["history", "matching"]

   [engine]
   bindAddress = "0.0.0.0"
   broadcastAddress = "10.0.1.21"   # this node's IP address
   ```

3. Open the ports between nodes: the service ports (7233 to 7239 by default) and their membership ports (6933 to
   6939). Integrations need only 7233. With `[engine.auth]`, turn on `[engine.tls.internode]` on every node: the
   engine refuses to start without it, because the internal ports accept calls without a token
   ([Secure the engine](secure-the-engine.md#turn-on-tls)).
4. Put a load balancer in front of the frontend nodes. Integrations keep long-lived gRPC connections, so use a
   TCP (layer 4) load balancer, such as an AWS Network Load Balancer.

A starting point: two frontend nodes, three history nodes, two matching nodes and one worker node. Grow history
with workflow throughput and frontend with the number of connected workers.

:::info
`--service frontend --service history` on the command line overrides the profiles for one start.
:::
