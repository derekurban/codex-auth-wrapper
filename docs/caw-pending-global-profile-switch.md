# Codex Auth Wrapper Pending Global Profile Switch

## Status

Verified against local implementation and upstream Codex app-server protocol on 2026-04-09.

## Purpose

This document records why CAW defers global profile switches when other live Codex sessions are still active, and how the broker decides when it is safe to commit the switch.

## User-facing contract

When the user selects a different account from Home:

- if no live Codex session is actively running a turn, the switch happens immediately
- if any live Codex session is actively running a turn, the switch becomes pending
- while pending:
  - the current active account remains selected
  - the target account is shown as pending
  - Home disables `Enter`
  - the initiating Home session may `force` or `cancel` the pending switch
- once all live Codex sessions are idle, the broker commits the switch and reloads live Codex sessions onto the new auth epoch

## Why CAW does not hot-swap auth in place

CAW uses a shared stock `codex app-server` and stock remote Codex clients.

The important constraints are:

- websocket auth is enforced before `initialize`
- app-server initialization is connection-bound
- the shared active auth context is materialized through stock Codex auth files and a restarted shared app-server

Because of that, CAW applies a committed switch by:

1. updating the active auth material
2. restarting the shared app-server
3. relaunching live Codex clients
4. resuming the tracked thread

This is why pending-switch behavior exists. It is safer than interrupting a live turn by default, and more realistic than trying to mutate auth inside an already-running stock Codex process.

## Busy/idle source of truth

CAW treats app-server notifications observed through its websocket gateway as the source of truth for session activity.

Signals used:

- `turn/started`
- `turn/completed`
- `thread/status/changed`

Current interpretation:

- `turn/started` means the session is busy
- `turn/completed` means the session may be idle again
- `thread/status/changed` repairs state when the app-server explicitly reports `active`, `idle`, `notLoaded`, or `systemError`

CAW does not use terminal output parsing as the source of truth for busy/idle decisions.

## Revalidation procedure

If upstream Codex changes and pending-switch behavior needs to be re-verified:

1. inspect the current local Codex app-server docs
2. confirm `turn/started`, `turn/completed`, and `thread/status/changed` still exist and keep their current semantics
3. verify websocket auth is still connection-bound and `initialize` remains single-shot per connection
4. smoke test CAW with:
   - two live Codex windows
   - one pending switch
   - one safe idle commit
   - one forced switch

If any of those assumptions stop holding, update this document and the broker switch/reload implementation together.
