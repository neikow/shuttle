package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RepoOrchestratorConfig holds orchestrator settings managed via
// orchestrator.yaml in the IaC git repo. These settings take effect on the
// next reconcile after they are committed — no restart required. Bootstrap
// settings (bearer_token, repo_url, webhook_secret, TLS, gRPC/HTTP addresses)
// stay in config.yml on the orchestrator server.
type RepoOrchestratorConfig struct {
	// CaddyAdminURL overrides the bootstrap caddy_admin_url. Empty = no override.
	CaddyAdminURL string `yaml:"caddy_admin_url"`
	// HTTPSRedirect, when non-nil, overrides the bootstrap https_redirect flag.
	HTTPSRedirect *bool `yaml:"https_redirect"`
	// SecretsBasePath / SecretsPathTemplate override the bootstrap secrets
	// path settings. Both must be absolute paths when set.
	SecretsBasePath     string `yaml:"secrets_base_path"`
	SecretsPathTemplate string `yaml:"secrets_path_template"`
	// GitCredentials overrides the bootstrap git_credentials list.
	GitCredentials []GitCredential `yaml:"git_credentials"`
}

// LoadRepoOrchestratorConfig reads orchestrator.yaml from repoDir.
// Returns (nil, false, nil) when the file is absent — the file is optional.
// Returns (cfg, true, nil) on success, or (nil, true, err) on a parse error.
// A parse error is logged and skipped by the orchestrator (old settings kept),
// so a bad commit never blocks deploys.
func LoadRepoOrchestratorConfig(repoDir string) (*RepoOrchestratorConfig, bool, error) {
	path := filepath.Join(repoDir, "orchestrator.yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	var cfg RepoOrchestratorConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		// An empty or comment-only document (io.EOF) is valid: it declares no
		// overrides. `shuttle init` scaffolds exactly such a file, so treat it as
		// present-but-empty rather than a parse error.
		if errors.Is(err, io.EOF) {
			return &cfg, true, nil
		}
		return nil, true, fmt.Errorf("orchestrator.yaml: %w", err)
	}
	return &cfg, true, nil
}
