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
}

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
}

type Host struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

type Service struct {
	Name         string `yaml:"name"`
	Host         string `yaml:"host"`
	Source       ServiceSource
	Domains      []string `yaml:"domains"`
	EnvFrom      string   `yaml:"env_from"`
	EnvSchema    []string `yaml:"env_schema"`
	Port         int      `yaml:"port"` // traffic port Caddy dials for this service's domains
	CaddySnippet string   `yaml:"caddy_snippet"`
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
}

// ServiceSource is either a local compose file or a remote pointer.
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

// Raw YAML structs for decoding (unexported).

type hostsFile struct {
	Hosts []Host `yaml:"hosts"`
}

type serviceFile struct {
	Name          string              `yaml:"name"`
	Host          string              `yaml:"host"`
	Domains       []string            `yaml:"domains"`
	EnvFrom       string              `yaml:"env_from"`
	EnvSchema     []string            `yaml:"env_schema"`
	Port          int                 `yaml:"port"`
	CaddySnippet  string              `yaml:"caddy_snippet"`
	DeleteVolumes deleteVolumesPolicy `yaml:"delete_volumes"`
	SecretPath    string              `yaml:"secret_path"`
	UpdatePolicy  string              `yaml:"update_policy"`
	Remote        *RemotePointer      `yaml:"remote"`
}
