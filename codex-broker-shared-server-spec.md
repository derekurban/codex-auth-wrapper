# Codex Broker Shared-Server Spec

## Purpose

This document defines a standalone wrapper/broker architecture for running the stock Codex CLI unchanged while adding:

- shared account/session management
- account usage and rate-limit visibility
- controlled account rotation
- automatic restart and resume
- multi-terminal awareness
- a wrapper-owned overlay UI/hotkey model

The design intentionally avoids modifying or forking Codex itself.

## Primary Goal

Build a separate project that sits on top of Codex and treats Codex as an upstream dependency, not an internal subsystem.

The wrapper should:

- launch and host the stock Codex TUI
- manage one hidden shared `codex app-server`
- maintain one shared runtime environment and one active auth account at a time
- monitor account exhaustion
- rotate auth when needed
- restart attached Codex TUI instances and resume their threads

## Non-Goals

- No patching of Codex source code
- No custom Codex fork for v1
- No simultaneous per-terminal different accounts in one shared server
- No attempt to hot-swap auth inside a still-running Codex TUI process
- No dependence on unstable internal Codex behavior beyond public/observable app-server behavior

## Core Model

One broker daemon owns one shared Codex environment:

- one hidden `codex app-server`
- one shared runtime directory / `CODEX_HOME`
- one active auth epoch
- zero or more attached terminal clients

Each terminal runs a stock Codex TUI connected to that same shared app-server through `--remote`.

Threads are connection-scoped for subscriptions, but auth is treated as environment-global.

This means:

- many terminals may share the same app-server
- many threads may run under that server
- all terminals under that server use the same active account
- account switching is a global broker event

## Design Principles

1. Codex remains upstream and replaceable.
2. The broker owns orchestration, not agent behavior.
3. The wrapper must be restart-safe.
4. Auth switching must be explicit, durable, and observable.
5. Failure should degrade to a recoverable state, not corrupt sessions.
6. User-visible terminal behavior should feel like stock Codex unless the broker overlay is invoked.

## High-Level Architecture

### Components

1. Broker daemon
   - long-lived local process
   - owns the shared app-server lifecycle
   - owns shared auth/account state
   - tracks connected terminal sessions
   - polls usage/rate limits
   - triggers account rotation

2. Terminal wrapper client
   - command the user runs instead of `codex`
   - connects to broker
   - spawns the stock Codex TUI in a PTY/ConPTY
   - passes input/output through unchanged
   - intercepts a reserved broker hotkey
   - can be restarted by broker during auth rotation

3. Shared hidden Codex app-server
   - single server instance per broker
   - shared across all attached terminal clients
   - exposes thread/account/rate-limit APIs

4. Account vault
   - broker-managed storage outside shared runtime directory
   - stores auth material per account
   - stores broker metadata for each account
   - source of truth for account roster and preferences

5. Shared runtime directory
   - the Codex runtime directory used by the shared app-server
   - contains shared sessions/state/config expected by Codex
   - active auth is copied or materialized into this runtime during an auth epoch

## Recommended Process Model

### One broker daemon

- Starts on first wrapper invocation or via explicit `broker start`
- Binds a local IPC endpoint
- Starts one `codex app-server`

### One wrapper client per visible terminal

- Connects to the broker IPC endpoint
- Requests a terminal session attachment
- Broker returns current app-server endpoint and session metadata
- Wrapper launches stock Codex TUI in PTY mode using remote app-server mode

### One shared app-server

- Started by broker only
- Hidden from the user
- Not directly user-managed

## Why Shared Server Is The Right v1

This model minimizes:

- duplicated Codex runtime state
- duplicated app-server processes
- duplicated session indexes and rollout databases
- logic for cross-instance coordination

It also matches the intended behavior:

- all terminals participate in one shared Codex environment
- auth rotation is global
- runtime directories do not fragment

The tradeoff is deliberate:

- terminals cannot use different accounts simultaneously under one broker

If simultaneous multi-account operation is ever required, that becomes v2 by running multiple brokers or multiple broker-managed environments.

## User Experience

### Normal usage

The user runs one wrapper command, for example:

```powershell
cbx
```

or

```powershell
codex-broker tui
```

The wrapper launches the stock Codex TUI and passes through all terminal input/output.

### Broker overlay

The wrapper reserves one escape chord, for example:

