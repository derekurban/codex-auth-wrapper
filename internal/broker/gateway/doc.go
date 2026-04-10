// Package gateway owns the thin websocket layer between visible CAW-hosted
// Codex clients and the shared stock Codex app-server. It validates per-session
// gateway tokens, rewrites cwd-sensitive requests, and translates websocket
// frames into typed session lifecycle observations. It must not mutate CAW
// store files directly or decide global profile-switch behavior.
package gateway

