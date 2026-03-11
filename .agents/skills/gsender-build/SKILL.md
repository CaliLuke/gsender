---
name: gsender-build
description: Reference for gSender Electron build, packaging, and release-oriented workflows.
---

# gSender Build and Packaging Reference

This skill covers workflows you usually run less often than day-to-day coding.

## Local Dev vs Packaging

- `npm run dev`: full watch flow for dev server + Vite + electron shell.
- `npm run start-dev:nodemon`: runs backend in dev mode only.
- `npm run vite:dev`: frontend-only dev server (port 5173).
- `npm run start` / `npm run start-electron`: run current built artifacts from `dist/`.

## Product Build

- `npm run build-dev`: local dev-target build only.
- `npm run build-prod`: production-target build.
- `npm run build`: alias for production build flow.

## Electron Packaging (Infrequent/Release)

- `npm run build:macos`: package universal macOS.
- `npm run build:macos-x64`: package macOS Intel.
- `npm run build:macos-arm64`: package macOS ARM64.
- `npm run build:windows`: package Windows x64.
- `npm run build:linux`: package Linux x64/arm64.
- `npm run electron-builder` / `npm run electron-builder:debug`: run electron-builder directly.

Use release commands only after validating migration milestones and test gates in `AGENTS.md`.

