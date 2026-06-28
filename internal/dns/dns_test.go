package dns

import (
	"context"
	"errors"
	"testing"
)

func TestManualManager_NoOp(t *testing.T) {
	var m Manager = manualManager{}
	ctx := context.Background()
	if err := m.Ensure(ctx, "example.com", Record{Name: "a.example.com", Type: "A", Value: "1.2.3.4"}); err != nil {
		t.Errorf("Ensure: %v", err)
	}
	if err := m.Remove(ctx, "example.com", Record{Name: "a.example.com", Type: "A", Value: "1.2.3.4"}); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if _, err := m.Owned(ctx, "example.com"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Owned err = %v, want ErrUnsupported", err)
	}
}

func TestNewManager(t *testing.T) {
	if _, err := NewManager("manual", "", nil); err != nil {
		t.Errorf("manual: %v", err)
	}
	if _, err := NewManager("bogus", "", nil); err == nil {
		t.Error("unsupported type should error")
	}
	if _, err := NewManager("ovh", "ovh-eu", map[string]string{"application_key": "k"}); err == nil {
		t.Error("ovh with missing creds should error")
	}
	if _, err := NewManager("ovh", "", map[string]string{
		"application_key": "k", "application_secret": "s", "consumer_key": "c",
	}); err == nil {
		t.Error("ovh without endpoint should error")
	}
	m, err := NewManager("ovh", "ovh-eu", map[string]string{
		"application_key": "k", "application_secret": "s", "consumer_key": "c",
	})
	if err != nil || m == nil {
		t.Errorf("ovh construct: m=%v err=%v", m, err)
	}
}

func TestAddressRecord(t *testing.T) {
	// Relative name within the zone; A vs AAAA inferred from the IP family.
	a, err := addressRecord("example.com.", Record{Name: "app.example.com", Value: "203.0.113.5"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "app" {
		t.Errorf("name = %q, want app", a.Name)
	}
	if a.RR().Type != "A" {
		t.Errorf("type = %q, want A", a.RR().Type)
	}

	apex, err := addressRecord("example.com.", Record{Name: "example.com", Value: "203.0.113.5"})
	if err != nil {
		t.Fatal(err)
	}
	if apex.Name != "@" {
		t.Errorf("apex name = %q, want @", apex.Name)
	}

	v6, err := addressRecord("example.com.", Record{Name: "app.example.com", Value: "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if v6.RR().Type != "AAAA" {
		t.Errorf("type = %q, want AAAA", v6.RR().Type)
	}

	if _, err := addressRecord("example.com.", Record{Name: "app.example.com", Value: "not-an-ip"}); err == nil {
		t.Error("invalid IP should error")
	}
}

func TestOwnerTXTRecord(t *testing.T) {
	txt := ownerTXTRecord("example.com.", "app.example.com")
	if txt.Name != ownerTXTPrefix+"app" {
		t.Errorf("owner txt name = %q, want %sapp", txt.Name, ownerTXTPrefix)
	}
	if txt.Text != ownerTXTValue {
		t.Errorf("owner txt value = %q, want %q", txt.Text, ownerTXTValue)
	}
}
