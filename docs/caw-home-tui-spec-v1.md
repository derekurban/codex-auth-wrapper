# Codex Auth Wrapper Home TUI Spec v1

## Status

Draft v1.

This document is normative where it uses:

- must
- should
- may

## Purpose

This document defines the wrapper-owned TUI that appears when the user runs:

```text
caw
```

This is the primary user interface for Codex Auth Wrapper.

Codex itself remains the primary coding surface after the user continues past this TUI.

## Core Rule

`caw` must open the wrapper home TUI first.

It must not drop directly into Codex.

## TUI Modes

The wrapper TUI must support these screens:

1. Home
2. Add Account
3. Link In Progress
4. Link Success
5. Manage Accounts
6. Switch Account Confirmation
7. Near-Limit Warning
8. Recovery / Degraded

For v1, all management should happen within these TUI screens.

## Navigation Model

### Required keys

The wrapper home TUI must support:

- `Enter`
- `Esc`
- arrow keys
- `Tab` where useful
- `F12`
- `Q`

### Global navigation rules

- `Enter` confirms the current primary action
- `Esc` returns to the previous wrapper screen
- `Q` exits the wrapper when not inside Codex
- `F12` is reserved as the return path from Codex back to the wrapper home TUI

### Home page shortcut hints

The home screen must visibly teach:

- `Enter` continue
- `A` add account
- `M` manage accounts
- `S` switch account
- `Q` quit
- `F12` returns here from Codex

## Screen 1: Home

### Purpose

The Home screen is the default landing page and the control center for the wrapper.

### Required regions

The Home screen must contain:

1. product title
2. active account summary
3. account overview list
4. primary action hint
5. key hints

### Required title

The title must identify the product clearly as:

```text
Codex Auth Wrapper
```

### Home state: no accounts

If no account profiles exist, the Home screen must show:

- no linked accounts message
- `Press Enter to set up your first account`
- key hints

Suggested shape:

```text
Codex Auth Wrapper

No accounts linked.

Press Enter to set up your first account.

Enter set up first account
Q quit
```

### Home state: accounts exist

If one or more account profiles exist, the Home screen must show:

- currently selected account id
- selected account health status
- account overview list
- `Press Enter to continue`

Suggested account overview columns:

- selected marker
- profile id
- 5-hour usage summary
- weekly usage summary
- health

### Home state: no usable selected account

If the selected account is not usable and no alternative is auto-selected, Home must show:

- explicit degraded notice
- account list
- actions to manage or switch accounts

### Enter behavior on Home

If no accounts exist:

- `Enter` must navigate to Add Account

If accounts exist and the selected account is usable:

- `Enter` must continue into Codex

If accounts exist but the selected account is not usable:

- `Enter` should open Manage Accounts or Switch Account flow instead of failing silently

## Screen 2: Add Account

### Purpose

Collect the user-facing profile name and generated profile id before linking a new account.

### Required fields

The Add Account screen must include:

- profile name input
- auto-generated profile id field
- editable id support

### Generation rules

The profile id must:

- default from the entered profile name
- be lowercase
- use kebab-case
- strip or normalize unsupported characters

Example:

- `Personal 1` -> `personal-1`

### Required actions

- `Enter` confirms and moves to Link In Progress
- `Esc` returns to Home

## Screen 3: Link In Progress

### Purpose

Explain that the wrapper is about to use stock Codex login and hand control to that login flow.

### Required message

This screen must communicate:

- account name being linked
- account id being created
- that stock Codex login will be used
- that a browser may open

Example:

```text
Link account: Personal 1
Account id: personal-1

Codex Auth Wrapper will now start the stock Codex login flow.
After login completes, the resulting auth.json will be copied into managed wrapper storage.

Press Enter to continue.
Esc cancel
```

### Required behavior

On `Enter`, the wrapper must:

1. invoke stock Codex login flow
2. wait for it to complete
3. locate the resulting `auth.json`
4. copy that file into the wrapper-managed profile location
5. return to Link Success on success
6. return to Recovery / Degraded on failure

## Screen 4: Link Success

### Purpose

Confirm that a new account profile has been linked successfully.

### Required content

This screen must show:

