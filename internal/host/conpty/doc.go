// Package conpty owns the Windows pseudo-console boundary for live Codex
// sessions. It gives CAW full control over the visible child terminal so host
// runtime policy can manage interrupts, reloads, resize propagation, and stale
// child termination without handing the real console directly to stock Codex.
package conpty
