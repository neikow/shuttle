package dns

import "context"

// manualManager is the no-op provider: the operator manages records by hand.
// Ensure/Remove do nothing (Shuttle creates nothing), and Owned reports
// ErrUnsupported since there is no API to read — the reconciler surfaces what the
// user should create rather than acting.
type manualManager struct{}

func (manualManager) Ensure(context.Context, string, Record) error { return nil }
func (manualManager) Remove(context.Context, string, Record) error { return nil }
func (manualManager) Owned(context.Context, string) ([]Record, error) {
	return nil, ErrUnsupported
}
