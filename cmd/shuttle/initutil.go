package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// prompter is the shared interactive front-end for the two init wizards
// (`shuttle init` and `shuttle orchestrator init`). It distinguishes essential
// questions (always asked) from advanced ones (asked only when --advanced is
// set, otherwise silently taking their default), so the default run is short.
type prompter struct {
	s        *bufio.Scanner
	w        io.Writer
	advanced bool
}

func newPrompter(r io.Reader, w io.Writer, advanced bool) *prompter {
	return &prompter{s: bufio.NewScanner(r), w: w, advanced: advanced}
}

func (p *prompter) line(format string, a ...any) {
	_, _ = fmt.Fprintf(p.w, format+"\n", a...)
}

// ask is an essential prompt: always shown.
func (p *prompter) ask(prompt, defaultVal string) string {
	if defaultVal != "" {
		_, _ = fmt.Fprintf(p.w, "%s [%s]: ", prompt, defaultVal)
	} else {
		_, _ = fmt.Fprintf(p.w, "%s: ", prompt)
	}
	if !p.s.Scan() {
		return defaultVal
	}
	if v := strings.TrimSpace(p.s.Text()); v != "" {
		return v
	}
	return defaultVal
}

func (p *prompter) askBool(prompt string, def bool) bool {
	defStr := "y/N"
	if def {
		defStr = "Y/n"
	}
	switch strings.ToLower(p.ask(prompt+" ("+defStr+")", "")) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

func (p *prompter) askSecret(prompt string) string {
	_, _ = fmt.Fprintf(p.w, "%s (leave empty to auto-generate): ", prompt)
	if !p.s.Scan() {
		return ""
	}
	return strings.TrimSpace(p.s.Text())
}

// adv is an advanced prompt: shown only with --advanced, otherwise it returns
// the default unprompted. This is how the wizards stay short by default while
// keeping every knob reachable.
func (p *prompter) adv(prompt, defaultVal string) string {
	if !p.advanced {
		return defaultVal
	}
	return p.ask(prompt, defaultVal)
}

func (p *prompter) advBool(prompt string, def bool) bool {
	if !p.advanced {
		return def
	}
	return p.askBool(prompt, def)
}

func (p *prompter) err() error { return p.s.Err() }

// generateHexToken returns a 256-bit crypto/rand value as hex, used for the
// auto-generated bearer token and webhook secret.
func generateHexToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// writeFileIfAbsent writes content unless the file already exists, so re-running
// an init wizard never clobbers a user-edited file.
func writeFileIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// resolveUnderOutput resolves a config-relative path against the project dir for
// on-disk writes. Absolute paths pass through unchanged. The value written into
// config.yml stays relative (resolved from the orchestrator's CWD at runtime),
// so the file lands under --dir while the config stays portable.
func resolveUnderOutput(outputDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(outputDir, p)
}

// joinRepo formats a repo-relative path for display in next-steps output,
// collapsing the "current directory" case so messages read "hosts.yaml" rather
// than "./hosts.yaml". A trailing slash in rel is preserved.
func joinRepo(repoDir, rel string) string {
	if repoDir == "" || repoDir == "." {
		return rel
	}
	out := filepath.Join(repoDir, rel)
	if strings.HasSuffix(rel, "/") {
		out += "/"
	}
	return out
}
