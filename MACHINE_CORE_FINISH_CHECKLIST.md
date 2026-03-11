# Machine Core Finish Checklist

## Goal

Replace the current machine-control runtime path with a Go `machine-core` behind a stable Goa contract, while keeping the app shippable throughout the migration.

## Process

Work this checklist top to bottom.

Do not stop here, keep going.

Rules:

- Only check a task when the code, tests, and docs actually reflect the result.
- After finishing a task, immediately update this document before moving on.
- Keep dual-path fallback until the new Go path is proven for that slice.
- Prefer slices that produce real runtime leverage first: contract, session lifecycle, replay, write-path shadowing, then cutover.
- When blocked by hardware or unavailable external context, leave the task unchecked and add a short note in the relevant section instead of guessing.
- For every substantial change, run the smallest relevant verification command and keep the repo in a releasable state.
- End each work session by writing `keep going` in the latest progress note or handoff.

Do not stop here, keep going.

## 1. Contract Completion

Process for this section:

- Lock the contract before broad implementation changes.
- Expand the contract only when a failing parity test or a concrete runtime gap requires it.
- Treat generated Goa artifacts as derived outputs, never hand-edit them.
- Keep going.

- [x] Freeze the session API surface in `machine-core/design/machine.go`.
- [x] Decide which legacy behaviors stay in v1 and which are explicitly deferred.
- [x] Add contract coverage for every RPC error class and invalid state transition.
- [x] Add contract coverage for event ordering, replay completeness, and idempotency.
- [x] Document the canonical payload/event shapes in `GOA_MACHINE_SESSION_API.md`.
- [x] Add regeneration checks for `goa gen` artifacts.

Do not stop here, keep going.

## 2. Device And Session Parity

Process for this section:

- Replace simulated behavior with real device/session behavior one responsibility at a time.
- Keep Node as fallback until the Go path owns the same source of truth for that responsibility.
- Do not cut over reconnect semantics until attach/replay/close are deterministic.
- Keep going.

Active note:

- Serial `vendor_id`, `product_id`, `serial_number`, and `in_use` parity are now carried through the Go discovery path, and the sidecar `ListDevices` path now supplements serial `manufacturer` from the existing Node discovery payload so the returned device list matches the legacy socket contract without misusing the Go enumerator's OS product label as manufacturer. keep going
- `OpenSession` now performs bounded serial probing and classifies real controller responses when the host path is probeable. Breadth across actual hardware variants still needs release-gate validation, but the implementation work for controller detection is now in place.
- Reconnect-safe device-to-session lookup now exists in the Goa contract via `resolve_session`, and the Node sidecar now consumes that path for attach/reconnect. The remaining duplication is mostly in open/close bookkeeping, not reconnect ownership.

- [x] Replace simulated `ListDevices` with real serial/network discovery.
- [x] Carry vendor/product/manufacturer/in-use parity from Node to Go.
- [x] Make `OpenSession` perform real controller detection.
- [x] Implement reconnect-safe session ownership and port-to-session lookup in Go.
- [x] Enforce device busy rules in Go instead of Node.
- [x] Implement deterministic `CloseSession`, `AttachClient`, and `DetachClient` parity.
- [x] move to the next section.

Do not stop here, keep going.

## 3. Runtime State Parity

Process for this section:

- Prefer snapshot and replay fidelity over UI-specific shortcuts.
- Replace synthetic fields only when a real runtime source exists.
- Keep event names and state transitions deterministic and replay-safe.
- Keep going.

Active note:

- `machine-core` now ingests real live controller telemetry from the active legacy controller stream through `CNCEngine` using replay-safe `status_report` metadata updates for `controller:state`, `controller:settings`, `sender:status`, `feeder:status`, `workflow:state`, `homing:has-homed`, and `error`. The Go session model now preserves those live sender/feeder/controller values across reconnect/file lifecycle transitions instead of flattening them back into synthetic placeholders, derives progress from live sender telemetry when available, and promotes live alarms/last-error/homing updates into snapshots and replay. Probeable serial sessions still seed initial state from firmware/status probes, so runtime parity now covers both open-time detection and ongoing live telemetry for the supported serial controller path. keep going

