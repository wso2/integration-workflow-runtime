# third-party

What we must ship with the distribution for the components we bundle. See
[0002](../docs/decisions/0002-licensing.md).

| File | What |
|---|---|
| `licenses/temporal-server-LICENSE` | Temporal's MIT license, copied from the pinned version |
| `go-licenses.csv` | Every Go module linked into the engine and its license (`make licenses`), checked by `make license-check` |
| `license-exceptions.txt` | Modules accepted after a manual license check, with the reason; the CI license gate skips only these |
