# Reliability Refactor Plan

## Goal

Start improving reliability in the highest-payoff area of the codebase without triggering a rewrite trap.

The best first refactor is not "clean up GRBL" in the abstract. It is to introduce a hard runtime boundary around a single machine connection session, then move GRBL and grblHAL behavior behind that boundary.

## Current State (2026-03-11)

- `machine-core` exists with a Goa API contract and generated stubs in `machine-core/gen`, but no hand-written session runtime is wired yet.
- The Node runtime remains the source of runtime truth for `src/server`, `src/app`, and `src/server/services/cncengine`.
- The most valuable next step is to stop changing behavior before locking deterministic lifecycle contracts and testable session semantics.
- **Current slice completed:** `machine-core` now includes `internal/service/machine_service.go` implementing the Goa contract (`gen.Service`) and `internal/service/machine_service_test.go` with lifecycle regression tests. It enforces basic idempotency and state transition rules for open/attach/close/replay/file load/unload and command lifecycle. The service also has deterministic session state updates and copy-on-read snapshot behavior.

## Immediate Execution Focus

Before writing more core runtime code, complete this in order:

1. Lock the session contract behavior with tests against current runtime.
2. Add explicit contract-facing adapter entry points in Node (even if they are narrow).
3. Prove parity for open/reconnect/close/attach/replay/flash through the adapter.
4. Expand Goa contract and generated client/server artifacts only when needed by those tests.
5. Begin implementing Go runtime modules behind that same contract.

Recommended first tasks:

- Add minimal lifecycle regression tests for:
  - open session controller detection,
  - reconnect with listener dedupe,
  - attach replay semantics,
  - idempotent close,
  - flash state transition safety.
- Add a thin compatibility API layer under `src/server` that maps these tests to a future `machine-core` boundary (without changing UI flow yet).
- Use this checklist to gate all edits touching `CNCEngine`, `Connection`, and controller orchestration.

### First 14-day execution path

1. **Contract snapshot pass**
  - Capture current expected behavior for open/reconnect/close/attach/flash with deterministic fixtures in `test/`.
  - Add assertions around listener count, ownership, and state replay timing.
- Add no-production-facing helpers only if needed for test observability.

### Progress Update (2026-03-11)

- Step 0 (Foundation) is complete:
  - In-memory Goa service implementation created in Go at `machine-core/internal/service/machine_service.go`.
  - Session lifecycle contract tests added and passing in `machine-core/internal/service/machine_service_test.go`.
  - `go test ./...` passes for `machine-core` with generated artifacts included.
  - Generator CLI vet warning fixed in `machine-core/goa489139379/main.go`.
- This allows the next phase to focus on:
  - adding a thin Node-to-Go compatibility adapter for read paths,
  - extending command validation to mirror legacy behavior (pause/resume edge states, flash guardrails, reconnect replay semantics),
  - then replacing command execution pieces incrementally.

2. **Adapter seam** ✅
  - ✅ Created a narrow `machine-core`-facing service facade at `src/server/services/machine-core/index.js`.
  - ✅ Wired existing runtime into the facade and re-pointed startup/file upload call sites:
    - `src/server/index.js`
    - `src/server/api/api.file.js`
  - ✅ Added `test/machine-core-adapter.test.js` to pin facade dispatch behavior before introducing a Go backend.
  - ✅ Added `test/machine-core.contract.test.js` to lock the Node-side Goa/gRPC payload mapping and transport-mode selection semantics with Jest.
  - ✅ Expanded seam contract with session-oriented API placeholders (`listDevices`, `openSession`, `closeSession`, `getSnapshot`, `attachClient`, `detachClient`, `sendCommand`) and added coverage for dispatch coverage of the expanded API.
  - ✅ Added optional Go-backed runtime path behind `MACHINE_CORE_TRANSPORT=grpc` (or `go`/`goa`), loading `machine-core` gRPC methods from the generated proto and mapping the session payloads into Goa request format.
  - ✅ Extracted device discovery normalization into shared helpers and routed the legacy socket device-list path through the same `listDevices()` runtime method now used by the seam.
  - ✅ Added a narrow Node replay bridge so the gRPC adapter can consume `SubscribeEvents` into a callback-driven event replay path without changing renderer/socket contracts yet.
  - ✅ Mirrored one real backend attach/reconnect path in `CNCEngine` via a Go sidecar session map (`port -> session_id`) so open/addclient/close can shadow the legacy runtime with `openSession`, `attachClient`, `replayEvents`, and `closeSession`.
  - ✅ Began translating sidecar replay back into existing socket events by mapping a safe subset of Go session events into `workflow:state`, `sender:status`, `file:load`, and `file:unload`.

