package config

// OrchestratorConfig is loaded from the orchestrator's config.yml.
type OrchestratorConfig struct {
	DataDir         string `yaml:"data_dir"`
	GRPCAddr        string `yaml:"grpc_addr"`
	HTTPAddr        string `yaml:"http_addr"`
	BearerToken     string `yaml:"bearer_token"`
	RepoURL         string `yaml:"repo_url"`
	RepoBranch      string `yaml:"repo_branch"`
	RepoDir         string `yaml:"repo_dir"`
	WebhookSecret   string `yaml:"webhook_secret"`
	CaddyAdminURL   string `yaml:"caddy_admin_url"`  // e.g. http://caddy:2019; empty disables route push
	HTTPSRedirect   bool   `yaml:"https_redirect"`   // when true, Caddy serves :443 only and 308-redirects :80 -> HTTPS
	SecretsProvider string `yaml:"secrets_provider"` // "infisical" | "file" | "none" (default)
	// SecretsBasePath is the shared secrets folder merged under every service
	// (default "/shared"). SecretsPathTemplate derives a service's own folder
	// from its name (default "/services/{service}"); a service's secret_path
	// overrides it. Both must be absolute paths.
	SecretsBasePath     string `yaml:"secrets_base_path"`
	SecretsPathTemplate string `yaml:"secrets_path_template"`
	// InfisicalWebhookSecret enables POST /webhook/infisical: when set, the
	// orchestrator authenticates Infisical secret-change webhooks (HMAC) and
	// redeploys the affected services. Empty disables the endpoint.
	InfisicalWebhookSecret string `yaml:"infisical_webhook_secret"`
	// InfisicalWebhookDebounce is the quiet window over which a burst of
	// Infisical changes is coalesced into one redeploy pass (e.g. "5s").
	// Empty defaults to 5s.
	InfisicalWebhookDebounce string `yaml:"infisical_webhook_debounce"`
	// InfisicalPollInterval enables periodic polling of the Infisical folders the
	// repo's services read (e.g. "60s"): when a folder's secret set changes, the
	// affected services are redeployed. A fallback for when webhooks are not
	// delivered. Requires a secrets provider. Empty disables polling. Only secret
	// fingerprints (SHA-256) are kept — never the values.
	InfisicalPollInterval string `yaml:"infisical_poll_interval"`
	// DNSReconcileInterval tunes how often the DNS record reconciler runs (e.g.
	// "2m"). Empty uses the default (2m). Manages A/AAAA/CNAME records and pushes
	// sidecar zones for dns.yml zones; a no-op when no zones are declared.
	DNSReconcileInterval string `yaml:"dns_reconcile_interval"`
	// gRPC TLS. cert+key => the orchestrator serves TLS; adding ca makes it
	// require+verify client certs (mutual TLS).
	GRPCTLSCert string `yaml:"grpc_tls_cert"`
	GRPCTLSKey  string `yaml:"grpc_tls_key"`
	GRPCTLSCA   string `yaml:"grpc_tls_ca"`
	// Agent enrollment tokens. When true, agents must present a valid bearer
	// token (see `shuttle enroll`) to register.
	AgentTokenAuth bool `yaml:"agent_token_auth"`
	// AdvertiseAddr is the gRPC host:port agents should dial, embedded in the
	// enrollment command. Falls back to GRPCAddr when empty.
	AdvertiseAddr string `yaml:"advertise_addr"`
	// AdvertiseServerName is the SAN agents expect on the orchestrator cert,
	// embedded in the enrollment command when TLS is on.
	AdvertiseServerName string `yaml:"advertise_server_name"`
	// AdvertiseControlURL is the publicly reachable control-plane URL (e.g.
	// https://orchestrator.example.com:8080). `shuttle enroll --config` reads it
	// to fill --url: the value is both the endpoint enroll calls and the
	// redeem-url baked into the join command, so it must be the externally
	// reachable URL, not http_addr (which is just the local listen address).
	AdvertiseControlURL string `yaml:"advertise_control_url"`
	// GitCredentials lists per-host or per-org HTTPS token credentials used to
	// authenticate git clone/fetch operations against private repos. Tokens are
	// resolved from the secrets provider at runtime.
	GitCredentials []GitCredential `yaml:"git_credentials"`
	// Notifications lists outbound notification targets. Each subscribes to the
	// orchestrator event bus and POSTs matching events to a webhook (Slack,
	// Discord, or a generic JSON endpoint). Lives in config.yml — not the
	// repo-managed orchestrator.yaml — because a Slack/Discord webhook URL is a
	// secret that must not be committed to the IaC repo.
	Notifications []NotificationTarget `yaml:"notifications"`
	// WebhookRateLimitPerMinute caps requests per client IP to the
	// unauthenticated webhook endpoints (/webhook, /webhook/infisical,
	// /webhook/repo/{id}). 0/unset keeps the built-in default (120/min); a
	// negative value disables rate limiting.
	WebhookRateLimitPerMinute int `yaml:"webhook_rate_limit_per_minute"`
	// MetricsRequireAuth gates GET /metrics behind the read role. Default false
	// keeps the standard unauthenticated Prometheus scrape model; set true when
	// /metrics is reachable from an untrusted network.
	MetricsRequireAuth bool `yaml:"metrics_require_auth"`
	// OIDC optionally enables per-user OpenID Connect bearer-token auth on the
	// HTTP control plane, layered on top of the static bearer + named control
	// tokens. Empty issuer disables it.
	OIDC OIDCConfig `yaml:"oidc"`
	// Backups configures service data backups: the backend credentials (resolved
	// from the secrets provider and injected into the backup process env, never
	// persisted), the default store/target services inherit, and the scheduler
	// tick. Lives in config.yml — not the repo-managed orchestrator.yaml — because
	// a restic password / S3 key is a secret reference that must not be committed
	// to the IaC repo. Empty leaves backups disabled.
	Backups BackupConfig `yaml:"backups"`
}

