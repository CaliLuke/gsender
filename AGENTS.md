# Repository Guidelines

## Migration Objective

This repository is in an active migration from the existing JavaScript/Node controller core to a Go-based machine runtime. The long-term goal is a cleaner, faster, and more reliable execution path via the `machine-core` boundary while preserving existing UI behavior until parity is reached.

## Migration Policy
- Core migration details moved to:
  - [gsender-goa-migration skill](/Users/luca/code/gsender/.agents/skills/gsender-goa-migration/SKILL.md)
- Keep `machine-core` as the lifecycle/session boundary and treat Node/Electron as integration until parity is proven.

## Project Structure & Module Organization
- `src/app`: React/Electron UI, styles, and app routes.
- `src/server`: Core Node runtime (controllers, CNC engine orchestration, device/serial handling).
- `src/electron-app`, `src/main.js`, `src/server-cli.js`: Electron bootstrap and runtime entrypoints.
- `scripts/`: Build/dev helper scripts and packaging automation.
- `test/`: Runtime regression tests and fixtures.
- `__tests__/`: Frontend component tests.
- `cypress/`: End-to-end test suite.
- `machine-core/`: New Go machine runtime boundary, with `design/` (Goa contract), `gen/` (generated transport), and Go module files. This is the target replacement for current JavaScript controller/session logic.
- `dist/` and `output/`: Build artifacts; do not edit committed outputs unless release build outputs are intentionally updated.

## Build, Test, and Development Commands
- `npm install && npm run install:packages`: install root and app dependencies.
- `npm run dev`: full watch pipeline (server rebuild + Vite HMR + electron shell).
- `npm run start-dev:nodemon`: launch server in dev mode only.
- `npm run vite:dev`: frontend dev server only (port 5173).
- `npm run start` / `npm run start-electron`: run current built artifacts from `dist/`.
- `npm run build`: run production build pipeline (no native distribution packaging).
- `npm run lint`: run eslint + stylint.
- `cd machine-core && goa gen github.com/Sienci-Labs/gsender/machine-core/design`: regenerate Goa contracts and gRPC code from the design file.
- Infrequent release/packaging commands and gates live in:
  - [gsender-build skill](/Users/luca/code/gsender/.agents/skills/gsender-build/SKILL.md)
  - [gsender release checklist](/Users/luca/code/gsender/.agents/skills/gsender-release-checklist/SKILL.md)

## Coding Style & Naming Conventions
- Follow existing project style enforced by ESLint/TypeScript-aware tooling.
- Keep file/function naming consistent with current code: `kebab-case` files, `camelCase` functions/variables, `PascalCase` React components.
- Use existing command and config naming patterns from `package.json`.
- In Go design files under `machine-core/design`, prefer deterministic field ordering and keep `gen/` untouched; edits belong in non-generated packages.

## Testing Guidelines
- `npm run test`: runs Tap-based backend-oriented tests in `test/*.js`.
- `npm run test:unit`: runs Jest tests in `__tests__/`.
- `npm run cypress:open`: run full E2E suite interactively.
- `machine-core` currently relies on `go test` and `golangci-lint` when implementing runtime packages; run generated-code checks only after interface changes are finalized.


## Commit & Pull Request Guidelines
- Use concise, imperative commit titles in the style seen in recent history (for example, `fix ...`, `add ...`, `remove ...`).
- Include issue context or ticket IDs in the body when available (e.g., `[MI-xxxx]`).
- PRs should include:
  - summary of behavior change and impacted modules,
  - validation commands executed,
  - for UI changes, screenshots or short video for manual verification,
  - and a note on any migration impact for `machine-core`.
