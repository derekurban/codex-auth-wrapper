// Package switchflow contains the global profile-switch state machine. It
// decides whether a switch is immediate, pending, forced, cancelled, or ready
// to commit based on the active profile, current pending state, and live
// session readiness. It does not start or stop Codex itself; it only returns
// decisions for the broker runtime to execute.
package switchflow

