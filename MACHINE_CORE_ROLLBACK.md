# Machine Core Rollback Path

## Goal

Provide a fast rollback path if the Go `machine-core` runtime causes parity or stability issues in production.

## Primary Rollback

Disable the Go runtime entirely and return to the legacy Node path.

Set either:

- `MACHINE_CORE_TRANSPORT=legacy`
- or `MACHINE_CORE_MODE=legacy`

Then restart the app/server process.

When unset or set to a Go value (`grpc`, `goa`, `go`), the Go transport path may be enabled again.

## Partial Rollback

If only one migrated slice is unstable, disable that sidecar path instead of disabling all Go integration.

Available scoped flags:

- `MACHINE_CORE_SESSION_OPEN_ENABLED=false`
- `MACHINE_CORE_SESSION_ATTACH_ENABLED=false`
- `MACHINE_CORE_SESSION_CLOSE_ENABLED=false`
- `MACHINE_CORE_FILE_SHADOW_ENABLED=false`
- `MACHINE_CORE_JOB_SHADOW_ENABLED=false`

These flags are evaluated by the Node sidecar layer in `src/server/services/machine-core/sidecar.js`.

Current meaning of the scoped write-path flags:

- `session_open` controls Go session admission before the legacy transport opens.
- `session_attach` controls Go resolve/replay on reconnect or multi-client attach.
- `session_close` controls Go session close on teardown.
- `file_shadow` now gates Go authority over file load/unload acceptance before legacy file side-effects run.
- `job_shadow` now gates Go authority over start/pause/resume/stop acceptance before legacy controller commands run.

## Recommended Rollback Order

1. Disable `file_shadow` and `job_shadow` first if the issue is write-path related.
2. Disable `session_attach` if reconnect/replay behavior is unstable.
3. Disable `session_open` and `session_close` if session lifecycle ownership is unstable.
4. Disable the entire Go transport (`MACHINE_CORE_TRANSPORT=legacy`) if the issue is broader or unclear.

## Validation After Rollback

After restarting with rollback flags:

1. Connect to a known controller.
2. Reconnect a second client if relevant.
3. Load and unload a file.
4. Start, pause, resume, and stop a short job.
5. Confirm logs no longer report `machine-core handled ... via Go sidecar` for the disabled paths.

## Notes

- This rollback path preserves the current migration strategy: Node remains the safety net until parity is proven.
- The rollback controls are operational only; they do not remove Go code from the build.
