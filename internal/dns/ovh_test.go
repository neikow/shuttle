package dns

import (
	"context"
	"net/netip"
	"testing"

	"github.com/libdns/libdns"
)

// fakeRecordClient is an in-memory libdns provider for testing ovhManager. It
// keys records by RR name+type so SetRecords upserts and DeleteRecords removes,
// mirroring the libdns contract closely enough for the manager's logic.
type fakeRecordClient struct {
	recs     []libdns.Record
	getErr   error
	setErr   error
	delErr   error
	lastZone string
}

func (f *fakeRecordClient) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	f.lastZone = zone
	return f.recs, f.getErr
}

func (f *fakeRecordClient) SetRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	if f.setErr != nil {
		return nil, f.setErr
	}
	for _, r := range recs {
		rr := r.RR()
		replaced := false
		for i, ex := range f.recs {
			er := ex.RR()
			if er.Name == rr.Name && er.Type == rr.Type {
				f.recs[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			f.recs = append(f.recs, r)
		}
	}
	return recs, nil
}

func (f *fakeRecordClient) DeleteRecords(_ context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	if f.delErr != nil {
		return nil, f.delErr
	}
	for _, r := range recs {
		rr := r.RR()
		for i, ex := range f.recs {
			er := ex.RR()
			if er.Name == rr.Name && er.Type == rr.Type {
				f.recs = append(f.recs[:i], f.recs[i+1:]...)
				break
			}
		}
	}
	return recs, nil
}

func TestOVHManager_EnsureWritesRecordAndOwnerTXT(t *testing.T) {
	f := &fakeRecordClient{}
	m := &ovhManager{c: f}
	if err := m.Ensure(context.Background(), "example.com", Record{Name: "app.example.com", Type: "A", Value: "203.0.113.5"}); err != nil {
		t.Fatal(err)
	}
	// Expect the A record and its owner TXT.
	var haveA, haveTXT bool
	for _, r := range f.recs {
		switch v := r.(type) {
		case libdns.Address:
			if v.Name == "app" {
				haveA = true
			}
		case libdns.TXT:
			if v.Name == ownerTXTPrefix+"app" && v.Text == ownerTXTValue {
				haveTXT = true
			}
		}
	}
	if !haveA || !haveTXT {
		t.Errorf("after Ensure: haveA=%v haveTXT=%v recs=%+v", haveA, haveTXT, f.recs)
	}
	if f.lastZone != "" && f.lastZone != "example.com." { // set only on Get
		t.Errorf("zone = %q, want example.com.", f.lastZone)
	}
}

func TestOVHManager_OwnedFiltersByOwnerTXT(t *testing.T) {
	f := &fakeRecordClient{recs: []libdns.Record{
		// Owned A (has owner TXT).
		libdns.Address{Name: "app", IP: netip.MustParseAddr("203.0.113.5")},
		libdns.TXT{Name: ownerTXTPrefix + "app", Text: ownerTXTValue},
		// Owned CNAME (has owner TXT).
		libdns.CNAME{Name: "www", Target: "lb.example.net."},
		libdns.TXT{Name: ownerTXTPrefix + "www", Text: ownerTXTValue},
		// Foreign A (no owner TXT) — must be ignored.
		libdns.Address{Name: "foreign", IP: netip.MustParseAddr("198.51.100.9")},
		// A TXT that isn't ours.
		libdns.TXT{Name: "random", Text: "not-ours"},
	}}
	m := &ovhManager{c: f}

	owned, err := m.Owned(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range owned {
		got[r.Name] = r.Type + ":" + r.Value
	}
	if len(got) != 2 {
		t.Fatalf("owned = %+v, want exactly app + www", got)
	}
	if got["app.example.com"] != "A:203.0.113.5" {
		t.Errorf("app = %q", got["app.example.com"])
	}
	if got["www.example.com"] != "CNAME:lb.example.net" {
		t.Errorf("www = %q", got["www.example.com"])
	}
	if _, ok := got["foreign.example.com"]; ok {
		t.Error("foreign record (no owner TXT) must not be returned")
	}
}

func TestOVHManager_RemoveDeletesRecordAndOwnerTXT(t *testing.T) {
	f := &fakeRecordClient{recs: []libdns.Record{
		libdns.Address{Name: "app", IP: netip.MustParseAddr("203.0.113.5")},
		libdns.TXT{Name: ownerTXTPrefix + "app", Text: ownerTXTValue},
	}}
	m := &ovhManager{c: f}
	if err := m.Remove(context.Background(), "example.com", Record{Name: "app.example.com", Type: "A", Value: "203.0.113.5"}); err != nil {
		t.Fatal(err)
	}
	if len(f.recs) != 0 {
		t.Errorf("after Remove, records remain: %+v", f.recs)
	}
}

func TestOVHManager_ErrorPaths(t *testing.T) {
	errBoom := errInjected{}
	m := &ovhManager{c: &fakeRecordClient{getErr: errBoom}}
	if _, err := m.Owned(context.Background(), "example.com"); err == nil {
		t.Error("Owned should surface GetRecords error")
	}
	m = &ovhManager{c: &fakeRecordClient{setErr: errBoom}}
	if err := m.Ensure(context.Background(), "example.com", Record{Name: "a.example.com", Type: "A", Value: "1.2.3.4"}); err == nil {
		t.Error("Ensure should surface SetRecords error")
	}
	m = &ovhManager{c: &fakeRecordClient{delErr: errBoom}}
	if err := m.Remove(context.Background(), "example.com", Record{Name: "a.example.com", Type: "A", Value: "1.2.3.4"}); err == nil {
		t.Error("Remove should surface DeleteRecords error")
	}
	// Invalid IP fails before any client call.
	m = &ovhManager{c: &fakeRecordClient{}}
	if err := m.Ensure(context.Background(), "example.com", Record{Name: "a.example.com", Type: "A", Value: "nope"}); err == nil {
		t.Error("Ensure should reject an invalid IP")
	}
}

type errInjected struct{}

func (errInjected) Error() string { return "boom" }

func TestZoneAndFQDNHelpers(t *testing.T) {
	if got := zoneFQDN("example.com"); got != "example.com." {
		t.Errorf("zoneFQDN = %q", got)
	}
	if got := zoneFQDN("example.com."); got != "example.com." {
		t.Errorf("zoneFQDN idempotent = %q", got)
	}
	if got := fqdnTrim("app.example.com."); got != "app.example.com" {
		t.Errorf("fqdnTrim = %q", got)
	}
	if got := fqdnDot("app"); got != "app." {
		t.Errorf("fqdnDot = %q", got)
	}
}
