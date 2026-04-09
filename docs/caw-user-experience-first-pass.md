# Codex Auth Wrapper User Experience First Pass

## Product Name

The product should be described consistently as:

- Codex Auth Wrapper
- `codex-auth-wrapper`
- `caw` as the CLI command

This document replaces the earlier `cbx` framing.

## Product Position

Codex Auth Wrapper should feel like a thin shell around normal Codex that adds shared auth management and a wrapper-owned home screen.

The intended experience is:

- the user launches `caw`
- `caw` opens its own TUI landing page first
- the user can manage accounts and select the active auth context from that TUI
- pressing `Enter` moves the user into stock Codex
- pressing `F12` from Codex returns the user to the wrapper home page

The wrapper should own navigation and auth management.
Codex should remain the actual coding experience.

## Core Mental Model

The user-facing model should be:

> `caw` gives me a home page for account management and session control. When I continue, it launches normal Codex using the currently selected account context.

Important implications:

- account management belongs to the wrapper TUI
- Codex remains stock Codex once entered
- the wrapper tracks the current thread id for each attached session
- returning to the home page does not mean losing the active thread
- switching auth contexts is a global action for all attached sessions on that shared broker

## Primary UX Principle

The wrapper should have exactly two visible modes:

1. Wrapper home / management TUI
2. Stock Codex TUI

The user should move between them intentionally.

Recommended navigation rule:

- `Enter` from the home page enters Codex
- `F12` from Codex returns to the home page

## Main Entry Experience

### Default command

The normal entrypoint should be:

```text
caw
```

This should always open the wrapper home page first.

This is true for:

- first run
- returning users
- users with one account
- users with many accounts

The product should not drop directly into Codex on launch.

## Home Page

### Purpose

The home page is the central management surface for Codex Auth Wrapper.

It should answer these questions immediately:

1. Do I have any linked accounts?
2. Which account is currently selected?
3. How healthy are those accounts?
4. Can I continue into Codex right now?
5. How do I manage accounts or get back here later?

### Layout expectations

The home page should be simple and calm, not dashboard-heavy.

Suggested sections:

- title / product name
- active account summary
- linked accounts list
- usage and health summary
- key hints
- primary action hint

### Required key hints

The home page should visibly teach:

- `Enter` to continue
- account management keys
- `F12` as the way back from Codex to this page

Example footer:

```text
Enter continue into Codex
A add account
M manage accounts
S switch account
F12 return here from Codex
Q quit
```

## Home Page States

### State 1: No accounts exist

If no accounts are configured, the page should be minimal.

Example:

```text
Codex Auth Wrapper

No accounts linked.

Press Enter to set up your first account.

F12 returns here from Codex
Q quits
```

Behavior:

- `Enter` starts first-account setup
- direct continue into Codex should not be available

### State 2: Accounts exist

If accounts exist, the page should show an overview.

Example:

```text
Codex Auth Wrapper

Active account: personal-1
Status: healthy

Accounts
personal-1   42% / 5h   healthy   selected
work-1       12% / 5h   healthy
backup-1     weekly 91% warning

Press Enter to continue.

A add account
M manage accounts
S switch account
F12 return here from Codex
Q quit
```

### State 3: Warning state

If the selected account is nearing its limit, the home page should make that visible.

Example:

```text
Active account: personal-1
Status: nearing 5-hour limit
```

This should not block entry into Codex.

### State 4: Degraded state

If no usable account exists, the home page should remain usable and explicit.

Example:

```text
Codex Auth Wrapper

No usable account is currently available.

You can manage accounts, switch accounts, or add a new one.
```

## First Account Setup Flow

### Expected user path

1. user launches `caw`
2. home page says `Press Enter to set up your first account`
3. user presses `Enter`
4. wrapper opens account creation TUI
5. user enters an account/profile name
6. wrapper auto-populates an id stub
7. user confirms
8. wrapper starts the Codex OAuth login flow
9. browser login completes
10. wrapper persists the resulting auth file in managed storage
11. wrapper shows success screen
12. user presses `Enter`
13. wrapper returns to the home page

### Account naming UX

The first prompt should ask for a user-friendly profile name.

Example:

```text
Account name: Personal 1
Account id:   personal-1
```

