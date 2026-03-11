---
name: gsender-goa-migration
description: Migration governance for moving machine lifecycle/session responsibility to Go core behind a Goa boundary.
---

# gSender Goa Migration Guide

Use this as the source of truth while extracting the machine runtime away from the current Node stack.

## Core Boundary
- `machine-core` owns machine session lifecycle, transport, firmware detection, command/state/job execution, and flash flow safety.
- Electron/Node layers (`src/server`, `src/app`, Electron bootstrap) remain integration and UI until parity is reached.
- Never model lifecycle through direct Socket.IO event rewriting in new work; treat session APIs as the contract.

## Cutover Rules
- Move behavior only after parity tests are in place for:
  - open/close lifecycle
  - reconnect + listener safety
  - add-client state replay
  - flash flow safety
  - job start/pause/resume/stop transitions
- Keep generated Goa artifacts under `machine-core/gen/` as edge output.
- Handwritten logic belongs in non-generated packages under `machine-core/internal/...`.

## Session Model Requirements
- One explicit session per active machine connection.
- One controller/runtime path per session.
- One teardown path per session state transition.
- Session transitions should be deterministic and replayable (`disconnected`, `opening`, `connected`, `reconnecting`, `running`, `paused`, `closing`, `flashing`, `errored`).

## Before Cutover Checklist
- Freeze observed JS behavior for the touched lifecycle path with tests.
- Add/update tests for reconnect/listener behavior and idempotent close before removing JS ownership.
- Validate `machine-core` behavior against the same tests before removing direct controller/session dependencies.

## Go/Goa Practical Rule
- In Goa boundaries, keep transport contracts explicit and stable.
- Regenerate design output with:
  - `cd machine-core && goa gen github.com/Sienci-Labs/gsender/machine-core/design`
- When interfaces change, keep generated checks aligned before merge approval.