- profile name
- profile id
- success status
- `Press Enter to return to the home page`

### Required behavior

On `Enter`, the wrapper must:

- mark the new account as selected if this is the first account
- return to Home

## Screen 5: Manage Accounts

### Purpose

Allow TUI-based account administration without requiring shell commands.

### Required functions

The Manage Accounts screen must support:

- view account profile list
- move selection
- set selected profile
- add account
- rename profile label
- enable or disable profile
- remove profile

For v1, remove profile may be gated behind confirmation.

### Required displayed fields

For each profile, show at minimum:

- profile id
- profile name
- selected status
- enabled status
- health
- 5-hour usage summary
- weekly usage summary

## Screen 6: Switch Account Confirmation

### Purpose

Make global consequences explicit before auth context changes.

### Required message

The confirmation must state:

- current selected account
- target account
- that all linked wrapper sessions will be reloaded
- that the shared server will restart

### Required actions

- `Enter` confirm switch
- `Esc` cancel switch

### Required confirm behavior

On confirm, the wrapper must:

1. inspect all live wrapper sessions
2. if any live Codex session is actively running a turn, enter pending-switch state instead of switching immediately
3. if no live Codex session is busy, persist current runtime auth back to the currently selected profile
4. copy target profile auth into the shared Codex runtime
5. restart the shared server
6. mark target profile as selected
7. flag all linked sessions for reload/resume
8. return to Home

### Pending-switch Home behavior

While a global account switch is pending, Home must:

- keep the current active account visibly selected
- mark the requested target account as pending
- show a banner explaining how many active Codex sessions are still blocking the switch
- disable `Enter`
- let only the initiating Home session choose:
  - `force switch now`
  - `cancel pending switch`

Other Home sessions may observe the pending switch, but may not take ownership of it.

## Screen 7: Near-Limit Warning

### Purpose

Warn before hard exhaustion while leaving the decision to the user.

### Trigger

This screen should appear when the selected account crosses configured warning thresholds for:

- 5-hour usage
- weekly usage

### Required content

Show:

- profile id
- relevant usage percentages
- choice to switch now or continue

### Required actions

- `Enter` continue
- `S` switch account
- `Esc` dismiss

### Continue behavior

If the user continues:

- return to prior flow without forced switch

### Switch behavior

If the user chooses switch:

- open Manage Accounts or Switch Account Confirmation

## Screen 8: Recovery / Degraded

### Purpose

Provide a controlled error screen instead of dropping the user into raw failures.

### Required failure cases

The wrapper must be able to land here for:

- failed login/link
- missing or invalid profile auth file
- failed shared server restart
- failed session resume
- no usable account exists

### Required content

The screen must contain:

- concise problem statement
- relevant profile/session context if available
- next action hint

## Continue Into Codex

### Required preconditions

Before continuing from Home into Codex, the wrapper must ensure:

1. selected profile is usable
2. selected profile auth exists in wrapper storage
3. selected profile auth has been materialized into the shared Codex runtime
4. shared Codex server is healthy

### Resume semantics

If the visible wrapper session has a known active Codex thread id:

- continue must resume that thread

If not:

- continue must open a fresh Codex session

## Returning From Codex

### Trigger

Inside Codex, `F12` must return the user to the wrapper Home screen.

### Required behavior

On `F12`, the wrapper must:

1. capture the visible session’s current active Codex thread id
2. stop or detach the Codex child
3. return to Home

### Required note

The wrapper must keep Codex thread ids as the conversation source of truth.

## Multi-Session Behavior

### Required model

All linked wrapper sessions under the same shared runtime must share:

- one selected auth context
- one shared Codex runtime
- one shared server restart cycle

### Required reload behavior

When auth context changes:

- all linked sessions must be invalidated
- Home sessions must refresh immediately
- idle live Codex sessions must be reloaded automatically
- busy live Codex sessions should delay the switch until they become idle unless the user forces the switch
- each session must preserve its own active Codex thread id for resume

## v1 Non-Goals For The Home TUI

The v1 Home TUI does not need:

- charts
- historical analytics
- per-session sub-agent views
- a full command palette
- a plugin system

The v1 goal is controlled auth, clear session continuity, and low-friction navigation.
