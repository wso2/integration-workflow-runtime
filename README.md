# WSO2 Integration Workflow Runtime

Durable workflow engine for WSO2 Integrator: embedded Temporal, a Go Manager with SSO and RBAC, and admin and
user portals, in one distribution.

The runtime is the engine that runs WSO2 Integrator's durable workflows. It is built on the open-source Temporal
server, which runs inside it as an internal component: customers provide a database and an identity provider,
and never install or patch Temporal themselves.

Status: early development. The engine builds and runs; the launcher, Manager and portals are being built.

## Layout

| Path | Component | Language |
|---|---|---|
| `engine/` | Temporal's services in our own binary (`workflow-engine`) and the schema tool (`workflow-engine-sql`) | Go |
| `launcher/` | Starts and supervises components per profile; writes Temporal's configuration from `runtime.toml`; enforces one-minor upgrades | Go |
| `manager/` | The Manager: administration API, SSO sign-in, roles; serves the portals | Go |
| `portals/` | Admin Portal and User Portal, static web apps served by the Manager | TypeScript |
| `distribution/` | Zip and Docker assembly, configuration templates, start scripts | Shell, Docker |
| `tests/` | Ballerina smoke and full suites run against the engine binary | Ballerina, Python |
| `third-party/` | License inventory and notices for everything we ship | |
| `docs/decisions/` | Decision records | |

Documentation: [architecture](docs/guides/ARCHITECTURE.md), [developing](docs/guides/DEVELOPING.md),
[testing](docs/guides/TESTING.md), [features](docs/features/README.md), [decisions](docs/decisions/).
Contributors and coding agents: read [AGENTS.md](AGENTS.md) first.

## Build

Needs Go 1.26.8 or later; the Go toolchain downloads it on demand.

```sh
make install-hooks   # commit checks
make build           # build/workflow-engine, build/workflow-engine-sql
make check           # every CI gate: vet, lint, gofmt, unit tests (75% coverage), licenses, vulnerabilities
make test-smoke      # Ballerina smoke suite against the engine (needs bal and the temporal CLI)
make test-full       # Ballerina full suite
./build/workflow-engine version
```

## Contributing

Issues are tracked in [wso2/product-integrator](https://github.com/wso2/product-integrator/issues) with the
`Area/Workflow` label. Send pull requests to this repository; see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0. Bundled third-party components keep their own licenses; see [NOTICE](NOTICE) and
[third-party/](third-party/).
