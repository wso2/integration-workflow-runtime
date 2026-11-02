---
title: Monitor the runtime
description: Metrics, logs and workflow visibility for the Workflow Runtime.
keywords: [wso2 integrator, workflow runtime, monitoring, prometheus, metrics, logs, icp]
---

# Monitor the runtime

## Metrics

Serve Prometheus metrics from each node:

```toml
[engine.metrics]
prometheusAddress = "0.0.0.0:9464"
```

Scrape `http://<node>:9464/metrics`. The metrics are Temporal's server metrics (requests, latencies, task queue
backlogs, persistence calls).

## Logs

The engine logs JSON to standard output at `logLevel` (`info` by default; `debug`, `warn` or `error`), which
systemd, Docker and Kubernetes collect. To write to a file instead, set `logFile = "logs/engine.log"` in
`[engine]`; the engine creates a missing directory and refuses to start when it cannot write the file.

During the first two minutes after a start, errors Temporal logs while its services join each other and acquire
their shards (`Not enough hosts to serve the request`, `shard status unknown` and similar) are logged at debug level:
they resolve themselves. After that they are logged as errors again, so a lasting problem still shows. Refused calls are logged with the reason, which the caller does not see.

## Workflows

Use the Integration Control Plane (ICP) to see integrations' workflows, their status and history. The Temporal CLI
works too, with a token that has `read` in the namespace:

```bash
temporal workflow list --namespace orders --address engine.example.com:7233 \
  --tls-ca-path ca.pem --api-key "$TOKEN"
```
