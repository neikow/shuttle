package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv reads KEY=VALUE pairs from path and sets them in the process
// environment, without overriding variables already present (the real
// environment always wins). A missing file is not an error — it returns nil so
// callers can load CWD/.env unconditionally. Lines may use an optional `export`
// prefix, `#` comments, blank lines, and single- or double-quoted values.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		key, val, ok := parseDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if key == "" {
			return fmt.Errorf("%s:%d: malformed line (empty key)", path, line)
		}
		if _, set := os.LookupEnv(key); set {
			continue // real environment takes precedence
		}
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, line, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// parseDotEnvLine splits a single .env line into key/value. ok is false for
// blank lines and comments, which callers skip.
func parseDotEnvLine(raw string) (key, val string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	s = strings.TrimPrefix(s, "export ")

	k, v, found := strings.Cut(s, "=")
	if !found {
		return "", "", true // malformed; key stays empty so caller errors
	}
	key = strings.TrimSpace(k)
	val = strings.TrimSpace(v)

	// Quoted values keep inner whitespace; unquoted values drop trailing comments.
	if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
		val = val[1 : len(val)-1]
	} else if i := strings.IndexByte(val, '#'); i >= 0 {
		val = strings.TrimSpace(val[:i])
	}
	return key, val, true
}
