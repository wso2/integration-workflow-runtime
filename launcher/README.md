# launcher

Planned. One process per node that:

- reads `conf/runtime.toml` and the profiles to run;
- writes Temporal's configuration (persistence, TLS, JWT authorizer trusting the Manager's keys) and starts
  `workflow-engine` with the services of those profiles, including the internal frontend when auth is on;
- starts the Manager (a JVM process) for the `manager` profile;
- runs `workflow-engine-sql` on first start and on upgrade, and refuses an engine more than one Temporal minor
  ahead of the version recorded in the Runtime DB;
- restarts a component on failure and reports health.

Likely a second command in the `engine` module, so the distribution ships one Go binary for both.
