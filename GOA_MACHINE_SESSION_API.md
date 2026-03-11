# Goa Machine Session API Draft

## Goal

Define the first-pass `goa` API for a Go-based machine core that can replace the current JS runtime behind a stable contract.

## Status

This document now describes the frozen v1 session contract currently implemented in `machine-core/design/machine.go`.

Rules for v1:

- add fields and methods only when a concrete parity gap or failing test requires them
- do not rename or repurpose existing fields casually
- generated code under `machine-core/gen/` must always be regenerated from the DSL, never edited by hand

This document is intentionally focused on:

- session lifecycle
- controller/runtime snapshots
- command submission
- event streaming
- migration compatibility

It is not trying to model every feature in the current app on day one.

## Design Principles

### 1. Session-oriented, not transport-oriented

The API should not expose "write bytes to serial port" as the primary abstraction.

The primary abstraction is a machine session:

- a device is opened
- a session is created
- clients attach to that session
- commands and files are sent to that session
- state is observed through snapshots and event streams

### 2. Explicit contract over implicit event mesh

Today the runtime relies on ad hoc Socket.IO event names and loosely shaped payloads.

The new API should provide:

- explicit request/response types
- explicit event stream payloads
- explicit session states
- explicit error categories

### 3. Snapshot plus events

The host should always be able to:

- fetch the current full session snapshot
- resolve the active session for a known device during reconnect
- subscribe to incremental runtime events

That makes reconnect and multi-client attach deterministic.

### 4. Minimal first milestone

The first implementation does not need every legacy feature.

The first useful milestone is:

- list devices
- open session
- detect controller
- resolve existing session by device
- get snapshot
- subscribe to events
- send core commands
- close session

## V1 Scope

Included in v1:

- device discovery
- session open/close
- device-to-session resolution for reconnect-safe attach
- attach/detach and replay
- session snapshot retrieval
- core workflow state transitions
- file load/unload lifecycle
- structured command submission
- flash request acceptance and guardrail modeling

Explicitly deferred from v1 unless a parity test forces earlier inclusion:

- full live serial parser parity
- full GRBL/grblHAL settings synchronization
- FTP/SD-card-specific workflows
- firmware-specific flashing implementations beyond the API guardrails
- removal of all legacy Socket.IO/controller glue
- hardware certification and release validation

## Service Surface

Suggested primary service name:

- `machine`

Suggested secondary service later if needed:

- `firmware`

## Core Entities

### Device

Represents something that can potentially host a machine session.

Fields:

- `id`
- `kind`
  - `serial`
  - `network`
  - `dfu`
- `path`
- `network_address`
- `manufacturer`
- `vendor_id`
- `product_id`
- `serial_number`
- `display_name`
- `in_use`
- `capabilities`

### Session

Represents one active machine runtime.

Fields:

- `id`
- `device_id`
- `controller_type`
  - `unknown`
  - `grbl`
  - `grblhal`
  - `fluidnc`
- `connection_state`
  - `disconnected`
  - `opening`
  - `probing_firmware`
  - `connected`
  - `reconnecting`
  - `closing`
  - `flashing`
  - `errored`
- `workflow_state`
  - `idle`
  - `running`
  - `paused`
  - `hold`
  - `stopping`
- `created_at`
- `updated_at`

### SessionSnapshot

Represents the full state needed for a reconnecting or newly attached client.

Fields:

- `session`
- `controller_state`
- `controller_settings`
- `sender_status`
- `feeder_status`
- `loaded_file`
- `homing_state`
- `alarm_state`
- `runtime_flags`
- `last_error`

### LoadedFile

Fields:

- `name`
- `size_bytes`
- `line_count`
- `checksum`
- `loaded_at`

### HomingState

Fields:

- `homing_required`
- `has_homed`
- `last_homing_at`

### RuntimeFlags

Fields:

- `can_start_job`
- `can_pause_job`
- `can_resume_job`
- `can_stop_job`
- `can_unload_file`
- `can_flash_firmware`

## Commands

Avoid generic stringly typed commands as the main API. Use structured commands.

### MachineCommandType

Suggested initial set:

- `unlock`
- `home`
- `reset`
- `sleep`
- `feed_hold`
- `cycle_start`
- `status_report`
- `jog`
- `set_feed_override`
- `set_spindle_override`
- `set_rapid_override`
- `raw_gcode_line`

### MachineCommand

Fields:

- `type`
- `raw_line`
- `axis_move`
- `feed_override`
- `spindle_override`
- `rapid_override`
- `metadata`

Only one payload branch should be populated according to `type`.

## RPC Methods

### `ListDevices`

Purpose:

- discover available machine devices

Response:

- `devices: Device[]`

### `OpenSession`

Purpose:

- open a device and create a machine session

Request:

- `device_id`
- `baud_rate`
- `rtscts`
- `network_port`
- `default_firmware`
- `client_id`

Response:

- `session`
- `snapshot`

Notes:

- this should perform controller detection
- if detection is asynchronous, response may return a session in `probing_firmware` state and the event stream will complete the transition

### `CloseSession`

Purpose:

- close and destroy a session

Request:

- `session_id`

Response:

- `closed`

### `GetSession`

Purpose:

- fetch current session metadata

Request:

- `session_id`

Response:

- `session`

