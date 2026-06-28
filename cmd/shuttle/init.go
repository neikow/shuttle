package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a new IaC repository (hosts, services, CI)",
	Long: `Scaffold a new Shuttle IaC git repository in the current directory (or --dir).

This writes only the git-managed side of a project: hosts.yaml, services/,
orchestrator.yaml, a .gitignore, and (optionally) CI workflows for your git
provider, then makes an initial commit. It does NOT generate the orchestrator's
server config — run 'shuttle orchestrator init' for config.yml, .env, and TLS
material (it can share this same directory).

The repo is provider- and remote-agnostic: create the remote yourself when ready
(e.g. an empty repo on GitHub/GitLab), push, then point the orchestrator at it
with 'shuttle orchestrator init --repo-url <url>' on the server.

By default it asks two questions (starter vs empty, and CI provider) and takes
secure defaults for the rest. Pass --advanced to be prompted for Caddy and secret
paths too.`,
	Example: `  # Scaffold a starter repo (whoami example) in the current directory
  shuttle init

  # Empty scaffold with GitLab CI, no prompts for it
  shuttle init --dir /etc/shuttle --ci gitlab`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		advanced, _ := cmd.Flags().GetBool("advanced")
		ci, _ := cmd.Flags().GetString("ci")
		opts, err := promptRepoInit(os.Stdin, os.Stdout, dir, ci, advanced)
		if err != nil {
			return err
		}
		return applyRepoInit(cmd.Context(), opts, os.Stdout)
	},
}

func init() {
	initCmd.Flags().String("dir", ".", "Directory to scaffold the IaC repo into")
	initCmd.Flags().Bool("advanced", false, "Prompt for advanced settings (Caddy, secret paths)")
	initCmd.Flags().String("ci", "", "CI workflows to generate for your git provider: none|github|gitlab (default: prompt)")
}

// RepoInitOptions holds the settings for scaffolding an IaC repo. Separating the
// prompt (I/O) from the apply (logic) keeps applyRepoInit fully testable.
type RepoInitOptions struct {
	RepoDir string

	// RepoMode controls what the repo is seeded with:
	//   "starter"  — an example whoami service + a "local" host to deploy first
	//   "empty"/"" — placeholder hosts.yaml + empty services/ (bring your own)
	RepoMode string

	// CIProvider selects which CI workflow files to generate for the git provider
	// the user will push to: "none" (default), "github", or "gitlab". The remote
	// itself is created by the user later, so only the provider is needed here.
	CIProvider string

	// Repo-managed orchestrator.yaml overrides (advanced). Empty = commented
	// examples only.
	CaddyAdminURL       string
	HTTPSRedirect       bool
	SecretsBasePath     string
	SecretsPathTemplate string
}

