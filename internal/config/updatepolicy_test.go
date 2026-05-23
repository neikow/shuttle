package config

import "testing"

func TestNormalizeUpdatePolicy(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", UpdatePolicyRolling, false},
		{"rolling", UpdatePolicyRolling, false},
		{"recreate", UpdatePolicyRecreate, false},
		{"bogus", "", true},
		{"Rolling", "", true}, // case-sensitive
	}
	for _, c := range cases {
		got, err := normalizeUpdatePolicy(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("normalizeUpdatePolicy(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("normalizeUpdatePolicy(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
