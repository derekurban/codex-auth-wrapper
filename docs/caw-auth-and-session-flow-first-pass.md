# Codex Auth Wrapper Auth And Session Flow First Pass

## Purpose

This document isolates the most important behavioral contract in Codex Auth Wrapper:

- how accounts are created and linked
- how auth contexts are selected and switched
- how Codex sessions resume across wrapper navigation
- how all linked wrapper instances react to an auth-context switch

## Terminology

### Account profile

A wrapper-managed account entry with:

- user-facing name
- stable id
- persisted auth material
- health and usage metadata

### Auth context

The currently selected account profile whose auth material is loaded into the shared Codex runtime.

### Shared runtime

The broker-owned Codex home and server context currently used by linked wrapper sessions.

### Visible wrapper session

One visible `caw` instance opened by the user.

### Active thread id

The Codex-native thread id currently associated with a visible wrapper session.

## Canonical User Story

The wrapper must support this story cleanly:

1. user opens `caw`
2. user creates and links an account
3. user enters Codex
4. user works in a thread
5. user returns to the wrapper home page with `F12`
6. user switches accounts
7. wrapper reloads the shared runtime
8. user presses `Enter`
9. user lands back in the same Codex thread, but under the new auth context

If this flow feels coherent, the product is on the right track.

## Account Creation Flow

### Step 1: Start from the home page

If no accounts exist, the home page should say:

```text
Press Enter to set up your first account.
```

### Step 2: Name the account profile

The wrapper should prompt for:

- profile name
- auto-generated id stub

Example:

```text
Profile name: Personal 1
Profile id:   personal-1
```

Rules:

- id is derived from the entered name
- id should be lowercase kebab-case
- id should be editable before final confirmation

### Step 3: Hand off to Codex login

The wrapper should then start the stock Codex login flow and clearly explain that the browser will open.

### Step 4: Persist managed auth material

When the login succeeds, the wrapper should copy the resulting `auth.json` into wrapper-managed profile storage as opaque account data.

Implementation note:

- the actual Codex runtime format today is `auth.json`, not `auth.jsonc`
- the wrapper should not reinterpret or rewrite auth internals in v1
- the wrapper should persist per-profile copies under wrapper-owned storage, then materialize the active one into the shared Codex home

### Step 5: Confirm success

The user should see:

```text
Account linked successfully.
Press Enter to return to the home page.
```

## Entering Codex From The Home Page

### Precondition

The selected account profile is valid and usable.

### Wrapper behavior

Before launching Codex, the wrapper should:

1. ensure the selected account profile is the active auth context
2. ensure the shared Codex runtime contains that account’s auth material
3. ensure the shared server is running against that runtime
4. resolve the current visible wrapper session’s last known thread id

### Launch behavior

If a current thread id exists for this visible wrapper session:

- launch into that thread

If no current thread id exists:

- launch a normal fresh Codex session

## Returning From Codex To The Wrapper

### Trigger

The user presses `F12`.

### Required wrapper behavior

1. intercept `F12`
2. capture the visible session’s latest known active thread id
3. stop or detach the Codex child cleanly
4. return to the wrapper home TUI

### Required state update

The wrapper must update its record for that visible session so that the home page now knows:

- session id
- selected account profile
- latest active Codex thread id
- whether resume is possible

## Thread Tracking Rules

### Source of truth

Codex-native thread ids are the source of truth.

### Required updates

If the user changes threads inside Codex, the wrapper must learn that.

That includes:

- starting a new thread
- using `/resume`
- otherwise switching active thread context

### Required effect

The next time the user returns to the home page and presses `Enter`, the wrapper should use the updated active thread id.

## Switching Auth Context

### Trigger

The user returns to the home page and selects a different account profile.

### Required warning

The wrapper must explain that auth switching is global for all linked wrapper sessions using that shared runtime.

### Required switch sequence

1. persist the current active auth context back to its managed account profile
2. mark the selected account profile as active
3. materialize that account’s auth into the shared Codex home
4. restart the shared server
5. invalidate and reload all linked wrapper sessions attached to the old auth context
6. preserve each wrapper session’s own active thread id

## What Other Sessions Should Experience

If one wrapper instance switches auth context, all linked wrapper instances must reload.

Expected user-visible notice:

```text
Codex Auth Wrapper is switching auth contexts.
This session will be reloaded and resumed.
```

After reload, each session should return to its own latest thread id where possible.

## Continue After Switching

When the switching user lands back on the home page and presses `Enter`, the expected result is:

- stock Codex opens
- the prior active thread resumes
- the new auth context is active
- `/status` inside Codex reflects the new account context

This is one of the main product promises and should be treated as a critical UX contract.

## Near-Limit Warning Flow

### Trigger conditions

The wrapper should detect warning thresholds for both:

- 5-hour usage
- weekly usage

### Prompt contract

When nearing a threshold, the wrapper should present a TUI choice:

```text
This account is nearing its limit.

Switch now or continue?
```

### Continue path

If the user continues:

- Codex keeps running normally
- no forced switch occurs
- Codex itself will surface the hard exhaustion condition when appropriate

### Switch path

If the user chooses to switch:

- wrapper returns to account selection or performs the selected switch immediately
- shared runtime reload occurs
- linked sessions are resumed appropriately

## Hard Exhaustion Flow

### Expected ownership split

At hard exhaustion:

- Codex owns the in-session "you are out of usage" behavior
- the wrapper owns the recovery path after the user returns to the home page

### Expected user flow

1. Codex surfaces that usage is exhausted
2. user presses `F12`
3. wrapper home page appears
4. user switches auth context
5. wrapper reloads shared runtime
6. user presses `Enter`
7. wrapper resumes the session in the same thread under the new auth context

## Non-Negotiable State To Persist

For each managed account profile:

- profile id
- profile name
- persisted auth material
- last known usage summary
- health state

For each visible wrapper session:

- wrapper session id
- latest active Codex thread id
- selected account profile id at last launch
- cwd
- restart/resume eligibility

For shared runtime state:

- currently selected account profile id
- current broker/server state
- reload generation or auth epoch

## Open Questions

1. Should the wrapper let the user edit the auto-generated profile id after the first successful login, or only before linking?
2. Should `F12` always force a child restart on return, or can some platforms support a more seamless suspend/resume path?
3. Should the wrapper show the current active thread id directly on the home page, or keep that hidden unless the user opens a session details view?
