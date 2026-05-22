package secrets

import (
	"context"
	"testing"
)

func TestFake_GetAll(t *testing.T) {
	f := NewFake(map[string]string{"A": "1", "B": "2"})
	all, err := f.GetAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if all["A"] != "1" || all["B"] != "2" {
		t.Errorf("unexpected map: %v", all)
	}
}

func TestFake_Get(t *testing.T) {
	f := NewFake(map[string]string{"KEY": "val"})
	v, err := f.Get(context.Background(), "KEY")
	if err != nil || v != "val" {
		t.Errorf("want 'val', got %q %v", v, err)
	}
}

func TestFake_NotFound(t *testing.T) {
	f := NewFake(nil)
	_, err := f.Get(context.Background(), "MISSING")
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
	v, err := f.Get(context.Background(), "X")
	if err != nil || v != "42" {
		t.Errorf("want '42', got %q %v", v, err)
	}
}

// Fake satisfies Provider interface — compile-time check.
var _ Provider = (*Fake)(nil)