- `Ctrl-]`
- `Ctrl-\`
- `F12`

When pressed:

- PTY passthrough pauses
- broker overlay opens
- user can inspect:
  - current account
  - current plan
  - current rate limits
  - all configured accounts
  - estimated account health
  - active attached terminals
  - active threads
- user can choose:
  - resume Codex
  - rotate account now
  - drain and switch
  - mark account disabled
  - view recent broker events/logs

### Automatic account rotation

When the broker decides the active account is exhausted or unusable:

1. Broker enters `draining`
2. Broker notifies attached terminal clients
3. Broker closes or interrupts attached Codex TUI instances
4. Broker swaps auth epoch
5. Broker restarts attached Codex TUI instances
6. Each TUI resumes its last known thread

The target experience is:

- minimal disruption
- no manual logout/login cycle
- no manual resume flow in normal cases

## Broker States

The broker should have explicit top-level states:

- `starting`
- `active`
- `draining`
- `switching`
- `resuming`
- `degraded`
- `stopped`

### State meanings

#### `starting`

- Broker is booting
- app-server may not yet be available
- no new terminal attachments should proceed until ready

#### `active`

- app-server is healthy
- one account epoch is active
- terminal clients may attach
- Codex runs normally

#### `draining`

- no new turns should be started if avoidable
- broker is preparing to rotate auth
- active sessions are being quiesced

#### `switching`

- broker is updating auth material
- Codex TUI instances should not be attached
- app-server may be restarted if required by implementation

#### `resuming`

- terminal clients are being relaunched
- prior thread IDs are being restored

#### `degraded`

- some core requirement failed:
  - no usable account
  - app-server failed to launch
  - resume failed repeatedly
  - runtime/auth mismatch

#### `stopped`

- broker is intentionally offline

## Broker Data Model

### Account record

Each configured account should have metadata like:

```json
{
  "account_id": "acct_primary_01",
  "label": "Personal Plus 1",
  "enabled": true,
  "priority": 100,
  "selection_mode": "automatic",
  "last_known_plan": "plus",
  "last_known_email": "user1@example.com",
  "last_health": "healthy",
  "last_rate_limit_snapshot": {},
  "last_exhausted_at": null,
  "cooldown_until": null,
  "notes": ""
}
```

### Broker session record

One visible terminal session should track:

- wrapper session ID
- PTY process PID
- attached Codex TUI PID
- current thread ID
- desired restart policy
- last known cwd
- last known startup args
- last active timestamp
- session state

### Shared auth epoch record

Track:

- epoch number
- active account ID
- activated at
- reason for activation
- previous account ID
- switch initiator

## Runtime Layout

Suggested root:

```text
D:/Working/codex-broker/
```

Suggested layout:

```text
D:/Working/codex-broker/
  broker.db
  broker.log
  config.toml
  runtime/
    codex-home/
      config.toml
      auth.json
      sessions/
      *_sqlite
  accounts/
    acct_primary_01/
      auth.json
      metadata.json
    acct_primary_02/
      auth.json
      metadata.json
  sockets/
  cache/
