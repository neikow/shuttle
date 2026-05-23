package config

import (
	"testing"
	"time"
)

func TestParseHumanDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"7 days", 7 * 24 * time.Hour, true},
		{"1 week", 7 * 24 * time.Hour, true},
		{"2 weeks", 14 * 24 * time.Hour, true},
		{"30 minutes", 30 * time.Minute, true},
		{"12h", 12 * time.Hour, true},
		{"1h30m", 90 * time.Minute, true},
		{"90s", 90 * time.Second, true},
		{"1.5d", 36 * time.Hour, true},
		{"7d", 7 * 24 * time.Hour, true},
		{"", 0, false},
		{"soon", 0, false},
		{"7 fortnights", 0, false},
		{"-3h", 0, false},
		{"0 days", 0, false},
	}
	for _, tt := range tests {
		got, err := ParseHumanDuration(tt.in)
		if tt.ok && (err != nil || got != tt.want) {
			t.Errorf("ParseHumanDuration(%q) = %v, %v; want %v, nil", tt.in, got, err, tt.want)
		}
		if !tt.ok && err == nil {
			t.Errorf("ParseHumanDuration(%q) = %v, nil; want error", tt.in, got)
		}
	}
}

func TestNormalizeDeleteVolumes(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", DeleteVolumesManual, true},
		{"manual", DeleteVolumesManual, true},
		{"MANUAL", DeleteVolumesManual, true},
		{"false", DeleteVolumesManual, true},
		{"true", DeleteVolumesImmediate, true},
		{"immediate", DeleteVolumesImmediate, true},
		{"7 days", "7 days", true},
		{"bogus", "", false},
	}
	for _, tt := range tests {
		got, err := normalizeDeleteVolumes(tt.in)
		if tt.ok && (err != nil || got != tt.want) {
			t.Errorf("normalizeDeleteVolumes(%q) = %q, %v; want %q, nil", tt.in, got, err, tt.want)
		}
		if !tt.ok && err == nil {
			t.Errorf("normalizeDeleteVolumes(%q) = %q, nil; want error", tt.in, got)
		}
	}
}

func TestDeleteVolumesPolicy_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		yaml string
		want string
		ok   bool
	}{
		{"delete_volumes: true", DeleteVolumesImmediate, true},
		{"delete_volumes: false", DeleteVolumesManual, true},
		{`delete_volumes: "manual"`, DeleteVolumesManual, true},
		{`delete_volumes: "7 days"`, "7 days", true},
		{`delete_volumes: "nonsense"`, "", false},
		{"delete_volumes: [1,2]", "", false},
	}
	for _, tt := range tests {
		var sf serviceFile
		err := strictDecode([]byte(tt.yaml), &sf)
		if tt.ok && (err != nil || string(sf.DeleteVolumes) != tt.want) {
			t.Errorf("decode %q = %q, %v; want %q, nil", tt.yaml, sf.DeleteVolumes, err, tt.want)
		}
		if !tt.ok && err == nil {
			t.Errorf("decode %q: want error, got %q", tt.yaml, sf.DeleteVolumes)
		}
	}
}
