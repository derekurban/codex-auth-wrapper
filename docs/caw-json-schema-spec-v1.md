# Codex Auth Wrapper JSON Schema Spec v1

## Status

Draft v1.

This document defines the wrapper-owned JSON files under:

```text
~/.codex-auth-wrapper/
```

These are wrapper-managed schemas.
They are distinct from upstream Codex-owned files such as `auth.json`.

## Schema Design Rules

### Rule 1: Version every file

Every wrapper-owned top-level JSON file must include:

- `schema_version`

Initial value:

```json
"schema_version": 1
```

### Rule 2: Prefer explicit objects over implicit arrays

Top-level files should use object envelopes rather than raw arrays.

### Rule 3: Keep Codex-owned data opaque

The wrapper must not normalize upstream `auth.json` contents into wrapper JSON files.

Profile auth artifacts should remain separate files:

```text
profiles/<id>/auth.json
```

### Rule 4: IDs are stable

The following ids must be treated as stable:

- profile id
- wrapper session id
- auth epoch id

### Rule 5: Thread ids are Codex-native

`thread_id` values stored by the wrapper must be the exact Codex-native ids the wrapper observed.

## File Inventory

Required v1 files:

- `state.json`
- `sessions.json`
- `broker.json`
- `profiles/<id>/profile.json`

Optional v1 support files:

- `events.jsonl`
- `runtime-lock.json`

## `state.json`

### Purpose

Global wrapper state that is not specific to one visible session.

### Authority

This file is the source of truth for:

- selected profile id
- profile ordering
- current auth epoch id
- wrapper-level defaults

### Schema

```json
{
  "schema_version": 1,
  "selected_profile_id": "personal-1",
  "profile_order": ["personal-1", "work-1"],
  "current_auth_epoch_id": "epoch-0000042",
  "next_auth_epoch_counter": 43,
  "home_screen": {
    "last_focus": "accounts_list",
    "last_selected_account_row": "personal-1"
  },
  "created_at": "2026-04-09T19:00:00Z",
  "updated_at": "2026-04-09T19:20:00Z"
}
```

### Field meanings

- `selected_profile_id`
  The currently selected wrapper-managed account profile.

- `profile_order`
  The visual ordering of profiles in the home/manage-accounts TUI.

- `current_auth_epoch_id`
  The currently active auth generation loaded into the shared runtime.

- `next_auth_epoch_counter`
  Monotonic counter used to generate the next epoch id.

- `home_screen.last_focus`
  Optional TUI restoration hint for the home screen.

- `home_screen.last_selected_account_row`
  Optional TUI restoration hint for the currently highlighted profile row.

### Constraints

- `selected_profile_id` should be null only when no profiles exist
- `profile_order` must contain only existing profile ids
- `current_auth_epoch_id` must match `broker.json.active_auth_epoch_id`

## `sessions.json`

### Purpose

Registry of visible wrapper sessions.

### Authority

This file is the source of truth for wrapper-managed visible-session continuity, not for Codex conversation contents.

### Schema

```json
{
  "schema_version": 1,
  "sessions": {
    "sess-01hsv2k5f3m0q2k1p9w8e7r6t5": {
      "session_id": "sess-01hsv2k5f3m0q2k1p9w8e7r6t5",
      "state": "at_home",
      "cwd": "D:\\Working\\repo-a",
      "active_thread_id": "thr_abc123",
      "last_known_profile_id": "personal-1",
      "last_seen_auth_epoch_id": "epoch-0000042",
      "resume_pending": false,
      "resume_allowed": true,
      "codex_child_pid": null,
      "last_entered_codex_at": "2026-04-09T19:10:00Z",
      "last_returned_home_at": "2026-04-09T19:12:00Z",
      "created_at": "2026-04-09T19:03:00Z",
      "updated_at": "2026-04-09T19:12:00Z"
    }
  },
  "updated_at": "2026-04-09T19:12:00Z"
}
```

### Session state enum

Allowed `state` values:

- `home`
- `launching_codex`
- `in_codex`
- `returning_home`
- `reloading`
- `resume_failed`
- `closed`

### Field meanings

- `session_id`
  Stable wrapper session id.

- `cwd`
  Working directory last used when entering Codex from this wrapper session.

- `active_thread_id`
  Current Codex-native thread id associated with this wrapper session.

- `last_known_profile_id`
  Profile id this wrapper session last used when entering Codex.

- `last_seen_auth_epoch_id`
  Auth epoch this session last successfully used.

- `resume_pending`
  Whether the wrapper intends to resume this session on next enter/reload.

- `resume_allowed`
  Whether auto-resume remains valid for this session.

- `codex_child_pid`
  Current stock Codex child pid if present.

### Constraints

- `active_thread_id` may be null for never-started sessions
- `resume_pending=true` should imply `resume_allowed=true`
- `state=closed` sessions may be retained briefly, but should be pruned eventually

## `broker.json`

### Purpose

Snapshot of broker/runtime state for the single shared local instance.

### Authority

This file is the source of truth for:

- broker lifecycle state
- shared runtime generation
- current active profile at runtime
- global switch/reload context

### Schema

```json
{
  "schema_version": 1,
  "broker_state": "active",
  "active_auth_epoch_id": "epoch-0000042",
  "active_profile_id": "personal-1",
  "server": {
    "state": "healthy",
    "listen_url": "ws://127.0.0.1:4517",
    "auth_mode": "capability-token",
    "started_at": "2026-04-09T19:04:00Z",
    "last_restart_reason": "profile_switch"
  },
  "switch_context": {
    "in_progress": false,
    "from_profile_id": null,
    "to_profile_id": null,
    "initiated_by_session_id": null,
    "initiated_at": null
  },
  "updated_at": "2026-04-09T19:20:00Z"
}
```

