package proxy

// Decision is the outcome of an intercept evaluation for a single CONNECT.
type Decision int

const (
	// Tunnel passes the encrypted CONNECT bytes through unchanged. The
	// upstream certificate reaches the client; the daemon never sees
	// plaintext.
	Tunnel Decision = iota
	// Intercept terminates TLS at the daemon using a leaf cert signed by
	// the per-machine local CA, observes the request/response, and
	// re-encrypts to the upstream. Reserved for hosts in the allowlist.
	Intercept
)

// String renders the decision for logging.
func (d Decision) String() string {
	switch d {
	case Intercept:
		return "intercept"
	case Tunnel:
		return "tunnel"
	default:
		return "unknown"
	}
}

// Decide is a pure function — the single source of truth for whether a host
// gets MITM'd. Both the daemon and tests call this. It must remain trivially
// reviewable: the rule is "in allowlist → intercept; everything else →
// tunnel".
//
// A nil allowlist always returns Tunnel — fail closed. We never default to
// intercepting traffic if the allowlist is missing.
func Decide(host string, allowlist *Allowlist) Decision {
	if allowlist == nil {
		return Tunnel
	}
	if allowlist.ShouldIntercept(host) {
		return Intercept
	}
	return Tunnel
}