Rules:

- the user types any label they want
- the id is auto-generated as a lowercase kebab-case stub
- the user may optionally edit the id before saving

Example:

- `Personal 1` becomes `personal-1`
- `Work Pro Account` becomes `work-pro-account`

### Login handoff UX

After naming the account, the wrapper should explain what happens next.

Example:

```text
Link account: Personal 1
Account id: personal-1

Press Enter to continue to the Codex login flow.
Your browser will open for authentication.
```

### Return from OAuth UX

Once the browser flow returns successfully, the user should see an explicit linked state.

Example:

```text
Account linked successfully.

Name: Personal 1
Id: personal-1

Press Enter to return to the home page.
```

### Persistence expectations

The wrapper should use stock Codex login and then copy the resulting `auth.json` into wrapper-managed storage.

The user expectation should be:

- the account is now managed by Codex Auth Wrapper
- it will appear on the home page
- it can be selected later without redoing login
- the wrapper is using the same auth artifact format Codex itself produced

## Returning User Flow

### Expected path

1. user launches `caw`
2. home page shows linked accounts and current statuses
3. user presses `Enter`
4. wrapper launches stock Codex using the selected auth context

This should feel nearly identical to having logged into Codex directly, except for the wrapper home page existing first.

## Entering Codex

### Expected behavior

Once the user presses `Enter` from the home page with a valid selected account:

- wrapper ensures the selected auth context is materialized into the shared Codex home
- wrapper ensures the shared server is healthy
- wrapper launches stock Codex
- wrapper resumes the current thread for that visible session if one is known
- otherwise wrapper launches a normal Codex session

The user should feel like they are now in ordinary Codex.

### Important fidelity requirement

Inside Codex:

- `Ctrl+C` should still exit normally
- `/resume` should still work normally
- `/status` should reflect the active account after a switch
- Codex should remain visually stock

## Returning To The Wrapper Home Page

### Primary mechanism

`F12` should return the user from Codex back to the wrapper home page.

This is not a quit.
This is a controlled return to the wrapper-owned TUI.

### Expected behavior when pressing `F12`

1. wrapper captures `F12`
2. current Codex session is detached or stopped in a controlled way
3. wrapper records the latest thread id for that visible session
4. wrapper returns to the home page
5. home page now reflects the current thread linkage and selected account

### Required user education

The home page should always remind the user that `F12` brings them back here from Codex.

## Session And Thread Persistence

### Key requirement

The wrapper must maintain a living record of the Codex thread ids being used by each visible wrapper session.

This is critical because the user expects:

- leaving Codex for the home page does not lose their place
- returning into Codex resumes where they left off
- if `/resume` changes the active thread inside Codex, the wrapper learns about that change

### Expected behavior

If the user:

1. enters Codex
2. works in thread A
3. presses `F12`
4. returns to the home page
5. presses `Enter`

then the wrapper should relaunch them into thread A.

If the user:

1. enters Codex
2. uses `/resume` and switches from thread A to thread B
3. presses `F12`
4. returns to the home page
5. presses `Enter`

then the wrapper should relaunch them into thread B.

### User-facing expectation

The wrapper should treat Codex thread ids as the source of truth.
It should not invent an alternate session identity for the conversation itself.

## Account Management Inside The TUI

### Principle

Account management should happen inside the wrapper TUI, not through external management commands.

Users should not need to remember separate CLI commands for ordinary tasks like:

- adding accounts
- switching accounts
- disabling accounts
- viewing account usage

### Suggested account-management actions

Within the home page or a child management view, the user should be able to:

- add account
- rename account label
- edit account id
- switch selected account
- enable or disable account
- view account usage status
- remove account

## Switching Accounts From The Home Page

### Expected flow

1. user returns to home page with `F12`
2. user selects a different linked account
3. wrapper warns that switching is global
4. user confirms
5. wrapper restarts the shared server with the new auth context
6. wrapper updates all connected sessions
7. user presses `Enter` to continue back into Codex

### Required warning

Switching accounts must clearly explain that the effect is global for all linked wrapper instances attached to that shared auth context.

Example:

```text
Switch account?

Current account: personal-1
New account: work-1

This will restart all connected Codex wrapper sessions using the current shared auth context.
```