### Broker state enum

Allowed `broker_state` values:

- `starting`
- `home_ready`
- `launching_codex`
- `active`
- `switching_profile`
- `reloading_sessions`
- `degraded`
- `stopped`

### Server state enum

Allowed `server.state` values:

- `starting`
- `healthy`
- `stopping`
- `failed`
- `stopped`

### Field meanings

- `active_auth_epoch_id`
  Current auth generation loaded into runtime.

- `active_profile_id`
  Profile currently materialized into runtime.

- `switch_context`
  Optional in-flight switch metadata for global reload operations.

### Constraints

- `active_profile_id` should equal `state.json.selected_profile_id`
- `active_auth_epoch_id` should equal `state.json.current_auth_epoch_id`

## `profiles/<id>/profile.json`

### Purpose

Metadata for one wrapper-managed account profile.

### Authority

This file is the source of truth for wrapper-owned per-profile metadata.

It is not the source of truth for auth payload contents.

### Schema

```json
{
  "schema_version": 1,
  "id": "personal-1",
  "name": "Personal 1",
  "enabled": true,
  "auth_file": "auth.json",
  "selection_priority": 100,
  "status": {
    "health": "healthy",
    "five_hour_usage_percent": 42,
    "weekly_usage_percent": 18,
    "five_hour_window_label": "5h",
    "weekly_window_label": "weekly",
    "last_checked_at": "2026-04-09T19:18:00Z",
    "warning_state": "none"
  },
  "last_selected_at": "2026-04-09T19:10:00Z",
  "created_at": "2026-04-09T19:01:00Z",
  "updated_at": "2026-04-09T19:18:00Z"
}
```

### Health enum

Allowed `status.health` values:

- `unknown`
- `healthy`
- `warning`
- `exhausted`
- `auth_failed`
- `disabled`

### Warning enum

Allowed `status.warning_state` values:

- `none`
- `five_hour_near_limit`
- `weekly_near_limit`
- `both_near_limit`

### Field meanings

- `id`
  Stable profile id used as the directory name.

- `name`
  User-facing editable profile label.

- `enabled`
  Whether this profile may be selected.

- `auth_file`
  Must be `"auth.json"` in v1.

- `selection_priority`
  Reserved for future auto-selection rules.

### Constraints

- `id` must equal the containing directory name
- `auth_file` must refer to a sibling file that exists for usable profiles

## Optional `events.jsonl`

### Purpose

Append-only operational event log.

### Format

JSON Lines.

Example entry:

```json
{"ts":"2026-04-09T19:10:00Z","event":"profile_switched","from_profile_id":"personal-1","to_profile_id":"work-1","auth_epoch_id":"epoch-0000043"}
```

### Recommendation

This file should be optional but strongly recommended for implementation.

It is useful for:

- debugging
- recovery
- reconstructing recent transitions after a crash

## Derived State Rules

The wrapper should avoid duplicating the same authority in multiple places unless one copy is explicitly a cache.

### Canonical ownership

- selected profile: `state.json`
- active runtime profile: `broker.json`
- visible wrapper sessions: `sessions.json`
- profile metadata: `profiles/<id>/profile.json`
- auth payload contents: `profiles/<id>/auth.json`

## JSON Validation Rules

On startup, the wrapper should validate:

1. all required files exist or can be initialized
2. `state.json.selected_profile_id` refers to an existing profile if non-null
3. every profile listed in `profile_order` exists
4. every usable profile has `profiles/<id>/auth.json`
5. `sessions.json` contains only object keys matching `session_id`

If validation fails, the wrapper should prefer repair or degraded mode over destructive reset.

## ID Formats

### Profile id

Regex:

```text
^[a-z0-9]+(?:-[a-z0-9]+)*$
```

### Wrapper session id

Recommended format:

```text
sess-<ulid>
```

Regex:

```text
^sess-[0-9A-HJKMNP-TV-Z]{26}$
```

### Auth epoch id

Recommended format:

```text
epoch-<zero-padded integer>
```

Regex:

```text
^epoch-[0-9]{7,}$
```

## Example Minimal First-Run State

### `state.json`

```json
{
  "schema_version": 1,
  "selected_profile_id": null,
  "profile_order": [],
  "current_auth_epoch_id": "epoch-0000000",
  "next_auth_epoch_counter": 1,
  "home_screen": {
    "last_focus": "primary_action",
    "last_selected_account_row": null
  },
  "created_at": "2026-04-09T19:00:00Z",
  "updated_at": "2026-04-09T19:00:00Z"
}
```

### `sessions.json`

```json
{
  "schema_version": 1,
  "sessions": {},
  "updated_at": "2026-04-09T19:00:00Z"
}
```

### `broker.json`

```json
{
  "schema_version": 1,
  "broker_state": "starting",
  "active_auth_epoch_id": "epoch-0000000",
  "active_profile_id": null,
  "server": {
    "state": "stopped",
    "listen_url": null,
    "auth_mode": null,
    "started_at": null,
    "last_restart_reason": null
  },
  "switch_context": {
    "in_progress": false,
    "from_profile_id": null,
    "to_profile_id": null,
    "initiated_by_session_id": null,
    "initiated_at": null
  },
  "updated_at": "2026-04-09T19:00:00Z"
}
```
