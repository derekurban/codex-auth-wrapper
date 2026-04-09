# Codex Auth Wrapper Lifecycle And State Machine Spec v1

## Status

Draft v1.

## Purpose

This document defines the runtime state transitions that the wrapper must support.

It complements:

- Home TUI spec
- Storage/auth spec
- JSON schema spec

## Principle

State transitions should be understandable from the user story:

- open `caw`
- link or select an account
- continue into Codex
- return with `F12`
- switch account
- reload all linked sessions

The wrapper should model those transitions explicitly instead of relying on implicit flags alone.

## State Domains

There are three distinct but related state machines:

1. broker state
2. visible wrapper session state
3. profile health state

## Broker State Machine

### States

- `starting`
- `home_ready`
- `launching_codex`
- `active`
- `switching_profile`
- `reloading_sessions`
- `degraded`
- `stopped`

### Meanings

#### `starting`

Wrapper runtime is initializing files, runtime directories, and server prerequisites.

#### `home_ready`

Home TUI is available and no Codex launch is currently in progress.

#### `launching_codex`

Wrapper is materializing the selected profile into runtime and starting or attaching Codex for a visible session.

#### `active`

At least one visible session is in or entering Codex under the current auth epoch.

#### `switching_profile`

Wrapper is changing the selected profile and restarting the shared runtime.

#### `reloading_sessions`

Wrapper has completed the profile switch and is resuming affected linked sessions under the new auth epoch.

#### `degraded`

A required condition failed, such as:

- no usable profile
- missing auth artifact
- shared server restart failure
- invalid state file

#### `stopped`

Wrapper runtime is intentionally offline.

### Allowed transitions

- `starting` -> `home_ready`
- `starting` -> `degraded`
- `home_ready` -> `launching_codex`
- `home_ready` -> `switching_profile`
- `home_ready` -> `degraded`
- `launching_codex` -> `active`
- `launching_codex` -> `degraded`
- `active` -> `home_ready`
- `active` -> `switching_profile`
- `active` -> `degraded`
- `switching_profile` -> `reloading_sessions`
- `switching_profile` -> `degraded`
- `reloading_sessions` -> `home_ready`
- `reloading_sessions` -> `active`
- `reloading_sessions` -> `degraded`
- `degraded` -> `home_ready`
- `degraded` -> `switching_profile`
- `home_ready` -> `stopped`
- `degraded` -> `stopped`

### Disallowed shortcuts

The wrapper should not jump directly:

- `active` -> `stopped` without controlled shutdown
- `switching_profile` -> `active` without passing through reload completion

## Visible Wrapper Session State Machine

### States

- `home`
- `launching_codex`
- `in_codex`
- `returning_home`
- `reloading`
- `resume_failed`
- `closed`

### Meanings

#### `home`

This visible wrapper session is currently showing the wrapper-owned TUI.

#### `launching_codex`

This visible wrapper session has requested entry into Codex and is waiting for the child launch/resume path to complete.

#### `in_codex`

This visible wrapper session is currently inside stock Codex.

#### `returning_home`

The user pressed `F12` and the wrapper is capturing session state and returning to Home.

#### `reloading`

This visible session is being reloaded because the shared auth context changed.

#### `resume_failed`

This visible session could not be resumed automatically.

#### `closed`

This visible session is no longer active.

### Allowed transitions

- `home` -> `launching_codex`
- `launching_codex` -> `in_codex`
- `launching_codex` -> `resume_failed`
- `in_codex` -> `returning_home`
- `returning_home` -> `home`
- `in_codex` -> `reloading`
- `home` -> `reloading`
- `reloading` -> `in_codex`
- `reloading` -> `home`
- `reloading` -> `resume_failed`
- `resume_failed` -> `home`
- `home` -> `closed`
- `resume_failed` -> `closed`
- `in_codex` -> `closed`

## Profile Health State Machine

### States

- `unknown`
- `healthy`
- `warning`
- `exhausted`
- `auth_failed`
- `disabled`

### Meanings

#### `unknown`

No reliable recent usage or auth information is available.

#### `healthy`

The profile is usable and not near configured warning thresholds.

#### `warning`

The profile is usable but nearing a configured threshold.

#### `exhausted`

