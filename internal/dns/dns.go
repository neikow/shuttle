// Package dns manages the A/AAAA DNS records Shuttle creates for services'
// domains, via pluggable providers. It is the record-management counterpart to
// the ACME DNS-01 challenge wiring (which lives in the orchestrator/Caddy path):
// here we actually point a name at a host's IP.
//
// Ownership is explicit and conservative: each managed address record is paired
// with a companion "owner TXT" so a Manager can tell Shuttle-managed records
// apart from foreign ones and never touch records it does not own. Combined with
// the orchestrator's dns_records ledger, this lets the reconciler detect and heal
// out-of-band drift.
package dns

import (
	"context"
	"errors"
)

// Record is a DNS record Shuttle manages for a service.
type Record struct {
	Name  string // FQDN, e.g. "app.example.com" (no trailing dot)
	Type  string // "A", "AAAA", or "CNAME"
	Value string // the target IP (A/AAAA) or hostname (CNAME)
}

// ErrUnsupported is returned by a Manager that lacks an API for an operation —
// e.g. the manual provider, which leaves all record management to the user.
var ErrUnsupported = errors.New("dns: operation not supported by this provider")

// Manager creates, updates, and removes the address records Shuttle owns within
// one provider's zone, and lists the records it owns there (for drift
// detection). A Manager establishes ownership by writing a companion owner TXT
// alongside each address record; it must never modify or delete records it does
// not own.
type Manager interface {
	// Ensure makes the zone reflect record r (A/AAAA) plus its owner TXT,
	// creating or updating as needed.
	Ensure(ctx context.Context, zone string, r Record) error
	// Remove deletes record r and its owner TXT from the zone (no-op if absent).
	Remove(ctx context.Context, zone string, r Record) error
	// Owned returns the address records Shuttle owns in the zone (those carrying a
	// matching owner TXT), with their current provider-side values — the basis for
	// drift detection. Returns ErrUnsupported for providers without a read API.
	Owned(ctx context.Context, zone string) ([]Record, error)
}

// Owner TXT marker written alongside each managed record. The reconciler treats
// only address records with a matching owner TXT as Shuttle-managed.
const (
	ownerTXTPrefix = "_shuttle-owner."
	ownerTXTValue  = "heritage=shuttle"
)

// ownerTXTName returns the owner-TXT FQDN that marks ownership of fqdn's record.
func ownerTXTName(fqdn string) string { return ownerTXTPrefix + fqdn }