### `GetSnapshot`

Purpose:

- fetch full state for reconnecting or newly attached clients

Request:

- `session_id`

Response:

- `snapshot`

### `AttachClient`

Purpose:

- register a logical UI client against a session

Request:

- `session_id`
- `client_id`

Response:

- `snapshot`

Notes:

- this is the explicit replacement for today’s implicit reconnect and add-client semantics

### `DetachClient`

Purpose:

- detach a UI client from a session without necessarily closing the machine session

Request:

- `session_id`
- `client_id`

Response:

- `detached`

### `SendCommand`

Purpose:

- submit a structured command to the machine session

Request:

- `session_id`
- `command`

Response:

- `accepted`

### `LoadFile`

Purpose:

- load G-code into the session runtime

Request:

- `session_id`
- `name`
- `content`
- `content_type`
- `metadata`

Response:

- `loaded_file`
- `snapshot`

### `UnloadFile`

Purpose:

- remove loaded file from the session

Request:

- `session_id`

Response:

- `unloaded`

### `StartJob`

Purpose:

- start executing the loaded file

Request:

- `session_id`
- `start_options`

Response:

- `accepted`

### `PauseJob`

Purpose:

- pause active execution

Request:

- `session_id`

Response:

- `accepted`

### `ResumeJob`

Purpose:

- resume paused execution

Request:

- `session_id`

Response:

- `accepted`

### `StopJob`

Purpose:

- stop execution

Request:

- `session_id`
- `force`

Response:

- `accepted`

## Event Stream

### `SubscribeEvents`

Purpose:

- stream runtime events for one session

Request:

- `session_id`
- `client_id`

Response:

- server stream of `SessionEvent`

This is the replacement for today’s raw Socket.IO event surface.

## SessionEvent

### Common fields

Every event should include:

- `session_id`
- `sequence`
- `occurred_at`
- `type`

### Event types

Initial event set:

- `session_opened`
- `session_closed`
- `client_attached`
- `client_detached`
- `controller_detected`
- `connection_state_changed`
- `controller_state_changed`
- `controller_settings_changed`
- `workflow_state_changed`
- `sender_status_changed`
- `feeder_status_changed`
- `file_loaded`
- `file_unloaded`
- `job_started`
- `job_paused`
- `job_resumed`
- `job_stopped`
- `job_progress`
- `homing_state_changed`
- `alarm_raised`
- `error_raised`
- `flash_started`
- `flash_progress`
- `flash_completed`

### Event payload rule

Keep the payload model explicit:

- each event type should have a specific payload shape
- avoid `map[string]any` except for truly opaque diagnostic metadata

## Error Model

Use domain-level errors, not just transport failures.

Suggested error categories:

- `device_not_found`
- `device_busy`
- `session_not_found`
- `unsupported_controller`
- `firmware_detection_failed`
- `invalid_state_transition`
- `command_rejected`
- `file_not_loaded`
- `job_not_running`
- `job_not_paused`
- `flash_not_allowed`
- `transport_error`
- `internal_error`

Each error should carry:

- `code`
- `message`
- `retryable`
- `details`

## First `goa` Design Outline

This is not final syntax, but it captures the shape we want.

```go
var _ = Service("machine", func() {
    Method("listDevices", func() {
        Result(ArrayOf(Device))
        HTTP(func() {
            GET("/machine/devices")
            Response(StatusOK)
        })
    })

    Method("openSession", func() {
        Payload(OpenSessionPayload)
        Result(OpenSessionResult)
        HTTP(func() {
            POST("/machine/sessions")
            Response(StatusCreated)
        })
    })

    Method("closeSession", func() {
        Payload(func() {
            Field(1, "session_id", String)
            Required("session_id")
        })
        HTTP(func() {
            DELETE("/machine/sessions/{session_id}")
            Response(StatusNoContent)
        })
    })

    Method("getSnapshot", func() {
        Payload(func() {
            Field(1, "session_id", String)
            Required("session_id")
        })
        Result(SessionSnapshot)
        HTTP(func() {
            GET("/machine/sessions/{session_id}/snapshot")
            Response(StatusOK)
        })
    })

    Method("sendCommand", func() {
        Payload(SendCommandPayload)
        Result(AcceptedResult)
        HTTP(func() {
            POST("/machine/sessions/{session_id}/commands")
            Response(StatusAccepted)
        })
    })
})
```

## Migration Strategy

### Stage 1

Define this contract before building the Go core.

### Stage 2

Build a JS compatibility adapter that exposes this API using the current runtime.

That lets the Electron app begin depending on the contract rather than the legacy event surface.

### Stage 3

Implement the same API in Go with `goa`.

### Stage 4

Swap implementations behind a feature flag.

### Stage 5

Migrate more behavior into the Go core incrementally.

## What Not To Do

Do not:

- expose arbitrary serial writes as the primary API
- make the frontend depend on low-level firmware parser events
- encode important state only as transient events without snapshot support
- let generated `goa` transport models become the full domain model
- port every legacy feature before the session API is proven

## First Implementation Scope

The first production-worthy version should support:

- device discovery
- session open and close
- controller detection
- snapshot fetch
- event subscription
- core machine commands
- file load and unload
- basic job lifecycle

That is enough to validate the architecture before porting every edge feature.