// promptRepoInit runs the (short by default) IaC-repo wizard. ci, when non-empty
// (from the --ci flag), pre-answers the CI-provider question.
func promptRepoInit(r io.Reader, w io.Writer, dir, ci string, advanced bool) (RepoInitOptions, error) {
	p := newPrompter(r, w, advanced)
	p.line("\nShuttle init — scaffold a new IaC repository.")
	if advanced {
		p.line("Advanced mode: every setting is prompted. Press Enter to accept a [default].")
	} else {
		p.line("Press Enter for the default. Re-run with --advanced for more options.")
	}
	p.line("")

	opts := RepoInitOptions{RepoDir: dir}

	// ── Repo contents (essential) ───────────────────────────────────────────
	p.line("=== Repository contents ===")
	p.line("  1) Starter — an example whoami service + a 'local' host to deploy first")
	p.line("  2) Empty   — placeholder hosts.yaml + empty services/ (bring your own)")
	if p.ask("Choice", "1") == "2" {
		opts.RepoMode = "empty"
	} else {
		opts.RepoMode = "starter"
	}

	// ── CI provider (essential; create the remote yourself later) ───────────
	// The remote may not exist at scaffold time, so we don't touch it — we only
	// ask which provider you'll push to so the right CI workflow is generated.
	opts.CIProvider = normalizeCIProvider(ci)
	if ci == "" {
		p.line("\n=== CI workflows ===")
		p.line("Generate deploy-on-push + plan-on-PR CI for the provider you'll push to.")
		p.line("  1) None")
		p.line("  2) GitHub Actions  (.github/workflows/)")
		p.line("  3) GitLab CI       (.gitlab-ci.yml)")
		switch p.ask("Choice", "1") {
		case "2":
			opts.CIProvider = "github"
		case "3":
			opts.CIProvider = "gitlab"
		default:
			opts.CIProvider = "none"
		}
	}

	// ── Advanced (orchestrator.yaml overrides) ──────────────────────────────
	opts.CaddyAdminURL = p.adv("Caddy admin URL for orchestrator.yaml (empty to leave commented)", "")
	if opts.CaddyAdminURL != "" {
		opts.HTTPSRedirect = p.askBool("Enable HTTPS redirect (:443 only, 308-redirect :80)?", false)
	}
	opts.SecretsBasePath = p.adv("Secrets base path override for orchestrator.yaml (empty to leave commented)", "")
	if opts.SecretsBasePath != "" {
		opts.SecretsPathTemplate = p.ask("Per-service path template", "/services/{service}")
	}

	return opts, p.err()
}

// normalizeCIProvider maps the --ci flag (and any pre-set value) to a known
// provider, defaulting unknown/empty to "none".
func normalizeCIProvider(ci string) string {
	switch strings.ToLower(strings.TrimSpace(ci)) {
	case "github", "gh":
		return "github"
	case "gitlab", "gl":
		return "gitlab"
	default:
		return "none"
	}
}

// applyRepoInit scaffolds the IaC repo and prints next steps.
func applyRepoInit(ctx context.Context, opts RepoInitOptions, w io.Writer) error {
	if err := scaffoldRepo(ctx, opts, w); err != nil {
		return fmt.Errorf("scaffold IaC repo: %w", err)
	}
	printRepoNextSteps(w, opts)
	return nil
}

// printRepoNextSteps lays out the two paths from a freshly scaffolded repo: the
// single-machine path (orchestrator drives this local repo via file://) and the
// cross-machine path (create a remote, push, init the orchestrator on the server
// from the remote URL).
func printRepoNextSteps(w io.Writer, opts RepoInitOptions) {
	cdPrefix := ""
	if opts.RepoDir != "" && opts.RepoDir != "." {
		cdPrefix = "cd " + opts.RepoDir + " && "
	}

	_, _ = fmt.Fprintln(w, "\n=== Next steps ===")

	if opts.RepoMode == "empty" {
		_, _ = fmt.Fprintf(w, "  • Declare hosts in %s and add services under %s.\n",
			joinRepo(opts.RepoDir, "hosts.yaml"), joinRepo(opts.RepoDir, "services/"))
	}

	_, _ = fmt.Fprintln(w, "\n  Single machine (orchestrator runs here, drives this repo directly):")
	if opts.RepoDir == "" || opts.RepoDir == "." {
		_, _ = fmt.Fprintln(w, "    shuttle orchestrator init")
	} else {
		_, _ = fmt.Fprintf(w, "    shuttle orchestrator init --dir %s\n", opts.RepoDir)
	}

	_, _ = fmt.Fprintln(w, "\n  Remote orchestrator (publish, then init on the server):")
	_, _ = fmt.Fprintln(w, "    1. Create an empty repo on your git provider, then push:")
	_, _ = fmt.Fprintf(w, "         %sgit remote add origin <your-remote-url>\n", cdPrefix)
	_, _ = fmt.Fprintf(w, "         %sgit push -u origin main\n", cdPrefix)
	_, _ = fmt.Fprintln(w, "    2. On the orchestrator host, clone-and-init from the remote URL:")
	_, _ = fmt.Fprintln(w, "         shuttle orchestrator init --repo-url <your-remote-url>")

	if opts.CIProvider == "github" || opts.CIProvider == "gitlab" {
		_, _ = fmt.Fprintf(w, "\n  CI (%s): set these variables in your repo settings:\n", opts.CIProvider)
		_, _ = fmt.Fprintln(w, "    • SHUTTLE_URL            — orchestrator control-plane URL")
		_, _ = fmt.Fprintln(w, "    • SHUTTLE_WEBHOOK_SECRET — matches webhook_secret in config.yml (deploy)")
		_, _ = fmt.Fprintln(w, "    • SHUTTLE_TOKEN          — control-plane bearer token (plan)")
	}
}