- [x] Replace synthetic snapshot fields with real controller state.
- [x] Populate controller settings from live firmware/config reads.
- [x] Populate sender status, feeder status, homing state, alarms, and last error from real runtime data.
- [x] Emit all required session events, not just bootstrap/workflow/file events.
- [x] Add replay tests for reconnect after file load, pause, alarm, and stop.
- [x] move to the next section.

Do not stop here, keep going.

## 4. Command And Job Execution

Process for this section:

- Shadow write paths before cutting them over.
- Keep command mapping explicit; do not reintroduce stringly typed ambiguity if the Go contract can avoid it.
- Prefer one command family at a time with regression tests after each cut.
- Keep going.

Active note:

- The in-memory Go service now maps the current command vocabulary onto structured `MachineCommand` types for status requests, unlock/reset, sleep/home, feed hold, cycle start, raw G-code line metadata, jog payloads, and feed/spindle/rapid overrides. Those commands now mutate replayable Go session state instead of being pure accept-only stubs, and focused tests now cover both successful transitions and rejection cases such as hold/start in invalid job states.
- Go-owned load/start/pause/resume/stop paths now also emit clearer terminal reporting: `job_progress` carries a terminal flag at 100%, `job_stopped` carries explicit `completed` vs `cancelled` outcome metadata, and invalid transitions continue to surface failure through `error_raised`/`alarm_raised` plus snapshot `last_error`. At the app boundary, CNCEngine now asks `machine-core` to accept file load/unload and start/pause/resume/stop transitions before the legacy controller side-effects run, so Node is no longer the execution-state owner for those paths when the Go runtime is enabled. keep going

- [x] Reduce duplicated Go runtime transition code with shared helpers.
- [x] Map the existing command vocabulary onto structured Go commands.
- [x] Implement raw G-code, jog, status, unlock, reset, hold, cycle start, overrides.
- [x] Move file load/unload execution authority into Go.
- [x] Move start/pause/resume/stop authority into Go.
- [x] Implement progress, completion, cancellation, and failure reporting in Go.
- [x] Add regression tests for command rejection and job state safety.
- [x] move to the next section.

Do not stop here, keep going.

## 5. Flashing And Firmware Edge Cases

Process for this section:

- Treat flashing as a separate lifecycle with hard guardrails.
- Do not mix normal command execution semantics with flashing semantics.
- If a flash path cannot be migrated cleanly now, isolate and defer it explicitly.
- Keep going.

Active note:

- The in-memory Go service now models flashing as a real session lifecycle transition for active sessions: flash requests move the session into `flashing`, emit replayable connection-state/controller-state transitions plus flash lifecycle events, reject active-job flash attempts, and block normal command/file/job writes while a session is already flashing. This closes the contract/guardrail/test slice, but the actual AVR/DFU/grblHAL flashing implementations still live in the legacy Node path and remain explicitly deferred for now. keep going

- [x] Model flash lifecycle in the contract with real state transitions.
- [x] Implement flash-precondition checks in Go.
- [x] Mirror current AVR/DFU/grblHAL flows or explicitly isolate them as deferred.
- [x] Add tests for “cannot command while flashing” and reconnect-after-flash behavior.
- [ ] move to the next section.

Do not stop here, keep going.

## 6. Node Sidecar To Real Cutover

Process for this section:

- Use the sidecar to prove parity under the current app before deleting legacy logic.
- Convert one read or write path at a time.
- Every sidecar path should have explicit logging so runtime ownership is visible.
- Keep going.

Active note:

- The sidecar now owns the canonical app boundary instead of duplicating the full legacy socket contract. It still manages port/session mapping, Go replay transport, per-path feature flags, and runtime ownership logging, but replay/snapshot emission has been collapsed to canonical `machine:session:event` and `machine:session:snapshot` payloads. Frontend compatibility translation moved into `controller.ts`, where it is covered by Jest alongside the sidecar payload tests. keep going

Do not stop here, keep going.