// BackupConfig is the orchestrator's bootstrap backup configuration. Per-service
// backup *policy* (engine, schedule, retention) lives in the IaC repo; this is
// the host-side wiring that policy can't carry: secret-resolved backend
// credentials and the defaults services inherit.
type BackupConfig struct {
	// Env lists secret keys resolved from the provider and injected into every
	// backup/restore process env (e.g. RESTIC_PASSWORD, AWS_ACCESS_KEY_ID). The
	// values are fetched fresh per operation and never written to disk — mirrors
	// the git_credentials model.
	Env []BackupCredential `yaml:"env"`
	// DefaultStore / DefaultTarget fill in a service's backup block when it omits
	// store/target, so most services need only declare an engine + schedule.
	DefaultStore  string `yaml:"default_store"`
	DefaultTarget string `yaml:"default_target"`
	// PollInterval is how often the backup scheduler checks for due scheduled
	// backups (e.g. "5m"). Empty defaults to 5m. The per-service schedule decides
	// whether a given service is actually due.
	PollInterval string `yaml:"poll_interval"`
}

// BackupCredential references a secret to inject into the backup process env.
// Mirrors GitCredential: the value is resolved from the secrets provider at
// runtime, never stored in config.
type BackupCredential struct {
	// Key is the environment variable name set in the backup process (e.g.
	// RESTIC_PASSWORD).
	Key string `yaml:"key"`
	// InfisicalKey is the secret key holding the value.
	InfisicalKey string `yaml:"infisical_key"`
	// InfisicalEnv and InfisicalPath optionally override the lookup scope. When
	// empty, the provider's defaults are used.
	InfisicalEnv  string `yaml:"infisical_env"`
	InfisicalPath string `yaml:"infisical_path"`
}

// Enabled reports whether the backup subsystem is configured (any credential or
// default target present).
func (c BackupConfig) Enabled() bool {
	return len(c.Env) > 0 || c.DefaultTarget != "" || c.DefaultStore != ""
}

// OIDCConfig configures OpenID Connect bearer-token authentication for the HTTP
// control plane. When Issuer is set, the orchestrator verifies presented JWTs
// against the issuer's published keys (JWKS, discovered at startup) and maps a
// token claim to a control-plane role, reusing the same read<deploy<admin model
// as the static bearer and named control tokens. This adds per-user identity
// (the audit actor becomes the OIDC subject) without replacing the bootstrap
// bearer, which stays the break-glass admin.
type OIDCConfig struct {
	// Issuer is the OIDC issuer URL (e.g. https://accounts.google.com or a
	// self-hosted Dex/Keycloak). Its /.well-known/openid-configuration is fetched
	// at startup for discovery. Empty disables OIDC entirely.
	Issuer string `yaml:"issuer"`
	// Audience is the expected `aud` claim — the client ID registered with the
	// IdP for Shuttle. Tokens not issued for this audience are rejected.
	Audience string `yaml:"audience"`
	// RolesClaim is the token claim read for role mapping; its value may be a
	// single string or a list of strings (e.g. "groups"). Default "groups".
	RolesClaim string `yaml:"roles_claim"`
	// RoleMapping maps a value found in RolesClaim to a control-plane role
	// (read/deploy/admin). The highest-ranked matched role wins. A validly-signed
	// token that maps to nothing is authenticated but unauthorized (403).
	// Required when Issuer is set.
	RoleMapping map[string]string `yaml:"role_mapping"`
	// UsernameClaim is the claim used as the caller's identity (the audit actor).
	// Default "sub".
	UsernameClaim string `yaml:"username_claim"`
	// Scopes are the OAuth2 scopes the *web UI* requests during its browser login
	// (Authorization Code + PKCE). It does not affect server-side token
	// verification — only what the SPA asks the IdP for so the resulting ID token
	// carries the claims (e.g. groups) this config maps. Space-separated; default
	// "openid profile email". Advertised at GET /auth/config.
	Scopes string `yaml:"scopes"`
}

