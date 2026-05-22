package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadOrchestratorConfig reads and validates the orchestrator's config.yml.
// Flag-provided defaults should be applied by the caller after loading.
func LoadOrchestratorConfig(path string) (*OrchestratorConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg OrchestratorConfig
	if err := strictDecode(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if cfg.BearerToken == "" {
		return nil, fmt.Errorf("%s: bearer_token is required", path)
	}
	return &cfg, nil
}

// Load walks a shuttle IaC repository rooted at rootDir and returns parsed Repo.
func Load(rootDir string) (*Repo, error) {
	hosts, err := loadHosts(rootDir)
	if err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}

	services, err := loadServices(rootDir)
	if err != nil {
		return nil, fmt.Errorf("services: %w", err)
	}

	repo := &Repo{Hosts: hosts, Services: services}
	if err := repo.Validate(); err != nil {
		return nil, err
	}
	return repo, nil
}

func loadHosts(rootDir string) ([]Host, error) {
	path := filepath.Join(rootDir, "hosts.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f hostsFile
	if err := strictDecode(data, &f); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return f.Hosts, nil
}

func loadServices(rootDir string) ([]Service, error) {
	servicesDir := filepath.Join(rootDir, "services")
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var services []Service
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		svc, err := loadService(rootDir, filepath.Join(servicesDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", entry.Name(), err)
		}
		services = append(services, *svc)
	}
	return services, nil
}

func loadService(rootDir, dir string) (*Service, error) {
	name := filepath.Base(dir)
	svcFile := filepath.Join(dir, name+".yaml")
	composeFile := filepath.Join(dir, "docker-compose.yml")

	hasSvcYAML := fileExists(svcFile)
	hasCompose := fileExists(composeFile)

	if !hasSvcYAML {
		return nil, fmt.Errorf("missing %s.yaml", name)
	}

	data, err := os.ReadFile(svcFile)
	if err != nil {
		return nil, err
	}
	var raw serviceFile
	if err := strictDecode(data, &raw); err != nil {
		return nil, fmt.Errorf("decode %s: %w", svcFile, err)
	}

	svc := &Service{
		Name:         raw.Name,
		Host:         raw.Host,
		Domains:      raw.Domains,
		EnvFrom:      raw.EnvFrom,
		EnvSchema:    raw.EnvSchema,
		Healthcheck:  raw.Healthcheck,
		CaddySnippet: raw.CaddySnippet,
	}

	if raw.Remote != nil && hasCompose {
		return nil, fmt.Errorf("XOR violation: %s has both a remote pointer and docker-compose.yml", name)
	}

	switch {
	case raw.Remote != nil:
		svc.Source = *raw.Remote
	case hasCompose:
		rel, err := filepath.Rel(rootDir, composeFile)
		if err != nil {
			return nil, err
		}
		svc.Source = LocalCompose{Path: rel}
	default:
		return nil, fmt.Errorf("no source: need either docker-compose.yml or remote pointer")
	}

	return svc, nil
}

// Validate checks referential integrity of the parsed repo.
func (r *Repo) Validate() error {
	hostSet := make(map[string]bool, len(r.Hosts))
	for _, h := range r.Hosts {
		if h.Name == "" {
			return fmt.Errorf("host missing name")
		}
		if hostSet[h.Name] {
			return fmt.Errorf("duplicate host name %q", h.Name)
		}
		hostSet[h.Name] = true
	}

	for _, svc := range r.Services {
		if svc.Name == "" {
			return fmt.Errorf("service missing name")
		}
		if svc.Host == "" {
			return fmt.Errorf("service %q missing host", svc.Name)
		}
		if !hostSet[svc.Host] {
			return fmt.Errorf("service %q references unknown host %q", svc.Name, svc.Host)
		}
	}
	return nil
}

func strictDecode(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