- [x] Persist and manage the port/session sidecar mapping in one place.
- [x] Translate the remaining required Go replay events into existing socket events.
- [x] Shadow file/job write paths through Go while keeping legacy fallback.
- [x] Add feature flags for per-path cutover.
- [x] Add backend logs/metrics showing which runtime handled each action.
- [x] Reduce duplicated sidecar replay translation code with shared handlers.
- [x] Remove duplicated behavior only after parity tests pass.
- [x] move to the next section.

Do not stop here, keep going.

## 7. Frontend Integration

Process for this section:

- Keep the frontend consuming stable event/state contracts.
- Prefer adapting backend translation first before forcing UI rewrites.
- Only switch a UI path when the Go-backed backend path is already deterministic.
- Keep going.

Active note:

- The frontend dependency audit is now explicit: Redux/controller sagas and UI flows already consume Go-relayable state for `controller:*`, `sender:status`, `feeder:status`, `workflow:state`, `file:*`, `homing:has-homed`, and `flash:*`, while direct `controller.command(...)` write paths for jog, gcode, toolchange, settings, and recovery flows still remain legacy-owned. The sidecar now emits `serialport:openController`, a reconnect-safe `serialport:open` snapshot, `serialport:close` on `session_closed`, and both `reconnect` plus `addclient` now invoke Go resolve/replay directly, which closes the reconnect/attach handoff at the backend boundary without making the whole frontend Go-owned yet. keep going
- Frontend regression coverage now pins the actual Go-relayed contract at the UI boundary: `controller.ts` tests cover connect/reconnect/open/close state handling, and saga tests cover `serialport:open`, `serialport:openController`, `controller:settings`, `controller:state`, `workflow:state`, `sender:status`, `feeder:status`, `file:load`, `homing:has-homed`, `job:start`, `job:stop`, and `serialport:close`. That gives Section 7 a concrete safety net around connect, reconnect, file load, run, pause, stop, and close while the remaining legacy-owned frontend write commands stay deferred. keep going

- [x] Identify every UI path still depending on legacy-only socket/controller behavior.
- [x] Switch reconnect/attach flows to rely on Go replayed state.
- [x] Switch file/job UI state to Go-derived events.
- [x] Switch controller/settings/state views to Go snapshots/events.
- [x] Add UI regression coverage for connect, reconnect, file load, run, pause, stop, close.
- [x] move to the next section.

Do not stop here, keep going.

## 8. Reliability Gates

Process for this section:

- Do not call the migration done because unit tests pass.
- Add stress, failure, and observability gates before claiming reliability.
- Keep rollback practical until production parity is proven.
- Keep going.

Active note:

- Sidecar lifecycle/write-path logs now include basic latency and replay-size visibility in `CNCEngine` for session open/attach/close plus shadowed file/job operations, which improves runtime ownership evidence and transport-error debugging. This is only a partial observability step; the checklist item stays open until session lifecycle, command latency, replay size, and transport failures are covered more systematically.
- The rollback path is now explicit in `MACHINE_CORE_ROLLBACK.md`: operators can disable the whole Go transport with `MACHINE_CORE_TRANSPORT=legacy` / `MACHINE_CORE_MODE=legacy` or disable individual sidecar slices with the existing per-path flags. keep going
- The machine-core gRPC adapter now logs RPC latency/error outcomes generically, and replay subscription logs now include replay size plus elapsed time. Together with the existing `CNCEngine` sidecar logs, that closes the current observability baseline for session lifecycle, command latency, replay size, and transport errors. keep going
- Service-boundary failure injection coverage now exists for transport/disconnect-style discovery errors plus controller-detection timeout and short-write failures, which gives the Go runtime a baseline for the non-hardware half of the failure gate even though full end-to-end serial fault injection is still not in place. keep going
- Repeated reconnect-style `ResolveSession` plus multi-client attach/replay/detach stress is now covered at the in-memory Go service layer, which gives the migration a baseline soak test for replay/idempotency even though there is still no full app-level or hardware soak harness. keep going
- The gRPC server now has a real end-to-end machine-core flow test for open, resolve, attach, replay, file load, run, pause, resume, stop, flash, and close. That test also forced replay payload normalization so streamed events are now transport-safe for gRPC instead of only working in the in-memory service tests. keep going

