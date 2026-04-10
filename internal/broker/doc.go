// Package broker contains the broker composition root used by the public CAW
// binary. The root package stays intentionally thin: it exposes service startup
// and coordinates bounded-context packages for session ownership, global
// profile switching, runtime control, and gateway observation.
package broker

