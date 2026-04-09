# Direct Account Sync Notes

Last verified: 2026-04-09
Local Codex CLI: `codex-cli 0.118.0`
Upstream source commit: `25a0f6784d359c2d0308ce98ea3660413431cdf1`

## Summary

`caw` can fetch profile identity and live usage information directly from stored `auth.json` records without depending on the shared `codex app-server`.

This is now implemented in [profile_usage.go](D:/Working/codex-auth-wrapper/internal/codex/profile_usage.go).

## Local Identity Data

These fields are available from the stored ChatGPT auth JWT alone:

- email
- plan type
- linked workspace/account id
- linked user id

The relevant upstream parsing path is:

- [token_data.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/login/src/token_data.rs#L11)
- [token_data.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/login/src/token_data.rs#L129)

## Live Usage Data

Codex’s backend client fetches live usage with:

- `GET https://chatgpt.com/backend-api/wham/usage`
- `Authorization: Bearer <access_token>`
- `ChatGPT-Account-Id: <workspace/account id>`

The relevant upstream source is:

- [client.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/backend-client/src/client.rs#L136)
- [client.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/backend-client/src/client.rs#L257)

The response includes fields useful to `caw`:

- `email`
- `plan_type`
- `user_id`
- `account_id`
- `rate_limit.primary_window.used_percent`
- `rate_limit.primary_window.reset_at`
- `rate_limit.secondary_window.used_percent`
- `rate_limit.secondary_window.reset_at`
- `additional_rate_limits`
- `credits`

## Token Refresh

When the stored access token is stale, Codex refreshes it with:

- `POST https://auth.openai.com/oauth/token`
- `client_id = app_EMoamEEZ73f0CkXaXp7hrann`
- `grant_type = refresh_token`

The relevant upstream source is:

- [manager.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/login/src/auth/manager.rs#L85)
- [manager.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/login/src/auth/manager.rs#L666)
- [manager.rs](D:/Working/codex-auth-wrapper/hidden/codex-src/codex-rs/login/src/auth/manager.rs#L779)

`caw` persists refreshed tokens back into the wrapper-managed profile auth file while preserving unknown top-level fields.

## UX Mapping

`caw` intentionally maps the returned windows to user-facing language:

- `primary_window` -> `5-hour`
- `secondary_window` -> `weekly`

This matches current Codex behavior and the live payload observed on 2026-04-09. If upstream changes these semantics, prefer the upstream window duration over the label.

## Revalidation Procedure

When Codex changes, re-check these items:

1. Run `codex --version`.
2. Re-open the upstream paths above in the latest Codex source.
3. Confirm `backend-client` still uses `/wham/usage`.
4. Confirm refresh still uses `/oauth/token` with the same request shape.
5. Confirm the usage payload still includes primary and secondary windows with reset timestamps.
6. Run a live probe against a stored wrapper profile and compare the observed fields with this document.

If any of those drift, update:

- [profile_usage.go](D:/Working/codex-auth-wrapper/internal/codex/profile_usage.go)
- [service.go](D:/Working/codex-auth-wrapper/internal/broker/service.go)
- [app.go](D:/Working/codex-auth-wrapper/internal/homeui/app.go)
- this document
