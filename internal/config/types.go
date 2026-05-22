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
	SecretsProvider string `yaml:"secrets_provider"` // "infisical" | "none" (default)
	// gRPC mTLS: when all three are set the orchestrator requires client certs.
	GRPCTLSCert string `yaml:"grpc_tls_cert"`
	GRPCTLSKey  string `yaml:"grpc_tls_key"`
	GRPCTLSCA   string `yaml:"grpc_tls_ca"`
}

// MTLSEnabled reports whether all gRPC TLS material is configured.
func (c *OrchestratorConfig) MTLSEnabled() bool {
	return c.GRPCTLSCert != "" && c.GRPCTLSKey != "" && c.GRPCTLSCA != ""
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
	Domains      []string     `yaml:"domains"`
	EnvFrom      string       `yaml:"env_from"`
	EnvSchema    []string     `yaml:"env_schema"`
	Healthcheck  *Healthcheck `yaml:"healthcheck"`
	CaddySnippet string       `yaml:"caddy_snippet"`
}

type Healthcheck struct {
	Path string `yaml:"path"`
	Port int    `yaml:"port"`
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
	Name         string         `yaml:"name"`
	Host         string         `yaml:"host"`
	Domains      []string       `yaml:"domains"`
	EnvFrom      string         `yaml:"env_from"`
	EnvSchema    []string       `yaml:"env_schema"`
	Healthcheck  *Healthcheck   `yaml:"healthcheck"`
	CaddySnippet string         `yaml:"caddy_snippet"`
	Remote       *RemotePointer `yaml:"remote"`
}
