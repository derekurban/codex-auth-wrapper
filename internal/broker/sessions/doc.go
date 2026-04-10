// Package sessions owns live CAW wrapper-session state. It tracks host and
// gateway connectivity, current wrapper session state, active thread identity,
// and busy or idle turn state for the CAW windows that currently exist. The
// package mirrors live sessions to sessions.json for observability, but that
// file is not a recovery source for a new broker process.
package sessions

