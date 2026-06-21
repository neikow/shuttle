package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neikow/shuttle/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// scaffoldCmd groups the repo-authoring helpers. They generate the same IaC YAML
// the loader consumes — the orchestrator's source of truth — so a freshly
// scaffolded file always parses. Each subcommand is a thin wrapper over a pure
// render/merge function (below) for testability, mirroring init's split.
var scaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Scaffold IaC repo files (services, hosts, DNS providers, certificates)",
	Long: `Generate the IaC YAML files Shuttle reads — service definitions, hosts,
DNS-challenge providers, and certificates — so you don't hand-write boilerplate.
Output is validated against the same loader the orchestrator uses, so a scaffolded
file is always something Shuttle accepts. Powers the VS Code extension's
scaffolding commands, and works standalone in a repo checkout.`,
}

// --- service ---

type serviceScaffold struct {
	Name     string
	Host     string
	Kind     string // "docker" | "compose" | "external"
	Domains  []string
	Port     int
	Image    string
	Upstream string
}

var scaffoldServiceCmd = &cobra.Command{
	Use:   "service <name>",
	Short: "Create services/<name>/<name>.yaml (+ docker-compose.yml unless external)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		kind, _ := cmd.Flags().GetString("kind")
		domains, _ := cmd.Flags().GetStringArray("domain")
		port, _ := cmd.Flags().GetInt("port")
		image, _ := cmd.Flags().GetString("image")
		upstream, _ := cmd.Flags().GetString("upstream")
		s := serviceScaffold{Name: args[0], Host: mustFlag(cmd, "host"), Kind: kind, Domains: domains, Port: port, Image: image, Upstream: upstream}
		paths, err := scaffoldService(repo, s)
		if err != nil {
			return err
		}
		printCreated(cmd, paths)
		return nil
	},
}

// scaffoldService writes the service file (and, unless external, a compose file)
// under <repo>/services/<name>/, returning the paths it created. It refuses to
// overwrite an existing service file.
func scaffoldService(repo string, s serviceScaffold) ([]string, error) {
	if s.Name == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if s.Host == "" {
		return nil, fmt.Errorf("--host is required")
	}
	if s.Kind == "" {
		s.Kind = "compose"
	}
	svcYAML, err := renderServiceYAML(s)
	if err != nil {
		return nil, err
	}
	if probs := config.ValidateBytes(config.FileKindService, []byte(svcYAML)); len(probs) > 0 {
		return nil, fmt.Errorf("generated service is invalid: %s", probs[0].Message)
	}

	dir := filepath.Join(repo, "services", s.Name)
	svcPath := filepath.Join(dir, s.Name+".yaml")
	if _, err := os.Stat(svcPath); err == nil {
		return nil, fmt.Errorf("%s already exists", svcPath)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(svcPath, []byte(svcYAML), 0o644); err != nil {
		return nil, err
	}
	created := []string{svcPath}

	if s.Kind != "external" {
		composePath := filepath.Join(dir, "docker-compose.yml")
		if err := os.WriteFile(composePath, []byte(renderComposeYAML(s)), 0o644); err != nil {
			return created, err
		}
		created = append(created, composePath)
	}
	return created, nil
}

// renderServiceYAML renders a clean service YAML for the given kind.
func renderServiceYAML(s serviceScaffold) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", s.Name)
	fmt.Fprintf(&b, "host: %s\n", s.Host)
	if len(s.Domains) > 0 {
		b.WriteString("domains:\n")
		for _, d := range s.Domains {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
	}
	if s.Port > 0 {
		fmt.Fprintf(&b, "port: %d\n", s.Port)
	}
	switch s.Kind {
	case "external":
		if s.Upstream == "" {
			return "", fmt.Errorf("--upstream is required for an external service")
		}
		if len(s.Domains) == 0 {
			return "", fmt.Errorf("an external service needs at least one --domain")
		}
		b.WriteString("external:\n")
		fmt.Fprintf(&b, "  upstream: %s\n", s.Upstream)
	case "docker", "compose":
		// Source is the sibling docker-compose.yml; nothing to add here.
	default:
		return "", fmt.Errorf("unknown --kind %q (want docker, compose, or external)", s.Kind)
	}
	return b.String(), nil
}

// renderComposeYAML renders a starter docker-compose.yml: a single service from
// --image for "docker", or an editable skeleton for "compose".
func renderComposeYAML(s serviceScaffold) string {
	image := s.Image
	if image == "" {
		image = "nginx:latest # TODO: set your image"
	}
	return fmt.Sprintf("services:\n  %s:\n    image: %s\n    restart: unless-stopped\n", s.Name, image)
}

// --- host ---

var scaffoldHostCmd = &cobra.Command{
	Use:   "host <name>",
	Short: "Add a host to hosts.yaml",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		labels, _ := cmd.Flags().GetStringArray("label")
		path, err := scaffoldHost(repo, args[0], labels)
		if err != nil {
			return err
		}
		printUpdated(cmd, path)
		return nil
	},
}