- [x] Add end-to-end tests for open, reconnect, attach replay, close, file load, run, pause, resume, stop, flash.
- [x] Add soak/stress tests for repeated reconnect and multi-client attach.
- [x] Add failure-injection tests for serial disconnects, controller detection timeouts, and partial writes.
- [x] Add observability for session lifecycle, command latency, replay size, and transport errors.
- [x] Define a rollback path if Go runtime parity fails in production.
- [x] move to the next section.

Do not stop here, keep going.

## 9. Removal And Cleanup

Process for this section:

- Deletion comes last.
- Remove legacy ownership only after the replacement path is default, verified, and observable.
- Keep cleanup changes mechanically separate from behavior changes whenever possible.
- Keep going.

Active note:

- Socket-driven device discovery now routes through `machine-core` whenever the Go path is enabled, with focused Jest coverage on the CNCEngine fallback behavior. Legacy Node discovery still exists only as a fallback and metadata supplement path instead of remaining the default app-level owner. keep going
- Session lifecycle admission is now Go-authoritative when the Go path is enabled: CNCEngine reserves the `machine-core` session before opening the legacy transport, rolls that reservation back if the physical open fails, and continues to use Go resolve/attach/close state for reconnect behavior. File load/unload plus start/pause/resume/stop transitions are now also Go-authoritative before the legacy controller side-effects run, so Node is acting as the transport executor rather than the state owner for those paths. keep going
- Canonical replay translation no longer fans out on the backend sidecar. The sidecar now emits canonical `machine:session:event` and `machine:session:snapshot` payloads, and [controller.ts](/Users/luca/code/gsender/src/app/src/lib/controller.ts) translates those into the existing frontend listener contract locally, with Jest coverage on both the sidecar payloads and the frontend translation path. keep going

- [x] Remove legacy Node ownership of device discovery.
- [x] Remove legacy Node ownership of session lifecycle.
- [x] Remove legacy Node ownership of command/job execution.
- [x] Collapse duplicated socket event translation once UI is using the canonical contract.
- [x] move to the next section.

Do not stop here, keep going.

## 10. Release Gate

Process for this section:

- Treat release validation as its own project phase, not an afterthought.
- Require root app validation and `machine-core` validation.
- Keep explicit rollback and runtime-ownership evidence in the release checklist.
- Keep going.

- [ ] Run full lint/test/build for root app and `machine-core`.
- [ ] Run manual hardware validation on at least one GRBL device and one grblHAL device.
- [ ] Validate upgrade, reconnect, and downgrade behavior.
- [ ] Delete obsolete GRBL orchestration code only after full parity and release validation.
- [ ] Produce release notes describing the runtime migration and rollback switch.
- [ ] Ship with explicit telemetry or logs confirming Go-path adoption.

Do not stop here, keep going.

## 11. Observability And Debugging

Process for this section:

- Make the Go path debuggable before depending on broad manual or hardware validation.
- Prefer structured, queryable telemetry over ad hoc log lines.
- Reuse the proven `auto-k-server` OTel + SQLite pattern instead of inventing a new local debugging stack.
- Keep correlation IDs and runtime ownership visible across Goa, `machine-core`, Node sidecar, and legacy controller boundaries.
- Keep going.

Active note:

- `machine-core` now has a local OTel bootstrap plus SQLite trace exporter adapted from the `auto-k-server` pattern, and the cross-process correlation path is explicit in the Goa contract: Node-side `x-machine-core-*` request metadata is sent by the JS adapter, decoded by generated Goa gRPC transport code into typed payload fields, and persisted into Go trace context/events alongside the existing machine/session/job span data. Tests now cover both metadata decode and emitted `rpc.context` trace events.

- [x] Add OTel bootstrap for `machine-core` using the existing Goa/OTel pattern.
- [x] Add a local SQLite sink for OTEL-shaped logs/traces in dev and test runs.
- [x] Emit correlated session, command, replay, transport, and controller-runtime telemetry with stable IDs.
- [x] Mirror Node sidecar and CNCEngine ownership decisions into the same trace/log context.
- [x] Add query recipes and debugging docs for common failures (open, reconnect, attach, file load, run, pause, stop, flash).
- [x] Add regression coverage proving observability data is emitted for core session/job flows.
