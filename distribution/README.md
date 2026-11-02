# distribution

Assembles the zip and the container image ([docs/features/distribution.md](../docs/features/distribution.md)).

```
wso2-workflow-runtime-<version>-<os>-<arch>/
  bin/         workflow-runtime(.sh|.bat), workflow-engine, workflow-engine-sql
  conf/        runtime.toml (the one file customers edit)
  systemd/     example service unit (Linux zips)
  sbom/        CycloneDX SBOM per binary
  third-party/ licenses and notices for everything shipped (Temporal's MIT license among them)
  README.md  LICENSE  NOTICE
```

| Path | What |
|---|---|
| `bin/` | Start scripts |
| `conf/runtime.toml` | The sample configuration shipped in every zip and the image |
| `systemd/` | Example unit for Linux virtual machines |
| `docker/Dockerfile` | The image, assembled from the linux zips (distroless, non-root) |
| `docker/compose.yml`, `docker/try-out.toml` | Try the runtime with PostgreSQL on one machine |
| `README.dist.md` | The zip's quick-start README |

The engine is a static Go binary (`CGO_ENABLED=0`), so the image uses a minimal base (distroless static), which
also keeps the base image's own license obligations small.