```

Notes:

- `runtime/codex-home` is the only `CODEX_HOME` used by the shared app-server
- `accounts/*` contains broker-managed auth snapshots
- if Codex is configured for keyring or auto storage, the broker loses deterministic control; v1 should force file-backed auth

## Auth Strategy

### v1 recommendation

Use file-backed auth only.

At broker startup:

- choose active account
- copy that account auth into shared runtime auth location
- launch app-server against the shared runtime

On auth rotation:

- persist current active auth back to that account vault location
- select next account
- copy next account auth into runtime
- reload or restart server as needed

### Why not hot-swap in place

The operationally safe model is:

- stop Codex TUI clients
- rotate auth
- restart/resume

This is simpler, more observable, and less dependent on internals than trying to mutate auth while active turns are still in flight.

## Thread and Terminal Model

### Important distinction

- Threads are logical conversations
- Terminal sessions are UI attachments
- One terminal normally focuses one primary thread at a time
- The broker manages terminal sessions, not Codex’s internal conversation logic

### Multi-terminal sharing

Many terminals may attach to the same shared server.

Each terminal can:

- start new threads
- resume existing threads
- hold its own current thread focus

The broker should track for each terminal:

- current thread ID
- last resumable thread ID
- whether the thread is mid-turn

## Switching Policy

### Trigger sources

Auth rotation may be triggered by:

- explicit user command
- account exhaustion
- threshold breach
- repeated auth failures
- unauthorized or invalid session
- scheduled/manual drain

### Proposed heuristics

An account may be considered unhealthy when:

- primary usage exceeds configurable threshold
- secondary usage exceeds configurable threshold
- backend returns unauthorized
- repeated failures occur within a short window
- plan/account metadata becomes inconsistent

### Rotation algorithm

1. Mark current epoch `draining`
2. Stop launching new terminal clients
3. Notify attached sessions
4. Wait briefly for active turns to settle, if possible
5. Force-close remaining TUI clients
6. Persist current auth back to vault
7. Select next eligible account
8. Materialize new auth into runtime
9. Confirm shared app-server is healthy under new auth
10. Restart prior terminal sessions
11. Resume previous thread IDs
12. Mark new epoch `active`

## Account Selection Policy

Support both:

- automatic selection
- manual selection

### Automatic selection inputs

- enabled flag
- cooldown
- explicit priority
- recent exhaustion
- rate-limit freshness
- recent success/failure history

### Initial simple strategy

Pick the highest-priority enabled account that:

- is not in cooldown
- is not explicitly exhausted
- has acceptable recent health

## Resume Strategy

For each terminal session, persist enough information to restart and resume:

- terminal session ID
- startup args
- cwd
- last thread ID
- whether resume should be attempted automatically

### Resume flow

1. Wrapper reconnects to broker
2. Broker provides new app-server endpoint metadata
3. Wrapper launches stock Codex in PTY
4. Wrapper issues the normal startup path that results in resuming the last thread

If resume fails:

- show broker overlay
- offer manual thread selection
- do not loop indefinitely

## PTY / Terminal Hosting

### Requirements

- Codex must appear visually identical to stock use
- wrapper must pass stdin/stdout/stderr through losslessly
- `Ctrl+C` should behave predictably
- wrapper hotkey should not be forwarded to Codex

### Windows concerns

Use ConPTY on Windows.

The wrapper should own:

- child process creation
- console resize propagation
- signal/close propagation
- shutdown ordering

### Input model

Default behavior:

- all keys pass through to Codex PTY

Reserved behavior:

- one escape chord intercepted by wrapper

### Output model

Default behavior:

- all PTY output passed directly to user terminal

Overlay behavior:

- PTY rendering paused
- wrapper draws its own full-screen or inline overlay
- on exit from overlay, PTY display restored

## Shared Server Control Plane

The broker should keep one non-PTY control connection to the shared app-server.

Use that connection for:

- `account/read`
- `account/rateLimits/read`
- `thread/list`
- `thread/loaded/list`
- `thread/read`
- `thread/resume` if broker-controlled resume is needed

This control connection must be treated as broker-internal and not user-facing.

## Failure Handling

### No healthy account available

Broker enters `degraded`.

Expected behavior:

- terminate or withhold Codex TUI relaunch
- present overlay or concise error
- let user choose next action:
  - manual account selection
  - disable broker auto-rotation
  - exit

### Shared app-server crash

Broker should:

- detect process exit
- restart app-server
- reconnect control plane
- relaunch affected terminal sessions
- resume threads when possible

### Resume failure

Broker should:

- retry a small bounded number of times
- then fall back to broker UI with session recovery choices

## Logging and Observability

The broker must maintain its own logs independent of Codex logs.

Recommended logged events:

- broker startup/shutdown
- app-server startup/shutdown
- account selection decisions
- rate-limit snapshots
- auth rotation triggers
- terminal attach/detach
- Codex TUI restart events
- thread resume outcomes
- failures and retries

Recommended log structure:

- newline-delimited JSON for machine analysis
- optional human-readable log view in overlay

## Configuration

Suggested config fields:

```toml
[broker]
runtime_root = "D:/Working/codex-broker/runtime"
account_root = "D:/Working/codex-broker/accounts"
hotkey = "ctrl-]"
resume_on_restart = true
graceful_drain_seconds = 8
server_restart_on_switch = true

[selection]
mode = "automatic"
default_account = "acct_primary_01"
exhaustion_cooldown_minutes = 30
primary_limit_threshold_percent = 95
secondary_limit_threshold_percent = 95

[codex]
binary = "codex"
app_server_args = []
tui_args = []
force_cli_auth_credentials_store = "file"
```

## Security Notes

- The broker becomes security-sensitive because it handles auth material.
- Auth files must be permission-restricted.
- Local IPC should bind to loopback or a locked-down named pipe/socket only.
- Do not expose the app-server remotely unless explicit authentication is configured.
- Avoid storing plaintext secrets outside intentionally managed vault paths.

## Implementation Phases

### Phase 1

- broker daemon
- one shared app-server
- one wrapper terminal client
- account vault
- manual account switch
- restart and resume one TUI session

### Phase 2

- multiple terminal clients attached to one broker
- global drain-and-switch
- automatic account selection
- rate-limit polling
- account health UI

### Phase 3

- smarter draining behavior
- better retry and recovery
- richer overlay
- broker-admin commands
- optional background service mode

## Suggested Commands

Examples:

```text
cbx                 # open a broker-managed Codex TUI
cbx broker start    # start broker daemon
cbx broker stop     # stop broker daemon
cbx status          # show broker/account/server status
cbx accounts list   # list configured accounts
cbx accounts use X  # switch active account
cbx accounts doctor # validate account vault entries
cbx sessions list   # list broker-known terminal sessions
```

## Open Questions

1. Should the broker itself start on demand, or run as a persistent background daemon?
2. Should account switching always restart the shared app-server, or only the TUI clients?
3. Should the broker interrupt active turns during drain, or wait for completion by default?
4. How should manual override precedence work when automatic account health says a chosen account is weak?
5. Should the broker maintain its own shadow session registry in addition to app-server thread metadata?
6. What is the cleanest cross-platform hotkey strategy for PTY-hosted terminal apps?

## Recommended First Build

Build the smallest thing that proves the architecture:

1. A broker daemon that launches one shared `codex app-server`
2. A wrapper command that launches stock Codex against that shared server
3. A file-backed account vault
4. A manual account switch action
5. A forced restart-and-resume flow
6. A minimal status overlay

If that works cleanly, then add:

7. multi-terminal attachment tracking
8. automatic usage polling
9. automatic drain-and-switch

## Summary

The broker should not try to become a modified Codex.

It should become a thin orchestration layer that:

- hosts stock Codex
- owns one shared app-server
- owns one shared runtime
- owns account rotation
- restarts and resumes Codex when the auth epoch changes

That keeps the system maintainable, upstream-compatible, and much easier to evolve.
