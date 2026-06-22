package orchestrator

import (
	"reflect"
	"testing"
)

func TestResolveEnv(t *testing.T) {
	provider := map[string]string{"DB": "pg://x", "TOK": "abc"}
	getenv := func(k string) (string, bool) {
		switch k {
		case "REGION":
			return "eu", true
		default:
			return "", false
		}
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    map[string]string
		missing []string
	}{
		{
			name: "empty value -> provider keyed by var name",
			env:  map[string]string{"DB": ""},
			want: map[string]string{"DB": "pg://x"},
		},
		{
			name: "secret token renames",
			env:  map[string]string{"DATABASE_URL": "${secret:DB}"},
			want: map[string]string{"DATABASE_URL": "pg://x"},
		},
		{
			name: "infisical alias",
			env:  map[string]string{"T": "${infisical:TOK}"},
			want: map[string]string{"T": "abc"},
		},
		{
			name: "bare brace = provider",
			env:  map[string]string{"T": "${TOK}"},
			want: map[string]string{"T": "abc"},
		},
		{
			name: "env token",
			env:  map[string]string{"R": "${env:REGION}"},
			want: map[string]string{"R": "eu"},
		},
		{
			name: "literal kept verbatim",
			env:  map[string]string{"LOG": "info"},
			want: map[string]string{"LOG": "info"},
		},
		{
			name: "embedded tokens with surrounding text",
			env:  map[string]string{"URL": "https://${env:REGION}.example.com/${secret:DB}"},
			want: map[string]string{"URL": "https://eu.example.com/pg://x"},
		},
		{
			name:    "missing provider + env refs",
			env:     map[string]string{"A": "${secret:NOPE}", "B": "${env:NOPE}"},
			want:    map[string]string{"A": "", "B": ""},
			missing: []string{"env:NOPE", "secret:NOPE"},
		},
		{
			name:    "unknown scheme is missing",
			env:     map[string]string{"A": "${vault:KEY}"},
			want:    map[string]string{"A": ""},
			missing: []string{"vault:KEY"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, missing := resolveEnv(tt.env, provider, getenv)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolved = %v, want %v", got, tt.want)
			}
			var miss []string
			for _, r := range missing {
				miss = append(miss, r.String())
			}
			if !reflect.DeepEqual(miss, tt.missing) {
				t.Errorf("missing = %v, want %v", miss, tt.missing)
			}
		})
	}
}

func TestEnvUsesProvider(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"empty value", map[string]string{"X": ""}, true},
		{"secret token", map[string]string{"X": "${secret:K}"}, true},
		{"bare brace", map[string]string{"X": "${K}"}, true},
		{"env token only", map[string]string{"X": "${env:K}"}, false},
		{"literal only", map[string]string{"X": "v"}, false},
		{"mixed env + secret", map[string]string{"X": "${env:A}", "Y": "${secret:B}"}, true},
		{"no env", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := envUsesProvider(tt.env); got != tt.want {
				t.Errorf("envUsesProvider(%v) = %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestExpand_unterminatedTokenKeptLiteral(t *testing.T) {
	// A malformed ${ with no closing } is preserved verbatim, no panic.
	got, missing := resolveEnv(map[string]string{"X": "a${env:B"}, nil, func(string) (string, bool) { return "", false })
	if got["X"] != "a${env:B" || len(missing) != 0 {
		t.Fatalf("got %q missing %v", got["X"], missing)
	}
}