// scaffoldCI writes the deploy + plan CI files for the chosen provider. "none"
// (or unknown) writes nothing.
func scaffoldCI(dir, provider string) error {
	switch provider {
	case "github":
		ghDir := filepath.Join(dir, ".github", "workflows")
		if err := os.MkdirAll(ghDir, 0o755); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(ghDir, "deploy.yml"), deployWorkflowContent); err != nil {
			return err
		}
		return writeFileIfAbsent(filepath.Join(ghDir, "shuttle-plan.yml"), planWorkflowContent)
	case "gitlab":
		return writeFileIfAbsent(filepath.Join(dir, ".gitlab-ci.yml"), gitlabCIContent)
	default:
		return nil
	}
}

// scaffoldRepo initialises a git repo at opts.RepoDir and writes the standard
// IaC scaffold: hosts.yaml, services/, orchestrator.yaml, .gitignore, and (per
// opts.CIProvider) CI workflow files. Makes an initial commit. Idempotent:
// existing files are never overwritten. The remote is left to the user.
func scaffoldRepo(ctx context.Context, opts RepoInitOptions, w io.Writer) error {
	dir := opts.RepoDir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// git init (idempotent if already a repo)
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if out, err := exec.CommandContext(ctx, "git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(string(out)))
		}
		// Configure a sensible default branch name.
		_, _ = exec.CommandContext(ctx, "git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/main").CombinedOutput()
	}

	// .gitignore — protects the server-side files (config.yml/.env/certs) that
	// `shuttle orchestrator init` writes alongside the repo.
	if err := writeFileIfAbsent(filepath.Join(dir, ".gitignore"), gitignoreContent); err != nil {
		return err
	}

	// hosts.yaml — the starter declares the "local" host its agent registers as;
	// otherwise a placeholder host to edit.
	hostsContent := hostsYAMLContent
	if opts.RepoMode == "starter" {
		hostsContent = starterHostsYAMLContent
	}
	if err := writeFileIfAbsent(filepath.Join(dir, "hosts.yaml"), hostsContent); err != nil {
		return err
	}

	// services/ — starter seeds a runnable whoami service; otherwise an empty
	// directory kept tracked with a .gitkeep.
	svcDir := filepath.Join(dir, "services")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		return err
	}
	if opts.RepoMode == "starter" {
		whoamiDir := filepath.Join(svcDir, "whoami")
		if err := os.MkdirAll(whoamiDir, 0o755); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(whoamiDir, "whoami.yaml"), starterServiceYAMLContent); err != nil {
			return err
		}
		if err := writeFileIfAbsent(filepath.Join(whoamiDir, "docker-compose.yml"), starterComposeYAMLContent); err != nil {
			return err
		}
	} else if err := writeFileIfAbsent(filepath.Join(svcDir, ".gitkeep"), ""); err != nil {
		return err
	}

	// orchestrator.yaml
	orchYAML, err := renderOrchestratorYAML(opts)
	if err != nil {
		return err
	}
	if err := writeFileIfAbsent(filepath.Join(dir, "orchestrator.yaml"), orchYAML); err != nil {
		return err
	}

	// CI workflows for the chosen git provider.
	if err := scaffoldCI(dir, opts.CIProvider); err != nil {
		return err
	}

	// Initial commit if the repo has no commits yet.
	out, _ := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if strings.Contains(string(out), "fatal") || strings.TrimSpace(string(out)) == "" {
		if _, err := exec.CommandContext(ctx, "git", "-C", dir, "add", ".").CombinedOutput(); err == nil {
			_, _ = exec.CommandContext(ctx, "git", "-C", dir,
				"-c", "user.email=shuttle@localhost",
				"-c", "user.name=Shuttle",
				"commit", "-q", "-m", "chore: shuttle init scaffold").CombinedOutput()
		}
	}

	_, _ = fmt.Fprintf(w, "Scaffolded IaC repo at %s\n", dir)
	return nil
}