3. **Go contract hardening ✅**
  - ✅ Added lifecycle regression tests in `machine-core/internal/service/machine_service_test.go` to lock idempotent close, attach replay dedupe behavior, and snapshot isolation.
  - ✅ Confirmed deterministic in-memory service behavior with copy-on-read snapshots and session record assertions for duplicate client attachment.

4. **Goa hardening ✅**
  - ✅ Locking session API contract through the existing design and generated transports.
  - ✅ Documented command to regenerate artifacts when contract updates happen (`goa gen ...`).

5. **Go runtime first slice ✅ (transport bootstrap)**
  - ✅ Added a runnable gRPC host in `machine-core/cmd/machine-core/main.go` using the generated transport (`machinepb.RegisterMachineServer`).
  - ✅ Added end-to-end startup and lifecycle validation in `machine-core/cmd/machine-core/main_test.go` (`list_devices -> open_session -> close_session`).
  - ✅ Hardened the in-memory `ListDevices` implementation so the Go service now reports multiple simulated device kinds and reflects `in_use` state from open sessions.
  - ✅ Hardened `OpenSession` bootstrap semantics so the Go service now derives initial `controller_type` and `connection_state` from the selected device/default firmware, including explicit `probing_firmware` fallback when detection is still unresolved.
  - ✅ Added deterministic session event replay for `SubscribeEvents`, including bootstrap `session_opened`, `connection_state_changed`, `controller_detected`, and `client_attached` events with monotonic sequence numbers.
  - ✅ Extended event replay through the core workflow lifecycle so file/job transitions now emit `file_loaded`, `job_started`, `job_paused`, `job_resumed`, `job_stopped`, and `file_unloaded`.

6. **Cutover decision gate**
  - Do not migrate UI event flow until phase 1-4 acceptance criteria are met.
  - Keep dual-path compatibility until reconnect and close idempotence are stable.

### Phase 1 acceptance criteria

- `test/` has at least one deterministic coverage test for each target behavior:
  - open lifecycle chooses correct controller type,
  - reconnect attaches to the same session without duplicate listeners,
  - attach replay emits single snapshot,
  - close idempotency and listener cleanup,
  - flash transition disables command routing.
- A `src/server` adapter seam exists and can be unit-tested without UI changes.
- `machine-core/design/machine.go` and generated artifacts are updated only via explicit test-driven contract changes.

## Why This First

The current reliability risk is concentrated in machine lifecycle orchestration, not just in large files.

Today that lifecycle is spread across:

- Socket.IO client event handling in [src/app/src/lib/controller.ts](/Users/luca/code/gsender/src/app/src/lib/controller.ts)
- Redux saga orchestration in [src/app/src/store/redux/sagas/controllerSagas.tsx](/Users/luca/code/gsender/src/app/src/store/redux/sagas/controllerSagas.tsx)
- The engine-global mutable connection in [src/server/services/cncengine/CNCEngine.js](/Users/luca/code/gsender/src/server/services/cncengine/CNCEngine.js)
- The serial/network transport in [src/server/lib/Connection.js](/Users/luca/code/gsender/src/server/lib/Connection.js)
- The controller implementations in [src/server/controllers/Grbl/GrblController.js](/Users/luca/code/gsender/src/server/controllers/Grbl/GrblController.js) and [src/server/controllers/Grblhal/GrblHalController.js](/Users/luca/code/gsender/src/server/controllers/Grblhal/GrblHalController.js)

That means reconnect, close, flash, and multi-client behavior depend on shared mutable state and implicit event ordering.

In a machine-control app, that is the most dangerous failure mode.

## Main Diagnosis

### 1. One global connection is doing per-port work

`CNCEngine` currently holds a single mutable `this.connection`, but much of the system behaves as if connections are scoped per port.

This is visible in:

- [src/server/services/cncengine/CNCEngine.js](/Users/luca/code/gsender/src/server/services/cncengine/CNCEngine.js)
- [src/server/lib/Connection.js](/Users/luca/code/gsender/src/server/lib/Connection.js)

This creates risk of:

- reconnect flows mutating the wrong active connection
- listener removal affecting the wrong session
- stale controller references surviving close/reopen paths
- callback ownership being ambiguous during refresh/reconnect

