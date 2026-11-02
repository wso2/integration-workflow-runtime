---
title: Run in containers
description: Run the Workflow Runtime's container image with Docker, Docker Compose, Amazon ECS or Kubernetes.
keywords: [wso2 integrator, workflow runtime, docker, container, compose, kubernetes, ecs]
---

# Run in containers

One image serves every profile, for linux/amd64 and linux/arm64. It runs as a non-root user from
`/opt/wso2-workflow-runtime`, and starts the engine with `/opt/wso2-workflow-runtime/conf/runtime.toml`. Mount your
configuration there.

## In a container, set the addresses

The container's IP address is known only at start, so let the engine find it:

```toml
[engine]
bindAddress = "0.0.0.0"
broadcastAddress = "auto"
```

`auto` picks the container's first non-loopback address.

## Run it

```bash
docker run -d --name workflow-runtime -p 7233:7233 \
  -e WORKFLOW_DB_PASSWORD=<password> \
  -v "$PWD/runtime.toml:/opt/wso2-workflow-runtime/conf/runtime.toml:ro" \
  -v "$PWD/certs:/opt/wso2-workflow-runtime/conf/certs:ro" \
  <image>:<version>
```

Arguments after the image are passed to the engine, for example `--allow-no-auth` when trying it out.

## Try it with Docker Compose

The source repository's `distribution/docker/compose.yml` runs the runtime with PostgreSQL on one machine, without
TLS or authentication:

```bash
WORKFLOW_RUNTIME_IMAGE=<image>:<version> docker compose -f distribution/docker/compose.yml up
```

Create the `default` namespace, then point an integration at `localhost:7233`
([Connect an integration](connect-an-integration.md)):

```bash
temporal operator namespace create --namespace default --address localhost:7233
```

## Amazon ECS and Kubernetes

- Store `runtime.toml` in a config map (Kubernetes) or a mounted file (ECS) and the database password in a
  secret passed as `WORKFLOW_DB_PASSWORD`.
- Run one deployment or service per profile when splitting the engine ([Run across several nodes](scale-across-nodes.md)),
  each with its own `runtime.toml` differing only in `[runtime] profiles`.
- Expose port 7233 of the frontend tasks or pods through a TCP load balancer. The other ports are only for traffic
  between the engine's tasks or pods.
