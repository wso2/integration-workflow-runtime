# launcher

The launcher is `bin/workflow-runtime`, built from [`engine/cmd/workflow-runtime`](../engine/cmd/workflow-runtime)
(`engine/internal/launcher`) so the distribution's Go binaries share one module. Today it:

- finds the runtime's home from its own location and works from there, so relative paths in `conf/runtime.toml`
  resolve against it;
- starts `workflow-engine` from `conf/runtime.toml` (or the file `WORKFLOW_RUNTIME_CONFIG` names), and runs
  development mode with `dev`;
- replaces itself with the engine on Linux and macOS, and runs it as a child on Windows.

Planned:

- start the API server for the `api` profile;
- refuse an engine more than one Temporal minor ahead of the version recorded at the last start;
- restart a component on failure and report health.
