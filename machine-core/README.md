# machine-core

This directory is the starting point for a Go-based machine runtime intended to replace the current JS controller/runtime layer behind a stable API.

## Scope

This module is for:

- machine session lifecycle
- device discovery
- controller detection
- runtime snapshots
- command submission
- event streaming

It is not intended to own renderer/UI concerns.

## Structure

- `design/`
  - `goa` design definitions for the machine session API
- `gen/`
  - generated Goa interfaces, gRPC transport, and client/server stubs
- `internal/`
  - session behavior and implementation boundaries
- `cmd/machine-core/`
  - Go runtime entrypoint that currently serves the gRPC API

## Notes

- This is intentionally isolated from the existing Node/Electron app.
- The first goal is to lock the API contract, not to port every runtime behavior immediately.
- The Node seam can call this service behind `MACHINE_CORE_TRANSPORT=grpc|goa|go` using the generated proto at runtime.

## Running the Go service

From the repository root:

```bash
cd machine-core
go test ./...
go run ./cmd/machine-core --addr :8081
```

The default endpoint is `127.0.0.1:8081` when `--addr` is omitted.

## Local observability

`machine-core` now starts with local OTel tracing enabled by default and writes spans plus span events to a SQLite debug database.

- Default DB path: `logs/machine-core-debug.sqlite`
- Disable locally: `MACHINE_CORE_OTEL_ENABLED=false`
- Override DB path: `MACHINE_CORE_OTEL_DB_PATH=/absolute/path/debug.sqlite`
- Adjust sample ratio: `OTEL_TRACES_SAMPLER_ARG=1.0`

The database currently contains:

- `otel_spans`: one row per recorded gRPC/service span
- `otel_span_events`: span events such as session open/close, file load/unload, job start/pause/resume/stop, flash completion, and runtime telemetry ingestion

Useful ad hoc queries:

```sql
SELECT start_time, name, trace_id, attributes_json
FROM otel_spans
ORDER BY id DESC
LIMIT 20;
```

```sql
SELECT event_name, timestamp, attributes_json
FROM otel_span_events
WHERE trace_id = ?
ORDER BY id ASC;
```

## Goa regeneration

From `machine-core/`:

```bash
make goa-gen
make goa-check
```

- `make goa-gen` regenerates Goa artifacts from `design/`.
- `make goa-check` fails if generated files under `gen/` or `goa489139379/` are out of sync.

## Discovery behavior

- Serial devices are discovered from the host using Go serial enumeration.
- Network devices can be injected with `MACHINE_CORE_NETWORK_DEVICES=host1:23,host2:23`.
- Simulator devices are used only when no real devices are discovered.
