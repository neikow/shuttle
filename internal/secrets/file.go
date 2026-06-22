package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileProvider resolves secrets from dotenv files on disk, mirroring the folder
// layout Infisical exposes: a Scope{Env, Path} maps to the file
//
//	<root>/<env>/<path>.env
//
// e.g. root=/etc/shuttle/secrets, env=prod, path=/services/web resolves to
// /etc/shuttle/secrets/prod/services/web.env. Each file is plain KEY=VALUE
// dotenv. A missing file resolves to an empty set (not an error), matching a
// folder that simply has no secrets — the orchestrator's env resolution is
// what turns a genuinely-missing referenced key into a hard error.
//
// This is the no-external-dependency provider: secrets live on the orchestrator
// host (e.g. a tmpfs mount, a Kubernetes secret projected as files, or sops
// decrypted at boot) instead of in Infisical. Values never leave disk except as
// the resolved env shipped with a deploy.
type FileProvider struct {
	root       string
	defaultEnv string
}

// NewFileProvider builds a FileProvider from the environment:
//
//	SHUTTLE_SECRETS_DIR  (required) root directory of the secret tree
//	SHUTTLE_SECRETS_ENV  (optional) default environment when a scope has none
//	                     (defaults to "production")
func NewFileProvider() (*FileProvider, error) {
	root := os.Getenv("SHUTTLE_SECRETS_DIR")
	if root == "" {
		return nil, fmt.Errorf("file secrets provider: SHUTTLE_SECRETS_DIR is required")
	}
	env := os.Getenv("SHUTTLE_SECRETS_ENV")
	if env == "" {
		env = "production"
	}
	return &FileProvider{root: root, defaultEnv: env}, nil
}

// fileFor maps a scope to the dotenv file path on disk. The scope's Path is
// cleaned as an absolute path before joining, so a stray ".." cannot escape the
// configured root.
func (p *FileProvider) fileFor(scope Scope) string {
	env := scope.Env
	if env == "" {
		env = p.defaultEnv
	}
	rel := filepath.Clean(string(filepath.Separator) + scope.Path)
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	return filepath.Join(p.root, env, rel) + ".env"
}

// GetAll reads and parses the dotenv file for scope. A missing file is an empty
// (non-nil) map with no error.
func (p *FileProvider) GetAll(_ context.Context, scope Scope) (map[string]string, error) {
	path := p.fileFor(scope)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read secrets file %s: %w", path, err)
	}
	return parseEnvFile(data), nil
}

// Get returns one key from the scope's file, or ErrNotFound when absent.
func (p *FileProvider) Get(ctx context.Context, scope Scope, key string) (string, error) {
	all, err := p.GetAll(ctx, scope)
	if err != nil {
		return "", err
	}
	v, ok := all[key]
	if !ok {
		return "", ErrNotFound{Key: key}
	}
	return v, nil
}

// parseEnvFile parses KEY=VALUE dotenv content into a map. Blank lines, `#`
// comments, and an optional `export ` prefix are handled; single- or
// double-quoted values keep their inner whitespace, and an unquoted value's
// trailing `# comment` is stripped. Malformed lines (no `=`) are skipped.
func parseEnvFile(data []byte) map[string]string {
	out := map[string]string{}
	for raw := range strings.SplitSeq(string(data), "\n") {
		s := strings.TrimSpace(raw)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		k, v, found := strings.Cut(s, "=")
		if !found {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		val := strings.TrimSpace(v)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		} else if i := strings.IndexByte(val, '#'); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		out[key] = val
	}
	return out
}