// OIDCEnabled reports whether OIDC HTTP auth is configured.
func (c *OrchestratorConfig) OIDCEnabled() bool { return c.OIDC.Issuer != "" }

// NotificationTarget is one outbound notification sink. The URL receives an
// HTTP POST for every event whose type matches Events (empty Events = all).
type NotificationTarget struct {
	// Type selects the payload format: "slack" and "discord" send a chat
	// message ({"text"} / {"content"}); "webhook" posts the raw event JSON.
	Type string `yaml:"type"`
	// URL is the incoming-webhook endpoint to POST to.
	URL string `yaml:"url"`
	// Events optionally restricts which event types are delivered (e.g.
	// ["deploy.failed", "deploy.rolled_back", "drift.detected"]). Empty means
	// every event type is delivered.
	Events []string `yaml:"events"`
}

// Notification target types.
const (
	NotifySlack   = "slack"
	NotifyDiscord = "discord"
	NotifyWebhook = "webhook"
)

// GitCredential configures HTTPS token auth for a git host or repo prefix.
// The token is resolved from the secrets provider at runtime, never stored in config.
type GitCredential struct {
	// RepoPrefix matches repo URLs that start with "https://<RepoPrefix>".
	// Examples: "github.com/myorg" (org-scoped), "github.com" (all repos on host).
	RepoPrefix string `yaml:"repo_prefix"`
	// InfisicalKey is the secret key name holding the HTTPS token.
	InfisicalKey string `yaml:"infisical_key"`
	// InfisicalEnv and InfisicalPath are optional overrides for the Infisical
	// lookup scope. When empty, the provider's default env/path are used.
	InfisicalEnv  string `yaml:"infisical_env"`
	InfisicalPath string `yaml:"infisical_path"`
}

// MTLSEnabled reports whether mutual TLS (client-cert verification) is configured.
func (c *OrchestratorConfig) MTLSEnabled() bool {
	return c.GRPCTLSCert != "" && c.GRPCTLSKey != "" && c.GRPCTLSCA != ""
}

// ServerTLSEnabled reports whether the orchestrator should serve TLS (cert+key
// present), regardless of client-cert verification.
func (c *OrchestratorConfig) ServerTLSEnabled() bool {
	return c.GRPCTLSCert != "" && c.GRPCTLSKey != ""
}

// Repo is the parsed state of a shuttle IaC repository.
type Repo struct {
	Hosts    []Host
	Services []Service
	// DNS is the optional dns.yml: DNS-challenge certificate providers and the
	// (wildcard) certificates they issue. Nil when the repo has no dns.yml.
	DNS *DNSConfig
}

type Host struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
	// Caddy optionally overrides the host's Caddy sidecar listen/publish ports.
	// Nil keeps the defaults (80 HTTP, 443 HTTPS).
	Caddy *HostCaddy `yaml:"caddy"`
	// Addresses are the host's reachable IPs by named network label, used as the
	// target for DNS records Shuttle manages (see dns.yml zones). A host may be
	// reachable at several addresses — e.g. {public: 203.0.113.20, tailscale:
	// 100.64.0.5} — and each dns.yml zone names which label its records point at
	// (default "public"). Empty when DNS record management is not used.
	Addresses map[string]string `yaml:"addresses"`
}

// DefaultAddressLabel is the host-address label a dns.yml zone targets when it
// names none.
const DefaultAddressLabel = "public"

// Address returns the host's IP for the given network label, falling back to the
// "public" address when label is empty. Returns "" when unset.
func (h Host) Address(label string) string {
	if label == "" {
		label = DefaultAddressLabel
	}
	return h.Addresses[label]
}

// HostCaddy configures the ports a host's Caddy sidecar listens on (and
// publishes) for HTTP and HTTPS traffic. Both the container's internal listen
// and the host-published port use these values, so an agent behind a load
// balancer (or sharing the box with another service on :80/:443) can relocate
// ingress. Zero/unset means the standard port (80 / 443).
type HostCaddy struct {
	HTTPPort  int `yaml:"http_port"`
	HTTPSPort int `yaml:"https_port"`
}

// Default Caddy sidecar ports when a host declares no override.
const (
	DefaultCaddyHTTPPort  = 80
	DefaultCaddyHTTPSPort = 443
)

