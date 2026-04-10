# Global Profile Switch Lifecycle

Updated: 2026-04-09

## Trigger

A global profile switch begins when a Home session selects a different account.
The switch is not tied to re-entering Codex.

## Decision flow

1. If no live busy Codex sessions exist, switch immediately.
2. If any live busy Codex sessions exist, create a pending switch.
3. While pending:
   - Home shows the current active profile and marks the target as pending.
   - `Enter` is disabled in all Home windows.
   - Only the initiating Home window may force or cancel the switch.
4. When all busy sessions become idle, commit exactly once.

## Commit behavior

On commit:

1. Copy the live runtime auth back into the old profile vault.
2. Bump the auth epoch.
3. Materialize the target profile auth into stock Codex home.
4. Restart the shared `codex app-server`.
5. Broadcast one reload notice for live sessions.
6. Idle live Codex sessions relaunch and resume the tracked thread.
7. Home sessions refresh to the new active profile.

If a reloaded Codex child does not exit promptly after the app-server restart,
the host runtime applies a bounded grace period and then terminates the stale
child so the session can continue the relaunch/resume path instead of hanging on
stale terminal output.

## Forced behavior

Forced switch exists as an explicit override. It may interrupt active work and
is only allowed from the initiating Home window.

## Busy/idle truth

Busy and idle state comes from gateway-observed app-server notifications:

- `turn/started`
- `turn/completed`
- `thread/status/changed`

Terminal output and persisted thread files are not considered authoritative for
switch safety.
