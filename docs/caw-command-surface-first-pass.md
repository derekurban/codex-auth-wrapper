# Codex Auth Wrapper Command Surface First Pass

## Product Name

This document assumes:

- product name: Codex Auth Wrapper
- repository/package name: `codex-auth-wrapper`
- CLI command: `caw`

Implementation status note as of 2026-04-10:

- this is a first-pass command design doc
- current implemented commands are `caw`, `caw status`, `caw shutdown`, and `caw broker start|stop|restart`
- `doctor`, `broker logs`, and the `F12` return path remain planned rather than implemented

## Design Direction

The command surface should be intentionally small because normal management belongs inside the TUI.

That means:

- `caw` is the main command
- account management should not require memorizing CLI subcommands
- external commands should exist mainly for status, debugging, and service control

## Main Command

### `caw`

The default command should open the wrapper home page.

```text
caw
```

This should:

- start or attach to the local shared wrapper runtime
- render the wrapper-owned home TUI
- allow the user to continue into Codex with `Enter`

It should not bypass the home page.

## TUI-First Principle

Normal user actions should happen in the TUI:

- add account
- switch account
- manage linked accounts
- continue into Codex
- return from Codex with `F12`

These should not require separate commands in normal use.

## Minimal External Command Surface

The first-pass CLI should stay small.

Recommended commands:

```text
caw
caw status
caw doctor
caw broker start
caw broker stop
caw broker restart
caw broker logs
```

Everything else should be considered optional unless needed for automation or debugging.

## `caw`

### Purpose

Open the wrapper home TUI.

### Behavior

- if no accounts exist, show first-account onboarding state
- if accounts exist, show linked accounts overview
- `Enter` continues into Codex if a selected usable account exists
- `Enter` starts account setup if no accounts exist

## `caw status`

### Purpose

Provide a quick textual view of current wrapper state without opening the TUI.

### Typical use cases

- shell scripting
- checking whether the broker is healthy
- seeing which account is active
- debugging if the TUI cannot launch cleanly

### Expected output

Example:

```text
Product: Codex Auth Wrapper
State: active
Active account: personal-1
5-hour usage: 42%
Weekly usage: 18%
Connected sessions: 3
Shared server: healthy
```

## `caw doctor`

### Purpose

Validate the wrapper environment when things feel wrong.

### Checks should include

- broker runtime existence
- shared server health
- selected account validity
- managed auth files present and readable
- current shared Codex home consistency
- resume metadata availability

## `caw broker start`

### Purpose

Explicitly start the shared broker/service layer.

### Notes

This is not required for normal usage if `caw` autostarts the broker.

This exists for:

- service workflows
- debugging
- power users

## `caw broker stop`

### Purpose

Gracefully stop the shared broker/service layer.

### Notes

This should:

- stop accepting new sessions
- shut down the shared server
- disconnect or drain linked sessions

## `caw broker restart`

### Purpose

Force a broker restart outside the TUI.

Useful for:

- debugging
- recovery
- internal testing

## `caw broker logs`

### Purpose

Show recent broker events for debugging and support.

### Expected content

- server starts/stops
- auth context switches
- near-limit detections
- linked-session reloads
- resume failures

## Commands That Should Not Be Primary

The following should not be part of the normal day-to-day user story:

- `caw accounts add`
- `caw accounts use`
- `caw accounts list`
- `caw sessions list`

Those can exist later for automation or internal debugging, but the product should not depend on them.

The user should be able to manage the product entirely from the TUI.

## Optional Hidden Or Secondary Commands

If needed for support, testing, or automation, these can exist as secondary commands:

```text
caw accounts list
caw accounts add
caw accounts switch <id>
caw sessions list
caw sessions reload
```

But these should be treated as:

- support tools
- automation tools
- debug tools

not as the primary UX.

## TUI Key Surface

Because the TUI is the main interface, the key surface matters as much as the CLI surface.

First-pass key model:

- `Enter` continue / confirm
- `F12` return from Codex to the wrapper home page
- `A` add account
- `M` manage accounts
- `S` switch selected account
- `Q` quit

This key surface should be visible on the home page.

## Relationship Between `caw` And Codex

The command model should reinforce the right architecture:

- users launch `caw`
- `caw` owns wrapper navigation and auth context
- `caw` launches stock Codex as the coding experience
- `F12` returns from Codex back into `caw`

That means `caw` is not replacing Codex commands inside Codex.
It is wrapping entry, auth, and session continuity around them.

## Command Philosophy

The correct philosophy for this product is:

- TUI-first
- CLI-minimal
- debug-capable

The shell command surface should stay small enough that it does not compete with the TUI model.

## Open Questions

1. Should `caw status` have a `--json` mode from day one?
2. Should support/debug commands be documented publicly, or hidden from normal help output?
3. Should `caw broker start` run in the foreground by default, or background immediately?
4. Should there be an explicit `caw home` command, or should `caw` alone always be that?
