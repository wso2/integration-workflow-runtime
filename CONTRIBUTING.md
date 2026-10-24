# Contributing to the WSO2 Integration Workflow Runtime

The runtime is licensed under the [Apache License 2.0](LICENSE). Contributions are welcome.

## Get started

- Read [AGENTS.md](AGENTS.md): it holds the rules every change follows, for people and coding agents alike.
- Read [docs/guides/ARCHITECTURE.md](docs/guides/ARCHITECTURE.md) and [docs/guides/DEVELOPING.md](docs/guides/DEVELOPING.md).
- Install the commit checks after cloning: `make install-hooks`.
- Report issues in [wso2/product-integrator](https://github.com/wso2/product-integrator/issues) with the
  `Area/Workflow` label.

## Build

- Go 1.26.8 or later and Python 3. For the Ballerina suites: Ballerina Swan Lake (`bal`) and the Temporal CLI.
- `make build`, `make test-coverage`, `make check`; `make test-smoke` and `make test-full` for the Ballerina suites.

## Submit a change

1. Make the change, with its documentation in `docs/` and its tests (see [AGENTS.md](AGENTS.md#rules-for-every-change)).
2. Run `make check` and, for engine changes, `make test-smoke`.
3. Commit with a [Conventional Commits](https://www.conventionalcommits.org/) subject, for example
   `feat(engine): add the engine endpoint on port 7233`. The PR title follows the same format; the Lint PR
   workflow checks both.
4. Push to your fork and open a pull request against `main`, referencing the public issue.
5. Accept the Contributor License Agreement when prompted on your first pull request.
