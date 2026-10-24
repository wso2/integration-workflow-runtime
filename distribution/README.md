# distribution

Assembles the zip and the Docker image.

```
workflow-runtime-<version>/
  bin/        workflow-runtime(.sh|.bat), workflow-engine, workflow-engine-sql
  conf/       runtime.toml (the one file customers edit)
  manager/    Manager executable and the portals' static files
  third-party/ licenses and notices for everything shipped (Temporal's MIT license among them)
  LICENSE  NOTICE
```

The engine is a static Go binary (`CGO_ENABLED=0`), so the image can use a minimal base (distroless static), which
also keeps the base image's own license obligations small.
