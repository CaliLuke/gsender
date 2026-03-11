---
name: gsender-release-checklist
description: Pre-release reliability and packaging checklist for gSender, including migration-aware gates.
---

# gSender Release Checklist

Use this before publishing any tagged release or edge package.

## 1) Version & Scope Freeze
- Confirm target version in `package.json` is correct for the release branch.
- Validate all intended changes are merged and migration-impacting work is documented.
- If `machine-core` changed, ensure the Goa contract and generated code are in sync.
- Update release notes (or changelog section) with:
  - user-visible changes
  - migration risk notes
  - known limitations

## 2) Verification Gates
- `npm run lint`
- `npm run test`
- `npm run test:unit`
- `npm run cypress:open` (or required focused E2E subset) for critical machine flows.
- `cd machine-core && go test ./...` when Go runtime packages changed.
- `cd machine-core && golangci-lint run` if linted Go paths were edited.
- Run at least one manual smoke path in app mode:
  - connect/disconnect
  - open/reconnect lifecycle
  - job start/pause/resume/abort
  - firmware/flash safety flow if modified

## 3) Build Artifacts
- `npm run build` to produce distributable app assets.
- Clean stale outputs:
  - `dist/`, `output/` before packaging reruns.
- For each target you intend to ship, run the matching command from
  [gsender-build](/Users/luca/code/gsender/.agents/skills/gsender-build/SKILL.md):
  - macOS, Windows, Linux commands as required.
- Verify artifacts exist in `output/` and open the target executable once.

## 4) Distribution Readiness
- Confirm signing/notarization keys and env vars are set for mac/windows release windows.
- Update release entry:
  - tag name and title
  - notes copied from changelog/summary
  - attach generated installers and checksums
- Verify install/update behavior on at least one platform for each OS family.
- Confirm no unexpected files in release outputs (stray debug artifacts, stale maps, etc.).

## 5) Post-release
- Push tag from the release branch.
- Confirm `master-latest`/Edge tracking tag updates if your release model uses it.
- Record migration progress and remaining gaps in `RELIABILITY_REFACTOR_PLAN.md` (if applicable).
- Start a smoke pass on the next deployment stage immediately after publish.