func renderOrchestratorYAML(opts RepoInitOptions) (string, error) {
	const tmpl = `# Orchestrator settings managed in git — no restart needed.
# Bootstrap settings (bearer_token, repo_url, webhook_secret, TLS, addresses)
# stay in config.yml on the orchestrator server.
#
# Changes here take effect on the next reconcile after pushing to the repo.

{{ if .CaddyAdminURL -}}
caddy_admin_url: "{{ .CaddyAdminURL }}"
https_redirect: {{ .HTTPSRedirect }}
{{ else -}}
# caddy_admin_url: "http://caddy:2019"
# https_redirect: false
{{ end -}}
{{ if .SecretsBasePath -}}
secrets_base_path: "{{ .SecretsBasePath }}"
secrets_path_template: "{{ .SecretsPathTemplate }}"
{{ else -}}
# secrets_base_path: "/shared"
# secrets_path_template: "/services/{service}"
{{ end }}
# Private IaC repo? Authenticate with a REPO-SCOPED token (token fetched from
# Infisical at runtime, never written to disk). Prefer a least-privilege token
# that can reach only this repo: a GitHub fine-grained PAT (Contents: read) or
# deploy key, a GitLab project access token (read_repository), or a Gitea
# repo token — not an account/org-wide PAT. Scope repo_prefix to the one repo.
# git_credentials:
#   - repo_prefix: github.com/you/iac   # single repo, no scheme
#     infisical_key: IAC_REPO_TOKEN
`
	t, err := template.New("orch").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, opts); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ── Static file content ────────────────────────────────────────────────────

// gitignoreContent keeps the bootstrap config, provider creds, generated TLS
// material, and runtime state out of git — they live alongside the IaC files in
// the project directory but must never be committed.
const gitignoreContent = `# Shuttle bootstrap config & secrets — keep on the server, never in git.
config.yml
.env

# Self-signed TLS material generated by shuttle orchestrator init.
certs/

# Orchestrator runtime state (ledger + data dir).
data/
*.db
*.db-wal
*.db-shm
`

const hostsYAMLContent = `hosts:
  - name: web1
    labels:
      region: us-east
      role: edge
`

// starterHostsYAMLContent declares the single host the starter agent registers
// as (the host you enroll, or `shuttle agent --host local`).
const starterHostsYAMLContent = `hosts:
  - name: local
`

// starterServiceYAMLContent / starterComposeYAMLContent are the runnable whoami
// example — the first thing a new install deploys. recreate (not the rolling
// default) lets the compose file publish a fixed host port so it's reachable at
// http://localhost:8088 without Caddy. Replace it with your real services.
const starterServiceYAMLContent = `name: whoami
host: local
update_policy: recreate   # lets the example publish a fixed host port
`

const starterComposeYAMLContent = `services:
  whoami:
    image: traefik/whoami:latest
    ports: ["8088:80"]
    restart: unless-stopped
`

