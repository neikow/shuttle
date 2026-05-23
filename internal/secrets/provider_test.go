package secrets

import (
	"context"
	"testing"
)

func TestFake_GetAll(t *testing.T) {
	f := NewFake(map[string]string{"A": "1", "B": "2"})
	all, err := f.GetAll(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if all["A"] != "1" || all["B"] != "2" {
		t.Errorf("unexpected map: %v", all)
	}
}

func TestFake_Get(t *testing.T) {
	f := NewFake(map[string]string{"KEY": "val"})
	v, err := f.Get(context.Background(), Scope{}, "KEY")
	if err != nil || v != "val" {
		t.Errorf("want 'val', got %q %v", v, err)
	}
}

func TestFake_NotFound(t *testing.T) {
	f := NewFake(nil)
	_, err := f.Get(context.Background(), Scope{}, "MISSING")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if _, ok := err.(ErrNotFound); !ok {
		t.Errorf("unexpected error type: %T %v", err, err)
	}
}

func TestFake_Set(t *testing.T) {
	f := NewFake(nil)
	f.Set("X", "42")
	v, err := f.Get(context.Background(), Scope{}, "X")
	if err != nil || v != "42" {
		t.Errorf("want '42', got %q %v", v, err)
	}
}

func TestFake_Scope(t *testing.T) {
	f := NewFake(map[string]string{"A": "default"})
	f.SetScope(Scope{Env: "staging"}, map[string]string{"A": "staging-val", "B": "only-staging"})

	// Default scope sees the default set.
	if v, _ := f.Get(context.Background(), Scope{}, "A"); v != "default" {
		t.Errorf("default scope A = %q, want 'default'", v)
	}
	// Named scope sees its own set, isolated from the default.
	if v, _ := f.Get(context.Background(), Scope{Env: "staging"}, "A"); v != "staging-val" {
		t.Errorf("staging scope A = %q, want 'staging-val'", v)
	}
	all, _ := f.GetAll(context.Background(), Scope{Env: "staging"})
	if all["B"] != "only-staging" || len(all) != 2 {
		t.Errorf("staging GetAll = %v, want {A,B}", all)
	}
	if _, err := f.Get(context.Background(), Scope{Env: "staging"}, "default-only"); err == nil {
		t.Error("staging scope should not see default-only keys")
	}
}

// Fake satisfies Provider interface — compile-time check.
var _ Provider = (*Fake)(nil)
