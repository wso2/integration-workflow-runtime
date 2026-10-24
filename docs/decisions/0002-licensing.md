# 0002. Licensing and notices

Status: accepted · 2026-10-23

## Context

The runtime's code is Apache 2.0. The engine embeds the Temporal server, which is MIT-licensed, and links every Go
module Temporal depends on into one static binary. Everything we ship has to carry the right notices, and no
dependency with an incompatible license may slip in.

## Licenses involved

| Component | License |
|---|---|
| Our code: engine, launcher and Manager (Go), portals (TypeScript) | Apache 2.0 |
| Temporal server (`go.temporal.io/server`) | MIT. Copyright (c) 2025 Temporal Technologies Inc., Copyright (c) 2020 Uber Technologies, Inc. |
| Go modules linked into the engine | Mostly Apache 2.0, MIT and BSD; the full list is `third-party/go-licenses.csv` |
| Go runtime and standard library | BSD-3-Clause |

MIT's condition is that its copyright and permission notice travel with every copy. MIT permits distributing
Temporal inside an Apache 2.0 product; Temporal's code keeps its MIT license.

## Decision

1. **Every artifact carries the notices.** The zip, the Docker image and every engine update include `LICENSE`,
   `NOTICE` and `third-party/` (Temporal's license text, the dependency inventory and the licenses it lists).
2. **Adapted source keeps its notice.** A file that follows Temporal's code (for example the engine's `main.go`)
   names Temporal's MIT copyright in its header beside ours.
3. **The inventory is generated, not written.** `make licenses` regenerates `third-party/go-licenses.csv`; CI
   fails when the committed file is out of date.
4. **CI rejects incompatible licenses.** `make check` fails on any dependency whose license is forbidden,
   restricted (for example GPL, LGPL, AGPL) or unknown. A module whose license cannot be detected automatically
   is accepted only after a manual check, listed with the reason in `third-party/license-exceptions.txt`.
5. **MPL-2.0 dependencies are used unmodified.** Their license ships with the notices, with where their source is.
6. **Names avoid the Temporal trademark.** Product and binary names do not use "Temporal"
   (`workflow-engine`, not `temporal-server`). Docs may say the runtime is built on the open-source Temporal server.
7. **Images use a minimal base** (distroless static), which brings no OS userland of its own to license.
8. **Changes to Temporal stay MIT,** so they can go upstream unchanged.