const deployWorkflowContent = `# Drop into .github/workflows/deploy.yml in your IaC repo.
# On push to main: signs a webhook payload and POSTs it to the orchestrator.
#
# Repo settings required:
#   Variable: SHUTTLE_URL            e.g. https://orchestrator.example.com:8080
#   Secret:   SHUTTLE_WEBHOOK_SECRET same value as webhook_secret in config.yml
name: Deploy via Shuttle

on:
  push:
    branches: [main]

jobs:
  notify:
    runs-on: ubuntu-latest
    steps:
      - name: Trigger Shuttle reconcile
        env:
          SHUTTLE_URL: ${{ vars.SHUTTLE_URL }}
          SHUTTLE_WEBHOOK_SECRET: ${{ secrets.SHUTTLE_WEBHOOK_SECRET }}
        run: |
          set -euo pipefail
          BODY=$(jq -nc \
            --arg ref "$GITHUB_REF" \
            --arg sha "$GITHUB_SHA" \
            --arg repo "$GITHUB_REPOSITORY" \
            '{ref:$ref, commit_sha:$sha, repo:$repo, services:[]}')
          TS=$(date +%s)
          SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SHUTTLE_WEBHOOK_SECRET" | awk '{print $NF}')"
          curl -fsS -X POST "$SHUTTLE_URL/webhook" \
            -H "X-Hub-Signature-256: $SIG" \
            -H "X-Shuttle-Timestamp: $TS" \
            -H "Content-Type: application/json" \
            --data-binary "$BODY"
`

const planWorkflowContent = `# Drop into .github/workflows/shuttle-plan.yml in your IaC repo.
# On every PR: validates the change and posts the orchestrator diff as a comment.
#
# Repo settings required:
#   Secret:   SHUTTLE_TOKEN  control-plane bearer token
#   Variable: SHUTTLE_URL    orchestrator control-plane URL
name: Shuttle plan

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write

jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - uses: neikow/shuttle/.github/actions/plan-comment@v1
        with:
          orchestrator-url: ${{ vars.SHUTTLE_URL }}
          token: ${{ secrets.SHUTTLE_TOKEN }}
          shuttle-version: latest
`

// gitlabCIContent is the GitLab equivalent of the two GitHub workflows: deploy on
// push to the default branch (signed webhook), plan on merge requests.
const gitlabCIContent = `# Shuttle CI for GitLab — generated by shuttle init.
# Deploy on push to the default branch; plan on merge requests.
#
# CI/CD variables required (Settings > CI/CD > Variables):
#   SHUTTLE_URL             e.g. https://orchestrator.example.com:8080
#   SHUTTLE_WEBHOOK_SECRET  same value as webhook_secret in config.yml (masked)
#   SHUTTLE_TOKEN           control-plane bearer token (masked; used by plan)
stages: [plan, deploy]

shuttle-plan:
  stage: plan
  image: alpine:latest
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  before_script:
    - apk add --no-cache curl bash jq
    - curl -sSfL https://neikow.github.io/shuttle/install | bash
  script:
    - shuttle plan --url "$SHUTTLE_URL" --token "$SHUTTLE_TOKEN" --ref "$CI_COMMIT_SHA"

shuttle-deploy:
  stage: deploy
  image: alpine:latest
  rules:
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
  before_script:
    - apk add --no-cache curl jq openssl
  script:
    - |
      set -euo pipefail
      BODY=$(jq -nc \
        --arg ref "$CI_COMMIT_REF_NAME" \
        --arg sha "$CI_COMMIT_SHA" \
        --arg repo "$CI_PROJECT_PATH" \
        '{ref:$ref, commit_sha:$sha, repo:$repo, services:[]}')
      TS=$(date +%s)
      SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SHUTTLE_WEBHOOK_SECRET" | awk '{print $NF}')"
      curl -fsS -X POST "$SHUTTLE_URL/webhook" \
        -H "X-Hub-Signature-256: $SIG" \
        -H "X-Shuttle-Timestamp: $TS" \
        -H "Content-Type: application/json" \
        --data-binary "$BODY"
`
