package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// ── generateHexToken ─────────────────────────────────────────────────────────

func TestGenerateHexToken_Length(t *testing.T) {
	tok := generateHexToken()
	if len(tok) != 64 {
		t.Errorf("token len = %d, want 64", len(tok))
	}
}

func TestGenerateHexToken_Unique(t *testing.T) {
	a, b := generateHexToken(), generateHexToken()
	if a == b {
		t.Error("two generated tokens should be different")
	}
}

// ── prompter ─────────────────────────────────────────────────────────────────

// TestPrompter_AdvSkippedWithoutFlag asserts an advanced prompt returns its
// default without consuming input when --advanced is off, while an essential
// prompt still reads the line.
func TestPrompter_AdvSkippedWithoutFlag(t *testing.T) {
	p := newPrompter(strings.NewReader("essential-answer\n"), io.Discard, false)
	if got := p.adv("advanced?", "def"); got != "def" {
		t.Errorf("adv without --advanced = %q, want default %q", got, "def")
	}
	if got := p.ask("essential?", "fallback"); got != "essential-answer" {
		t.Errorf("ask = %q, want consumed line", got)
	}
}

// TestPrompter_AdvPromptedWithFlag asserts an advanced prompt reads input when
// --advanced is on.
func TestPrompter_AdvPromptedWithFlag(t *testing.T) {
	p := newPrompter(strings.NewReader("custom\n"), io.Discard, true)
	if got := p.adv("advanced?", "def"); got != "custom" {
		t.Errorf("adv with --advanced = %q, want %q", got, "custom")
	}
}

// ── shared assertion helpers ─────────────────────────────────────────────────

func assertContains(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Errorf("expected to find %q in output", substr)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %s", path)
	}
}
