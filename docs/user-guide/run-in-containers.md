---
title: Run in containers
description: Run the Workflow Runtime's container image with Docker, Docker Compose, Amazon ECS or Kubernetes.
keywords: [wso2 integrator, workflow runtime, docker, container, compose, kubernetes, ecs]
---

# Run in containers

One image serves every profile, for linux/amd64 and linux/arm64. It runs as a non-root user from
`/opt/wso2-workflow-runtime`, and its entry point, `workflow-runtime`, starts the engine with
`/opt/wso2-workflow-runtime/conf/runtime.toml`. Mount your configuration there.

:::info
Until the first release, test images are at `ghcr.io/wso2/integration-workflow-runtime:nightly`. Releases are
published to WSO2's registry.
:::

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

Arguments after the image are passed to `workflow-runtime`, for example `--allow-no-auth` when trying it out. An
engine without authentication must not be reachable from other machines, so publish its port on loopback only:
`-p 127.0.0.1:7233:7233`.

For a throwaway development engine with no configuration at all:

```bash
docker run --rm -p 127.0.0.1:7233:7233 <image>:<version> dev --ip 0.0.0.0
```

## Try it with Docker Compose

The source repository's `distribution/docker/compose.yml` runs the runtime with PostgreSQL on one machine, without
TLS or authentication:

```bash
WORKFLOW_RUNTIME_IMAGE=<image>:<version> docker compose -f distribution/docker/compose.yml up
```

Its `setup` service creates the `default` namespace, with the `WorkflowKind` search attribute, once the engine is
healthy; then point an integration at `localhost:7233` ([Connect an integration](connect-an-integration.md)).

## Health checks

The image includes `workflow-cli`, and the image has no shell, so give the health check its path:

```yaml
healthcheck:
  test: ["CMD", "/opt/wso2-workflow-runtime/bin/workflow-cli", "operator", "cluster", "health"]
```

On a node that runs another profile, check its own service and port, for example
`--service history --address 127.0.0.1:7234`, adding the internode certificate flags when internode TLS is on, and
`--tls-server-name` with the name in the internode certificate, since it does not hold `127.0.0.1`
([Manage the engine with workflow-cli](manage-with-workflow-cli.md#check-the-engine)). In Kubernetes, use the same
command as the readiness probe.

## API keys in containers

The image runs as a non-root user and its `conf/` is not writable, so keep the API key files in two mounted
directories: the signing key in a secret, and the revocation list with the issued-key record on a volume every node
shares, so a revocation reaches all of them.

```toml
[engine.auth.apiKeys]
signingKey = "/var/run/secrets/workflow-runtime/api-key-signing.pem"
revocationList = "/var/lib/workflow-runtime/keys/revoked-api-keys.txt"
```

1. Create the signing key and the first keys once, with both directories mounted writable. `--user` makes the files
   yours rather than the image's user; the key is printed on standard output:

   ```bash
   mkdir -p keys/signing keys/lists
   (umask 077; docker run --rm --user "$(id -u):$(id -g)" \
     -v "$PWD/runtime.toml:/opt/wso2-workflow-runtime/conf/runtime.toml:ro" \
     -v "$PWD/keys/signing:/var/run/secrets/workflow-runtime" \
     -v "$PWD/keys/lists:/var/lib/workflow-runtime/keys" \
     <image>:<version> api-key create --name ops --namespace '*' --role admin > ops.key)
   ```

   Run the same command with another `--name`, `--namespace` and `--role` for each integration's key.

2. Store the signing key as a secret and mount it read-only on every node. Other users must not be able to read it,
   so give the secret volume mode `0440` and the pod the image user's group (`65532`):

   ```bash
   kubectl create secret generic workflow-runtime-api-key-signing \
     --from-file=api-key-signing.pem=keys/signing/api-key-signing.pem
   ```

   ```yaml
   spec:
     securityContext:
       fsGroup: 65532
     containers:
       - name: workflow-runtime
         volumeMounts:
           - {name: api-key-signing, mountPath: /var/run/secrets/workflow-runtime, readOnly: true}
           - {name: api-key-lists, mountPath: /var/lib/workflow-runtime/keys}
     volumes:
       - name: api-key-signing
         secret: {secretName: workflow-runtime-api-key-signing, defaultMode: 0440}
       - name: api-key-lists
         persistentVolumeClaim: {claimName: workflow-runtime-api-key-lists}
   ```

   With plain Docker, mount `keys/signing` read-only at the same path instead.

3. Copy `keys/lists/revoked-api-keys.txt` and `keys/lists/issued-api-keys.txt` onto the shared volume, a
   ReadWriteMany volume such as NFS or Amazon EFS that you can also mount on your machine. Let the image's group read
   and write them, for example `chgrp 65532` and `chmod 660`; the engine refuses a list other users can write.

4. To revoke a key, run `api-key revoke` with the shared volume mounted writable, here mounted on your machine at
   `/mnt/workflow-runtime-keys`. Running nodes read the change within seconds:

   ```bash
   docker run --rm --user "$(id -u):65532" \
     -v "$PWD/runtime.toml:/opt/wso2-workflow-runtime/conf/runtime.toml:ro" \
     -v "/mnt/workflow-runtime-keys:/var/lib/workflow-runtime/keys" \
     <image>:<version> api-key revoke --name orders-integration --reason "leaked in a log"
   ```

   Later `api-key create` and `api-key list` commands take the same mounts, plus the signing key read-only for
   `create`.

A node that has read the list and then loses it, for example when the volume fails to mount, refuses every API key
until the list is back. A node that never saw a list logs a warning at start and accepts every key, so revocations
reach only the nodes that mount the volume.

## Amazon ECS and Kubernetes

- Store `runtime.toml` in a config map (Kubernetes) or a mounted file (ECS) and the database password in a
  secret passed as `WORKFLOW_DB_PASSWORD`.
- Run one deployment or service per profile when splitting the engine ([Run across several nodes](scale-across-nodes.md)),
  each with its own `runtime.toml` differing only in `[runtime] profiles`.
- Expose port 7233 of the frontend tasks or pods through a TCP load balancer. The other ports are only for traffic
  between the engine's tasks or pods.