// HTTPPortOrDefault returns the host's configured HTTP port, or the default.
func (h Host) HTTPPortOrDefault() int {
	if h.Caddy != nil && h.Caddy.HTTPPort != 0 {
		return h.Caddy.HTTPPort
	}
	return DefaultCaddyHTTPPort
}

// HTTPSPortOrDefault returns the host's configured HTTPS port, or the default.
func (h Host) HTTPSPortOrDefault() int {
	if h.Caddy != nil && h.Caddy.HTTPSPort != 0 {
		return h.Caddy.HTTPSPort
	}
	return DefaultCaddyHTTPSPort
}

type Service struct {
	Name         string `yaml:"name"`
	Host         string `yaml:"host"`
	Source       ServiceSource
	Domains      []string `yaml:"domains"`
	EnvFrom      string   `yaml:"env_from"`
	// Env declares the environment variables shipped to the service. Each value
	// is a source spec resolved at deploy time:
	//   ""                                        -> the configured secrets
	//                                                provider, keyed by the var name
	//   ${secret:KEY} / ${infisical:KEY} / ${KEY} -> the provider, keyed by KEY
	//   ${env:KEY}                                -> the orchestrator's process env
	//   any other text                            -> a literal (tokens may be embedded)
	// A service with no Env reads no secrets, so its provider folder need not exist.
	Env          map[string]string `yaml:"env"`
	Port         int               `yaml:"port"` // traffic port Caddy dials for this service's domains
	CaddySnippet string            `yaml:"caddy_snippet"`
	// TLSCertificate optionally pins this service's domains to a named
	// certificate declared in dns.yml (forcing its DNS challenge). Empty lets
	// Caddy auto-match the domain to a covering certificate, else fall back to
	// per-domain HTTP-01.
	TLSCertificate string `yaml:"tls_certificate"`
	// DeleteVolumes is the canonical volume-deletion policy when this service is
	// removed from the repo: "immediate", "manual" (default), or a duration
	// string (e.g. "7 days") after which volumes are deleted.
	DeleteVolumes string
	// SecretPath is the Infisical folder this service's secrets are read from,
	// overriding the orchestrator's secrets_path_template. Must be absolute.
	SecretPath string `yaml:"secret_path"`
	// UpdatePolicy is how the agent applies a deploy: "rolling" (default,
	// zero-downtime) or "recreate". Canonicalized at load (never empty).
	UpdatePolicy string
	// Backup is the service's repo-managed backup policy, or nil when the
	// service declares no `backup:` block.
	Backup *ServiceBackup
}

// ServiceSource is a local compose file, a remote pointer, or an external
// (Shuttle-not-managed) upstream that only gets a Caddy route.
type ServiceSource interface {
	isServiceSource()
}

type LocalCompose struct {
	Path string // absolute path to docker-compose.yml within the repo
}

func (LocalCompose) isServiceSource() {}

type RemotePointer struct {
	Repo   string `yaml:"repo"`
	Branch string `yaml:"branch"`
	Path   string `yaml:"path"`
}

func (RemotePointer) isServiceSource() {}

// ExternalService is a service Shuttle does NOT deploy or manage the lifecycle
// of — it only routes ingress to it. Upstream is the address the host's Caddy
// sidecar dials verbatim (e.g. a sibling container on the shared `shuttle`
// network, or `host.docker.internal:PORT`). Used to put HTTPS + a reverse proxy
// in front of out-of-band infrastructure (e.g. an Infisical instance running
// beside the agent).
type ExternalService struct {
	Upstream string `yaml:"upstream"`
}

func (ExternalService) isServiceSource() {}

// IsExternal reports whether the service is an external (proxy-only) service.
// Shuttle skips it in every lifecycle path (deploy/diff/drift/teardown/backup)
// and only emits a Caddy route for it.
func (s Service) IsExternal() bool {
	_, ok := s.Source.(ExternalService)
	return ok
}

// Raw YAML structs for decoding (unexported).

type hostsFile struct {
	Hosts []Host `yaml:"hosts"`
}

type serviceFile struct {
	Name           string              `yaml:"name"`
	Host           string              `yaml:"host"`
	Domains        []string            `yaml:"domains"`
	EnvFrom        string              `yaml:"env_from"`
	Env            map[string]string   `yaml:"env"`
	Port           int                 `yaml:"port"`
	CaddySnippet   string              `yaml:"caddy_snippet"`
	TLSCertificate string              `yaml:"tls_certificate"`
	DeleteVolumes  deleteVolumesPolicy `yaml:"delete_volumes"`
	SecretPath     string              `yaml:"secret_path"`
	UpdatePolicy   string              `yaml:"update_policy"`
	Backup         *serviceBackup      `yaml:"backup"`
	Remote         *RemotePointer      `yaml:"remote"`
	External       *ExternalService    `yaml:"external"`
}