## Switching Accounts While Other Sessions Exist

### Required behavior

If one wrapper instance changes the active auth context:

- all other linked wrapper instances sharing that context must be reloaded
- the shared server must be restarted
- all affected sessions must be stopped and resumed appropriately

### User expectation

The user should understand:

- auth switching is global
- sessions are not independent auth containers
- the system will bring them back after the restart

### Recommended notice for other sessions

Example:

```text
Codex Auth Wrapper is switching accounts.
This session will be reloaded and resumed.
```

## Auth Context Materialization

### What the user expects

When switching accounts, the user expects the wrapper to:

- load the selected account’s auth context
- place the correct auth payload into the Codex home
- restart the server so the new auth context is actually active

The user should not need to think about `auth.json`, but the system behavior should match that expectation.

### User-visible result

After switching and pressing `Enter` to continue:

- Codex comes back
- the active thread is resumed
- `/status` inside Codex shows the new account context

The ideal feeling is:

- same session
- same thread
- new auth context

## Nearing Limit Warnings

### Desired behavior

If the selected account is nearing either:

- the 5-hour limit
- the weekly limit

then the wrapper should surface a decision point before the user hard-fails.

### Prompt expectation

The user should see a wrapper TUI prompt like:

```text
This account is nearing its limit.

personal-1
5-hour limit: 92%
weekly limit: 71%

Would you like to switch now or continue?

Enter continue
S switch account
Esc cancel
```

### Decision behavior

If the user chooses continue:

- Codex continues normally
- wrapper does not force-switch
- Codex eventually handles its own out-of-usage message when appropriate

If the user chooses switch:

- wrapper returns to account selection
- wrapper restarts with the new auth context

## Running An Account Out Completely

### Expected behavior

If the user ignores the warning and runs the account out:

- Codex should handle its normal "out of usage" behavior
- the wrapper should not try to replace that behavior inside the Codex session

The user’s recovery path should be:

1. press `F12`
2. return to the wrapper home page
3. switch account
4. press `Enter`
5. continue in the resumed session

This is a good v1 tradeoff because it keeps Codex behavior intact while still making auth recovery easy.

## Wrapper-Owned Home Page After Returning From Codex

When returning from Codex using `F12`, the home page should reflect live state such as:

- current selected account
- current session’s last known thread id
- account health and usage
- availability of other accounts

This page should feel like a control room for the current wrapper context, not a generic splash screen.

## Multi-Session Expectations

### Shared behavior

If multiple wrapper instances are attached:

- all of them share the selected auth context
- all of them are subject to a global server restart on account switch
- each should retain its own last known active thread id

### Resume behavior

After a global restart:

- each wrapper instance should be returned to its own thread where possible
- if resume fails for one, that one should land in a recovery state without breaking the others

## Failure And Recovery UX

### No accounts configured

Example:

```text
No accounts linked.

Press Enter to set up your first account.
```

### No usable account available

Example:

```text
No usable account is currently available.

Manage accounts to continue.
```

### Resume failed

Example:

```text
This session could not be resumed automatically.

Last known thread: thr_abc123
Selected account: work-1

Press Enter to open the home page.
```

### Account switch in progress

Example:

```text
Switching auth context...
Restarting shared Codex server...
Resuming linked sessions...
```

## Strong Design Calls In This Draft

This draft assumes:

1. `caw` should always open the wrapper home page first.
2. Account management should be TUI-first, not command-first.
3. `F12` should be the main return path from Codex to the wrapper.
4. Codex itself should remain visually and behaviorally stock.
5. Thread ids from Codex are the source of truth for conversation continuity.
6. Global auth switching should restart all linked sessions sharing that auth context.
7. Near-limit prompts should offer a choice, not force a switch.

## Open UX Questions

1. Should the home page show one-line usage summaries only, or separate 5-hour and weekly columns?
2. Should the auto-generated account id be editable before login, or locked after generation?
3. Should pressing `F12` from Codex always kill and relaunch the Codex child, or should it support a cleaner detach model if technically possible?
4. Should the wrapper home page show the current thread id for the active visible session?
5. Should near-limit prompts appear only on the home page, or also interrupt inside Codex before hard exhaustion?