The profile is considered temporarily unusable due to hard limit exhaustion.

#### `auth_failed`

The stored auth artifact appears invalid or unauthorized.

#### `disabled`

The user disabled this profile manually.

### Allowed transitions

- `unknown` -> `healthy`
- `unknown` -> `warning`
- `unknown` -> `auth_failed`
- `healthy` -> `warning`
- `healthy` -> `exhausted`
- `healthy` -> `auth_failed`
- `warning` -> `healthy`
- `warning` -> `exhausted`
- `warning` -> `auth_failed`
- `exhausted` -> `healthy`
- `exhausted` -> `warning`
- `auth_failed` -> `healthy`
- any active state -> `disabled`
- `disabled` -> `unknown`
- `disabled` -> `healthy`

## Critical Transition Flows

### Flow 1: First successful account link

1. broker `home_ready`
2. user enters Add Account
3. wrapper invokes stock Codex login
4. wrapper copies resulting `auth.json`
5. wrapper creates `profiles/<id>/profile.json`
6. wrapper updates `state.json.selected_profile_id`
7. broker remains `home_ready`

### Flow 2: Continue into Codex

1. session `home`
2. broker `home_ready`
3. user presses `Enter`
4. session -> `launching_codex`
5. broker -> `launching_codex`
6. runtime auth materialized if needed
7. shared server verified or started
8. Codex child launched
9. session -> `in_codex`
10. broker -> `active`

### Flow 3: Return from Codex with `F12`

1. session `in_codex`
2. user presses `F12`
3. session -> `returning_home`
4. wrapper captures current `active_thread_id`
5. Codex child stopped or detached
6. session -> `home`
7. if no other sessions remain in Codex, broker may return to `home_ready`

### Flow 4: Switch profile from Home

1. broker `home_ready` or `active`
2. user chooses new profile
3. broker -> `switching_profile`
4. wrapper snapshots current session/thread mapping
5. runtime auth copied back to old profile if needed
6. new profile auth copied into shared runtime
7. shared server restarted
8. broker -> `reloading_sessions`
9. affected sessions -> `reloading`
10. each session either returns to `in_codex` or `home`
11. broker -> `active` if any sessions are in Codex, else `home_ready`

### Flow 5: Resume failure

1. session `reloading` or `launching_codex`
2. resume attempt fails
3. session -> `resume_failed`
4. broker stays `home_ready`, `active`, or enters `degraded` depending on severity

## File Update Responsibilities By Transition

### On profile switch

Must update:

- `state.json.selected_profile_id`
- `state.json.current_auth_epoch_id`
- `broker.json.active_profile_id`
- `broker.json.active_auth_epoch_id`
- `broker.json.switch_context`
- `sessions.json.sessions[*].last_seen_auth_epoch_id` after successful reload

### On return from Codex

Must update:

- `sessions.json.sessions[session_id].active_thread_id`
- `sessions.json.sessions[session_id].state`
- timestamps for that session

### On `/resume` or thread switch observed inside Codex

Must update:

- `sessions.json.sessions[session_id].active_thread_id`

## Crash Recovery Rules

### Broker crash or forced exit

On next startup, the wrapper should:

1. read `state.json`, `broker.json`, and `sessions.json`
2. if `broker_state` was `switching_profile` or `reloading_sessions`, treat startup as interrupted recovery
3. reconcile selected profile and runtime auth artifact
4. move to `home_ready` or `degraded`

### Session crash

If one visible session exits unexpectedly:

- mark that session `closed`
- do not disturb other sessions

## Degraded Mode Entry Rules

The broker should enter `degraded` when:

- selected profile is missing `auth.json`
- no usable enabled profiles exist
- runtime auth materialization fails
- shared server restart fails
- core JSON state is invalid and unrecoverable automatically

## Degraded Mode Exit Rules

The broker may leave `degraded` when:

- a usable profile is selected
- required auth artifact exists again
- shared server health is restored
- invalid state is repaired

## Invariants

The implementation should preserve these invariants:

1. At most one profile is selected.
2. The active runtime profile matches the selected profile after successful steady-state transitions.
3. Each visible wrapper session has at most one active Codex thread id.
4. A profile switch increments auth epoch exactly once.
5. `F12` never silently discards a known active thread id if it can be captured.
