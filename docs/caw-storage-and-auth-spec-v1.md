# Codex Auth Wrapper Storage And Auth Spec v1

## Status

Draft v1.

Current implementation note as of 2026-04-10:

- stock Codex home is `~/.codex`
- CAW does not run a parallel wrapper-owned runtime home anymore
- `sessions.json` is a live-session mirror, not a crash-recovery source for a new CAW launch

## Purpose

This document defines the storage model and auth-handling rules for Codex Auth Wrapper.

The most important implementation constraint is:

> Codex Auth Wrapper should not parse or reinterpret Codex auth internals more than necessary.

The wrapper should use stock Codex login to create the auth artifact and then copy that artifact as opaque profile state.

## Design Goals

1. Reuse stock Codex login flow.
2. Treat Codex `auth.json` as an opaque upstream-owned artifact.
3. Avoid sqlite for wrapper-owned state in v1.
4. Persist wrapper state in simple JSON files.
5. Keep all wrapper-owned state under a dedicated wrapper home.

## Wrapper Home Location

This spec interprets the requested `.codex-auth-wrapper` location as a directory in the user home, not a single file.

Recommended location:

```text
~/.codex-auth-wrapper/
```

Reason:

- the wrapper needs multiple persisted artifacts
- a single file would become an awkward pseudo-database immediately
- a small directory of JSON files is simpler and clearer

If desired, a single manifest file can still exist inside that directory.

## Relationship To `~/.codex`

The wrapper must not treat the user’s default `~/.codex` as the source of truth
for managed profiles, but it does use stock `~/.codex` as the active Codex
runtime at steady state.

That means:

- stock Codex login may temporarily write to a Codex home chosen by the wrapper
- the wrapper then copies the resulting `auth.json`
- the wrapper stores the copied file under its own managed profile storage
- the wrapper later materializes one selected profile into the stock shared Codex home at `~/.codex`

## Core Rule For Auth

The wrapper should use stock `codex login` for account linking.

It should not implement its own OAuth stack in v1.

### Required behavior

When linking an account:

1. wrapper invokes stock Codex login flow
2. stock Codex produces `auth.json`
3. wrapper copies the resulting `auth.json` verbatim into managed profile storage
4. wrapper records metadata about that profile separately

This minimizes coupling to upstream auth changes.

## Auth Artifact

### Canonical auth file

Today, the canonical Codex auth artifact is:

```text
auth.json
```

The wrapper must store it and copy it using that filename.

The wrapper should not rename the file contents or convert it to another format.

### Opaque-artifact rule

The wrapper should treat `auth.json` as:

- opaque for persistence
- upstream-owned in shape
- safe to copy byte-for-byte

The wrapper may read only the minimum metadata needed for UI if required, but it should not depend on deep schema interpretation.

## Storage Layout

Recommended wrapper home layout:

```text
~/.codex-auth-wrapper/
  state.json
  sessions.json
  broker.json
  logs/
  profiles/
    personal-1/
      profile.json
      auth.json
    work-1/
      profile.json
      auth.json
  runtime/
    app-server-token.txt
```

## JSON Files

### `state.json`

Global wrapper state.

Suggested contents:

- selected profile id
- profile order
- last active home-screen state
- current auth epoch number
- schema version

### `sessions.json`

Visible wrapper-session registry.

Suggested contents:

- wrapper session ids
- current active Codex thread ids
- per-session cwd
- restart/resume eligibility
- last seen auth epoch

### `broker.json`

Lightweight broker state snapshot.

Suggested contents:

- broker state
- active shared runtime generation
- shared server listen address if needed
- last restart reason

### `profiles/<id>/profile.json`

User-managed metadata for one account profile.

Suggested contents:

- id
- name
- enabled
- created_at
- updated_at
- last_known_health
- last_known_5h_usage_percent
- last_known_weekly_usage_percent
- last_selected_at

### `profiles/<id>/auth.json`

Opaque copy of the stock Codex-generated auth artifact for that profile.

## Why JSON Is Enough For v1

A sqlite database is not required in v1 because:

- one local wrapper instance owns state transitions
- concurrent write complexity is low
- the number of profiles and sessions is small
- state is naturally hierarchical

JSON should be sufficient if writes are done carefully and atomically.

## Required Write Rules

Wrapper JSON writes should be:

- atomic where practical
- versioned with a schema field
- resilient to interruption

Recommended pattern:

1. write temp file
2. flush
3. replace target file atomically

## Shared Runtime Rules

The active shared Codex runtime is the stock user home:

```text
~/.codex/
```

CAW-owned storage under `~/.codex-auth-wrapper/` holds metadata, profile vault
copies, logs, and support files, but not the primary active Codex session store.

## Profile Selection Rules

At any given time:

- exactly one profile is selected for the shared runtime
- that selected profile’s `auth.json` must be copied into `~/.codex/auth.json`

When profile selection changes:

1. current runtime `auth.json` may be copied back to the old selected profile
2. new selected profile `auth.json` must be copied into runtime
3. shared server must restart

## Session Persistence Rules

The wrapper must persist enough session state to coordinate currently live CAW
windows during the lifetime of the running broker.

Current implementation:

- `sessions.json` mirrors live sessions only
- returning to Home and re-entering within the same CAW window preserves that session's tracked thread
- fully exiting CAW and launching it again creates a fresh wrapper session
- shared server restart and auth context switch may still use the live-session mirror while the broker is alive

Minimum required session state:

- wrapper session id
- active Codex thread id
- cwd
- last selected profile id
- last auth epoch seen

## Upstream Compatibility Strategy

The wrapper’s compatibility strategy should be:

- rely on stock Codex to author `auth.json`
- persist `auth.json` copies without reshaping them
- avoid depending on the detailed internal schema

If Codex changes `auth.json` format in a future version:

- login still produces the new file
- wrapper still copies the new file
- wrapper stays compatible unless it made avoidable schema assumptions

This is the main reason to route account linking through stock Codex login.

## Required Spec Decision

The wrapper should prefix profiles by stub-based ids at the directory level, not by rewriting auth internals.

That means:

- `profiles/personal-1/auth.json`
- `profiles/work-1/auth.json`

not:

- mutating fields inside `auth.json`

## Open Questions

1. Should `sessions.json` be a single object file or newline-delimited records?
2. Should the wrapper keep a backup copy of the previously selected runtime `auth.json` before every switch?
3. Should logs remain plain text or JSON lines in v1?
