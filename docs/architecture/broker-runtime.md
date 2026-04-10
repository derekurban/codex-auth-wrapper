# Broker Runtime Architecture

Updated: 2026-04-09

## Runtime model

CAW runs one per-user broker process and one shared stock `codex app-server`.
Every visible `caw` terminal is a separate host session that talks to the same
broker.

The stock Codex runtime lives in `~/.codex`. CAW stores its own metadata and
profile vault in `~/.codex-auth-wrapper`.

## Ownership boundaries

- `SessionManager`
  - Owns live wrapper-session state.
  - Tracks host/gateway connectivity, busy/idle status, and active thread
    identity for the current CAW windows.

- `SwitchCoordinator`
  - Owns the global profile-switch state machine.
  - Decides when a switch is immediate, pending, forced, cancelled, or ready
    to commit.

- `RuntimeController`
  - Owns profile auth materialization into `~/.codex/auth.json`.
  - Owns app-server start, stop, reuse, and auth epoch advancement.

- `Gateway`
  - Observes stock Codex websocket traffic and translates it into session
    events.
  - Observation must not block websocket frame delivery to the visible Codex
    client. Session bookkeeping and switch reconciliation happen off the proxy
    hot path so a slow broker callback cannot strand the user on stale
    "Working" output after the backend has already completed.
  - It is not allowed to mutate persisted state directly.

- `HostSessionRuntime`
  - Owns one CAW terminal's Codex child process and reload/relaunch policy.
  - Owns the ConPTY-backed terminal boundary for live Codex sessions so CAW
    can manage input, resize propagation, reloads, and stale-child recovery
    without giving the real console directly to stock Codex.
  - While Codex is live, `Ctrl+C` is CAW-owned input. It returns that window to
    Home instead of forwarding the interrupt into stock Codex. On Home, the
    existing TUI `Ctrl+C` behavior still exits the wrapper.
  - Applies a bounded wait during reload; if a stale Codex child does not exit
    after the shared app-server has already moved on, CAW terminates that stale
    child and continues the relaunch path.

## Session lifetime

Wrapper sessions are live-only.

- Return Home and re-enter within the same CAW window: resume the tracked
  thread.
- Exit CAW entirely and launch it again: start a fresh wrapper session.
- The session mirror on disk exists for observability and coordination while
  the broker is alive, not for cross-process recovery.

## Runtime invariants

- `state.selected_profile_id` always means the currently active auth context.
- A pending switch must not mutate `selected_profile_id` until commit.
- Normal account switching must not interrupt an active Codex turn.
- Switching auth means reconnecting/relaunching Codex, not mutating tokens in
  place inside a live Codex client.
- Reloaded sessions must not wait forever for a stale Codex child to exit once
  the auth epoch has advanced.