### 2. Runtime state is spread across multiple event systems

The same machine lifecycle is represented through:

- Socket.IO events
- Redux actions
- `pubsub-js` topics
- persistent app store values

This is visible in:

- [src/app/src/lib/controller.ts](/Users/luca/code/gsender/src/app/src/lib/controller.ts)
- [src/app/src/store/redux/sagas/controllerSagas.tsx](/Users/luca/code/gsender/src/app/src/store/redux/sagas/controllerSagas.tsx)
- [src/app/src/store/index.ts](/Users/luca/code/gsender/src/app/src/store/index.ts)

That does not just make the code ugly. It makes correctness depend on ordering across systems that have no single source of truth.

### 3. GRBL and grblHAL are too large to safely improve in place

The controller implementations are already huge:

- `GrblController.js`: about 2234 lines
- `GrblHalController.js`: about 2621 lines

Trying to "encapsulate GRBL" before creating a stable seam will likely produce churn without improving reliability.

## Strategic Direction

The immediate reliability seam is still a session boundary, but the preferred end state is no longer "a cleaner JS runtime."

The preferred end state is:

- a Go machine core
- a `goa` API in front of that core
- a session-oriented contract that the Electron/Node app talks to
- incremental removal of direct dependence on the current Socket.IO event mesh

So the profitable plan is:

1. freeze the machine-session contract using the current runtime
2. prove the contract with lifecycle tests
3. move the implementation behind that contract into Go
4. use `goa` as the canonical API boundary

## Target Architecture

### Host / Core Split

The host application should own:

- windows and renderer lifecycle
- UI state and dialogs
- visualizer and workspace concerns
- user preference persistence
- analytics and reporting

The Go machine core should own:

- device discovery
- connection/session lifecycle
- firmware detection
- protocol parsing
- controller state machine
- job streaming and queueing
- reconnect semantics
- file execution state
- snapshot and replay behavior
- flashing lifecycle

### Why `goa` Helps

`goa` is not just a transport convenience here. It directly addresses one of the biggest problems in the current codebase: there is no hard contract.

Today the runtime depends on:

- ad hoc Socket.IO event names
- loosely shaped payloads
- implicit sequencing across multiple event systems
- shared mutable state spread across backend and frontend

Using `goa` for the machine API helps by forcing:

- explicit service definitions
- explicit request and response models
- explicit event stream models
- generated clients and servers
- one canonical contract for migration

That will materially reduce drift and ambiguity during the rewrite.

## Session-Oriented API Boundary

The machine API should be session-based, not "write arbitrary bytes to port."

Suggested operations:

- `ListDevices`
- `OpenSession`
- `CloseSession`
- `AttachClient`
- `DetachClient`
- `LoadFile`
- `UnloadFile`
- `SendCommand`
- `StartJob`
- `PauseJob`
- `ResumeJob`
- `StopJob`
- `FlashFirmware`
- `GetSnapshot`
- `SubscribeEvents`

Suggested event families:

- `session_opened`
- `session_closed`
- `controller_detected`
- `controller_state_changed`
- `workflow_state_changed`
- `sender_status_changed`
- `feeder_status_changed`
- `file_loaded`
- `file_unloaded`
- `job_progress`
- `alarm`
- `error`
- `flash_progress`

### Session Model

The core runtime should own one explicit session object per active machine connection.

Each session should own:

- one device identity
- one active connection
- one controller runtime
- one session state machine
- one file/job state
- one subscriber set
- one teardown path

Suggested explicit states:

- `disconnected`
- `opening`
- `probing_firmware`
- `connected`
- `reconnecting`
- `running`
- `paused`
- `closing`
- `flashing`
- `errored`

### Core Package Shape

Suggested Go layout:

- `design/`
  - `goa` design for the machine session service
- `internal/machine`
  - session manager
  - controller runtime coordination
  - snapshots and replay
- `internal/serial`
  - serial and network transport
- `internal/protocol/grbl`
  - GRBL protocol parsing and translation
- `internal/protocol/grblhal`
  - grblHAL protocol parsing and translation
- `internal/gcode`
  - job streaming and queueing
- `gen/`
  - generated `goa` transport, client, and server code

Critical rule:

- keep `goa` transport code at the edge
- keep machine runtime logic in hand-written domain packages

Do not let generated transport types become the whole architecture.

## First Tests To Write

These should be added before or during extraction of the session contract. They should initially run against the current JS runtime, and later become compatibility tests for the Go implementation.