func scaffoldHost(repo, name string, labels []string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("host name is required")
	}
	entry := mapNode()
	addPair(entry, "name", scalarNode(name))
	if len(labels) > 0 {
		lm := mapNode()
		for _, kv := range labels {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return "", fmt.Errorf("--label %q must be key=value", kv)
			}
			addPair(lm, strings.TrimSpace(k), scalarNode(strings.TrimSpace(v)))
		}
		addPair(entry, "labels", lm)
	}
	path := filepath.Join(repo, "hosts.yaml")
	return path, mergeListEntry(path, "hosts", "name", name, entry, config.FileKindHosts)
}

// --- dns provider ---

var scaffoldDNSProviderCmd = &cobra.Command{
	Use:   "dns-provider <name>",
	Short: "Add a DNS-challenge provider to dns.yml",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		endpoint, _ := cmd.Flags().GetString("endpoint")
		path, err := scaffoldDNSProvider(repo, args[0], mustFlag(cmd, "type"), endpoint)
		if err != nil {
			return err
		}
		printUpdated(cmd, path)
		return nil
	},
}

func scaffoldDNSProvider(repo, name, typ, endpoint string) (string, error) {
	if name == "" || typ == "" {
		return "", fmt.Errorf("provider name and --type are required")
	}
	entry := mapNode()
	addPair(entry, "name", scalarNode(name))
	addPair(entry, "type", scalarNode(typ))
	if endpoint != "" {
		addPair(entry, "endpoint", scalarNode(endpoint))
	}
	// Prefill the credential keys the type requires, each pointing at a secrets
	// provider key the operator fills in (a sensible upper-case guess).
	creds := mapNode()
	for _, key := range config.DNSProviderCredentialKeys(typ) {
		ref := mapNode()
		guess := strings.ToUpper(name + "_" + key)
		val := scalarNode(guess)
		val.LineComment = "key in your secrets provider"
		addPair(ref, "infisical_key", val)
		addPair(creds, key, ref)
	}
	addPair(entry, "credentials", creds)

	path := filepath.Join(repo, "dns.yml")
	return path, mergeListEntry(path, "providers", "name", name, entry, config.FileKindDNS)
}

// --- certificate ---

var scaffoldCertificateCmd = &cobra.Command{
	Use:   "certificate <name>",
	Short: "Add a certificate to dns.yml",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, _ := cmd.Flags().GetString("repo")
		domains, _ := cmd.Flags().GetStringArray("domain")
		path, err := scaffoldCertificate(repo, args[0], mustFlag(cmd, "provider"), domains)
		if err != nil {
			return err
		}
		printUpdated(cmd, path)
		return nil
	},
}

func scaffoldCertificate(repo, name, provider string, domains []string) (string, error) {
	if name == "" || provider == "" {
		return "", fmt.Errorf("certificate name and --provider are required")
	}
	if len(domains) == 0 {
		return "", fmt.Errorf("at least one --domain is required")
	}
	entry := mapNode()
	addPair(entry, "name", scalarNode(name))
	ds := seqNode()
	for _, d := range domains {
		ds.Content = append(ds.Content, scalarNode(d))
	}
	addPair(entry, "domains", ds)
	addPair(entry, "provider", scalarNode(provider))

	path := filepath.Join(repo, "dns.yml")
	return path, mergeListEntry(path, "certificates", "name", name, entry, config.FileKindDNS)
}

