# CAW Repository Organization

Updated: 2026-04-09

## Purpose

This file maps the generic organization rules onto `codex-auth-wrapper`.
It is the standing guide for where new code belongs and which packages own
which behavior.

## Package map

- `cmd/caw`
  - Framework root and process bootstrap only.
  - Parses CLI args, starts the broker process, and wires the host runtime.
  - Must not own broker state machines or Codex child lifecycle rules.

- `internal/broker`
  - Core orchestration boundary.
  - Owns broker-facing bounded contexts and the IPC façade.
  - Subpackages are the intended ownership seams:
    - `app`: broker composition and IPC routing
    - `sessions`: live session ownership
    - `switchflow`: global profile-switch state machine
    - `runtime`: shared Codex runtime and app-server control
    - `gateway`: websocket translation and observation

- `internal/host`
  - Visible terminal session runtime.
  - Owns one CAW window's Codex child lifecycle, reload handling, and
    return-to-Home behavior.
  - `internal/host/conpty` is the Windows terminal-boundary mechanism package.
    It owns pseudoconsole process hosting and raw input/output forwarding, but
    it must not own broker policy or Home workflow decisions.

- `internal/homeui`
  - Presentation only.
  - Renders the wrapper Home TUI and returns user intents.
  - Must not own broker event races or Codex process behavior.

- `internal/codex`
  - Adapter for stock Codex CLI and app-server integration.
  - Contains login, remote launch, app-server control, and direct account
    sync helpers.

- `internal/ipc`
  - Transport and wire types only.
  - Must not own workflow logic.

- `internal/store`
  - JSON-backed repositories and path helpers.
  - Must not contain product workflow decisions.

- `internal/model`
  - Stable persisted and shared model types.
  - Avoid embedding transient runtime-only state here unless it is mirrored
    intentionally for observability.

## Dependency rules

- `cmd/caw -> internal/host -> internal/homeui | internal/ipc | internal/codex`
- `cmd/caw -> internal/broker`
- `internal/broker/app -> internal/broker/sessions | switchflow | runtime | gateway | store | ipc | model`
- `internal/broker/gateway -> internal/model` only through callback payloads
- `internal/homeui` must not import broker internals
- `internal/store` must not import broker or host packages

## Conventions

- Each bounded-context package gets a `doc.go` describing:
  - what it owns
  - what it collaborates with
  - what it must not own
- Files that enforce non-obvious behavioral invariants should have a short
  responsibility comment at the top.
- `sessions.json` is a live-session mirror only. It is not a recovery source.
- `state.json` and `broker.json` remain the active persisted control plane.