### 1. Open flow creates the correct controller

Given firmware probe output:

- GRBL creates `GrblController`
- grblHAL creates `GrblHalController`
- unknown firmware falls back only through an explicit path

### 2. Reconnect does not duplicate listeners

When a socket disconnects and reconnects:

- no duplicate event listeners are attached
- replayed state is sent once
- the session remains attached to the correct port

### 3. Additional client attach and state replay work correctly

If a second client joins an already-open machine session:

- controller snapshot is replayed once
- file state is replayed once
- workflow state is replayed without mutation
- attach does not poison the underlying connection lifecycle

### 4. Close is idempotent

Calling close more than once:

- does not double-destroy
- does not double-call callbacks
- clears session registry state exactly once

### 5. File replay behaves correctly for reconnecting clients

If a file is loaded and a second client joins or reconnects:

- file metadata is replayed correctly
- running workflows do not reload unsafely
- idle workflows replay state safely

### 6. Flash flow transitions cleanly

Starting a flash:

- detaches the active controller/connection cleanly
- enters a flashing state
- prevents normal command routing during flash
- leaves the session in a known post-flash state

## Phased Execution Order

### Phase 1: Freeze the contract

- document the current runtime event and lifecycle behavior
- add a small test harness around `CNCEngine` and `Connection`
- add regression tests for open, reconnect, attach, close, replay, and flash
- define the target session and snapshot model before building the Go core

Current repo evidence:
- `machine-core/design/machine.go` already defines a session model and API.
- `src/server/services/cncengine/CNCEngine.js` and `src/server/lib/Connection.js` still own live session orchestration.

Outcome:
- current behavior is pinned down before structural change or rewrite

### Phase 2: Harden the `goa` session API and generated boundaries

- confirm service methods/payloads and event stream/snapshot models against the contract tests
- tighten domain error semantics where needed
- regenerate Goa artifacts only after design edits are validated
- make the contract session-oriented rather than transport-oriented

Outcome:

- there is one canonical host/core boundary

### Phase 3: Build a JS compatibility adapter behind the new contract

- adapt the current runtime to the new session API
- keep the implementation ugly if needed, but force the boundary to become explicit
- use this adapter to validate the API before the Go rewrite

Outcome:

- the app can start depending on the contract instead of direct runtime details

### Phase 4: Build the Go core behind the same contract

- implement session manager, transport, parsing, snapshots, and event fanout in Go
- use `goa` generated transport at the edge only
- initially support the minimum viable runtime:
  - open device
  - detect controller
  - maintain snapshot
  - send core commands
  - replay state to reconnecting clients

Outcome:

- the riskiest runtime logic moves into a stricter, more predictable implementation

### Phase 5: Migrate behavior incrementally

- move more commands and workflows from the JS runtime to the Go core
- add file execution, pause/resume, flashing, and richer alarms/errors
- retire direct dependence on legacy Socket.IO event names

Outcome:

- the legacy runtime shrinks behind a stable boundary

### Phase 6: Reduce frontend event sprawl

- replace raw controller event consumption with one generated client gateway
- reduce direct dependence on `pubsub-js` and ad hoc Redux orchestration
- consume snapshots and event streams from the machine-session API

Outcome:

- frontend behavior becomes more deterministic because the backend contract is finally explicit

## What Not To Do First

Do not start with:

- a full rewrite of `GrblController.js`
- merging GRBL and grblHAL into one mega-controller inside the current JS runtime
- a broad Redux/store cleanup
- porting every feature before the new contract exists
- using `goa` generated code as the domain model itself

Those may become worthwhile later, but they do not attack the highest-risk failure mode first.

## Success Criteria

This refactor is paying off if:

- reconnect behavior becomes deterministic
- session lifecycle no longer relies on one engine-global connection
- close, attach, replay, and flash paths become testable and idempotent
- the Electron app depends on one explicit machine-session API
- the Go core can be swapped in behind that API without rewriting the UI again
- future GRBL and grblHAL cleanup happens in a compiled runtime behind a stable contract

## Recommendation

Proceed with:

1. pin down lifecycle behavior with tests
2. define the session-oriented `goa` API
3. validate that API with a JS compatibility adapter
4. implement the new machine core in Go
5. migrate behavior incrementally behind the same contract

That is the most reliable way to turn "make GRBL tested and encapsulated" into a predictable migration toward a Go-based machine core instead of just rearranging the current mess.
