// Package runtime owns the shared stock Codex runtime managed by CAW. It
// materializes profile auth into ~/.codex, controls the hidden shared
// `codex app-server`, advances auth epochs, and persists runtime status in
// broker state. It must not own live session state or profile-switch policy.
package runtime

