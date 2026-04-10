// Package host owns the visible CAW terminal runtime. It manages the Home-to-
// Codex handoff, the active Codex child process for one terminal, and the
// relaunch or return-home behavior when auth epochs change. It must not own
// broker-wide switching or persistence policy.
package host

