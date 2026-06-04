package main

import "testing"

func TestResolveEnrollCreds(t *testing.T) {
	tests := []struct {
		name                                          string
		flagURL, flagTok, cfgURL, cfgTok, envURL, env string
		wantURL, wantTok                              string
		wantErr                                       bool
	}{
		{
			name:    "flags win over config and env",
			flagURL: "https://flag", flagTok: "flagtok",
			cfgURL: "https://cfg", cfgTok: "cfgtok",
			envURL: "https://env", env: "envtok",
			wantURL: "https://flag", wantTok: "flagtok",
		},
		{
			name:   "config used when flags empty",
			cfgURL: "https://cfg", cfgTok: "cfgtok",
			envURL: "https://env", env: "envtok",
			wantURL: "https://cfg", wantTok: "cfgtok",
		},
		{
			name:   "env fallback when flags and config empty",
			envURL: "https://env", env: "envtok",
			wantURL: "https://env", wantTok: "envtok",
		},
		{
			name:    "mixed: url from flag, token from config",
			flagURL: "https://flag", cfgTok: "cfgtok",
			wantURL: "https://flag", wantTok: "cfgtok",
		},
		{
			name:    "missing token errors",
			flagURL: "https://flag",
			wantErr: true,
		},
		{
			name:    "missing url errors",
			flagTok: "flagtok",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, tok, err := resolveEnrollCreds(tt.flagURL, tt.flagTok, tt.cfgURL, tt.cfgTok, tt.envURL, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got url=%q tok=%q", url, tok)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tt.wantURL || tok != tt.wantTok {
				t.Fatalf("got url=%q tok=%q, want url=%q tok=%q", url, tok, tt.wantURL, tt.wantTok)
			}
		})
	}
}