// --- shared file merge ---

// mergeListEntry appends entry to the top-level sequence topKey in the YAML file
// at path (creating the file/key as needed), preserving the rest of the file and
// its comments via a yaml.Node round-trip. It refuses to add a duplicate (an
// existing item whose idField equals idValue) and validates the result against
// kind before writing.
func mergeListEntry(path, topKey, idField, idValue string, entry *yaml.Node, kind config.FileKind) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var doc yaml.Node
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapNode()}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping", path)
	}

	seq := childValue(root, topKey)
	if seq == nil {
		seq = seqNode()
		addPair(root, topKey, seq)
	}
	if seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s: %q is not a list", path, topKey)
	}
	for _, item := range seq.Content {
		if item.Kind == yaml.MappingNode {
			if v := childValue(item, idField); v != nil && v.Value == idValue {
				return fmt.Errorf("%s already has %s %q in %s", path, idField, idValue, topKey)
			}
		}
	}
	seq.Content = append(seq.Content, entry)

	out, err := marshalNode(&doc)
	if err != nil {
		return err
	}
	if probs := config.ValidateBytes(kind, out); len(probs) > 0 {
		return fmt.Errorf("generated %s is invalid: %s", filepath.Base(path), probs[0].Message)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// --- yaml.Node helpers ---

func scalarNode(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
func mapNode() *yaml.Node           { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
func seqNode() *yaml.Node           { return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"} }

func addPair(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, scalarNode(key), val)
}

// childValue returns the value node for key in a mapping node, or nil.
func childValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// marshalNode renders a node with the repo's 2-space indentation.
func marshalNode(n *yaml.Node) ([]byte, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// --- shared cmd helpers ---

func mustFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func printCreated(cmd *cobra.Command, paths []string) {
	for _, p := range paths {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "created", p)
	}
}

func printUpdated(cmd *cobra.Command, path string) {
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "updated", path)
}

func init() {
	scaffoldServiceCmd.Flags().String("repo", ".", "Path to the IaC repo root")
	scaffoldServiceCmd.Flags().String("host", "", "Host the service runs on (required)")
	scaffoldServiceCmd.Flags().String("kind", "compose", "Service kind: docker, compose, or external")
	scaffoldServiceCmd.Flags().StringArray("domain", nil, "Domain to route to the service (repeatable)")
	scaffoldServiceCmd.Flags().Int("port", 0, "Traffic port Caddy dials for the service")
	scaffoldServiceCmd.Flags().String("image", "", "Container image (kind=docker)")
	scaffoldServiceCmd.Flags().String("upstream", "", "Upstream address (kind=external)")
	_ = scaffoldServiceCmd.MarkFlagRequired("host")

	scaffoldHostCmd.Flags().String("repo", ".", "Path to the IaC repo root")
	scaffoldHostCmd.Flags().StringArray("label", nil, "Host label key=value (repeatable)")

	scaffoldDNSProviderCmd.Flags().String("repo", ".", "Path to the IaC repo root")
	scaffoldDNSProviderCmd.Flags().String("type", "", "Provider type, e.g. ovh (required)")
	scaffoldDNSProviderCmd.Flags().String("endpoint", "", "Provider endpoint, e.g. ovh-eu")
	_ = scaffoldDNSProviderCmd.MarkFlagRequired("type")

	scaffoldCertificateCmd.Flags().String("repo", ".", "Path to the IaC repo root")
	scaffoldCertificateCmd.Flags().String("provider", "", "DNS provider name (required)")
	scaffoldCertificateCmd.Flags().StringArray("domain", nil, "Certificate subject domain (repeatable, required)")
	_ = scaffoldCertificateCmd.MarkFlagRequired("provider")

	scaffoldCmd.AddCommand(scaffoldServiceCmd, scaffoldHostCmd, scaffoldDNSProviderCmd, scaffoldCertificateCmd)
}
