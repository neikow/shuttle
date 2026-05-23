package agent

import (
	"reflect"
	"testing"
)

func TestTargetScale(t *testing.T) {
	cases := map[int]int{0: 1, 1: 2, 2: 4, 3: 6}
	for in, want := range cases {
		if got := targetScale(in); got != want {
			t.Errorf("targetScale(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestDiffIDs(t *testing.T) {
	cases := []struct {
		name     string
		cur, old []string
		want     []string
	}{
		{"one new", []string{"a", "b"}, []string{"a"}, []string{"b"}},
		{"all new", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"none new", []string{"a"}, []string{"a", "b"}, nil},
		{"preserves order", []string{"x", "y", "z"}, []string{"y"}, []string{"x", "z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diffIDs(c.cur, c.old); !reflect.DeepEqual(got, c.want) {
				t.Errorf("diffIDs(%v,%v) = %v, want %v", c.cur, c.old, got, c.want)
			}
		})
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("shortID long = %q", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID short = %q", got)
	}
}
