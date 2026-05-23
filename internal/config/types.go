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
	SecretsProvider string `yaml:"secrets_provider"` // "infisical" | "none" (default)
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
	Remote        *RemotePointer      `yaml:"remote"`
}
