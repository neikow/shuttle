package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

// CaddyClient drives the Caddy Admin API on a local or remote Caddy instance.
type CaddyClient struct {
	adminURL string
	http     *http.Client
}

func NewCaddyClient(adminURL string) *CaddyClient {
	return &CaddyClient{
		adminURL: adminURL,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// CaddyRoute maps a domain to an upstream.
type CaddyRoute struct {
	Domain   string
	Upstream string // host:port
	// Handlers are extra Caddy HTTP handler objects parsed from the service's
	// caddy_snippet, inserted ahead of the reverse_proxy handler (e.g. headers,
	// rewrites, auth). Empty when the service has no snippet.
	Handlers []any
}

// RoutesFromRepo derives the desired Caddy routes from the repo. Each managed
// service domain maps to an upstream of <host>:<port>; an external service maps
// to its declared upstream verbatim. Services without domains (or a non-external
// service without a port) are skipped (nothing to route). A service's
// caddy_snippet, when set, must be a JSON array of Caddy HTTP handler objects;
// an invalid snippet is a hard error.
func RoutesFromRepo(repo *config.Repo) ([]CaddyRoute, error) {
	return routesFromRepo(repo, "", false)
}

// RoutesForHost derives Caddy routes for a single host's sidecar. Unlike
// RoutesFromRepo (used by the central Caddy), a managed service's upstream dials
// the service NAME, which is the network alias the agent assigns when it joins
// the service's containers to the shared Caddy network — so Caddy reaches them as
// "<service>:<port>" on that network. An external service's upstream is dialed
// verbatim (Shuttle never joins it to the network — the operator makes it
// reachable, e.g. by attaching it to the shared `shuttle` network).
func RoutesForHost(repo *config.Repo, host string) ([]CaddyRoute, error) {
	return routesFromRepo(repo, host, true)
}

// routesFromRepo is the shared route derivation. host=="" includes every host
// (central Caddy); otherwise only that host. byAlias selects the managed-service
// upstream form: the service name alias (sidecar) vs <host>:<port> (central).
func routesFromRepo(repo *config.Repo, host string, byAlias bool) ([]CaddyRoute, error) {
	var routes []CaddyRoute
	for _, svc := range repo.Services {
		if host != "" && svc.Host != host {
			continue
		}
		if len(svc.Domains) == 0 {
			continue
		}
		upstream, ok := routeUpstream(svc, byAlias)
		if !ok {
			continue
		}
		handlers, err := parseSnippet(svc.CaddySnippet)
		if err != nil {
			return nil, fmt.Errorf("service %q caddy_snippet: %w", svc.Name, err)
		}
		for _, domain := range svc.Domains {
			routes = append(routes, CaddyRoute{Domain: domain, Upstream: upstream, Handlers: handlers})
		}
	}
	return routes, nil
}

// routeUpstream returns the Caddy dial target for a service, or ok=false when it
// is not routable (a managed service with no port). External services dial their
// declared upstream verbatim; managed services dial <service>:<port> (byAlias,
// for a host sidecar) or <host>:<port> (central Caddy).
func routeUpstream(svc config.Service, byAlias bool) (string, bool) {
	if ext, isExt := svc.Source.(config.ExternalService); isExt {
		return ext.Upstream, ext.Upstream != ""
	}
	if svc.Port == 0 {
		return "", false
	}
	if byAlias {
		return svc.Name + ":" + strconv.Itoa(svc.Port), true
	}
	return svc.Host + ":" + strconv.Itoa(svc.Port), true
}

// HostCaddyConfigJSON builds the Caddy JSON config a host's sidecar should run,
// or (nil, false) when the host has no routable services. httpPort/httpsPort are
// the ports the sidecar listens on (default 80/443 when 0); tlsPolicies are the
// resolved DNS-challenge automation policies relevant to this host (nil for the
// default per-domain HTTP-01 behavior).
func HostCaddyConfigJSON(repo *config.Repo, host string, httpsRedirect bool, httpPort, httpsPort int, tlsPolicies []map[string]any) ([]byte, bool, error) {
	routes, err := RoutesForHost(repo, host)
	if err != nil {
		return nil, false, err
	}
	if len(routes) == 0 {
		return nil, false, nil
	}
	data, err := json.Marshal(buildCaddyConfig(routes, httpsRedirect, httpPort, httpsPort, tlsPolicies))
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// parseSnippet decodes a service caddy_snippet into a slice of Caddy HTTP
// handler objects. Empty snippet yields nil.
func parseSnippet(snippet string) ([]any, error) {
	if snippet == "" {
		return nil, nil
	}
	var handlers []any
	if err := json.Unmarshal([]byte(snippet), &handlers); err != nil {
		return nil, fmt.Errorf("must be a JSON array of Caddy handler objects: %w", err)
	}
	return handlers, nil
}

// ApplyRoutes replaces the entire Caddy config with the given routes. Each route
// gets HTTPS + auto-TLS: DNS-challenge issuance for domains covered by
// tlsPolicies, else per-domain Let's Encrypt (HTTP-01). The central Caddy
// (caddy_admin_url) is not host-scoped, so it keeps the standard 80/443 ports;
// per-host ports apply only to agent sidecars.
func (c *CaddyClient) ApplyRoutes(ctx context.Context, routes []CaddyRoute, httpsRedirect bool, tlsPolicies []map[string]any) error {
	cfg := buildCaddyConfig(routes, httpsRedirect, config.DefaultCaddyHTTPPort, config.DefaultCaddyHTTPSPort, tlsPolicies)
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL+"/load", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("caddy load: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy load status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// httpPort/httpsPort are the ports Caddy listens on (default 80/443 when 0).
// When httpsRedirect is true, the app server listens on the HTTPS port only, so
// Caddy's automatic HTTPS stands up its own HTTP-port server that 308-redirects
// to HTTPS (and still serves ACME HTTP-01 challenges). When false, the server
// also listens on the HTTP port and serves plaintext directly — claiming it
// suppresses the auto-redirect.
//
// tlsPolicies, when non-empty, add an `apps.tls.automation` block so the listed
// subjects (incl. wildcards) are issued via a DNS-01 challenge; domains not
// covered by any policy keep Caddy's default automation (per-domain HTTP-01).
func buildCaddyConfig(routes []CaddyRoute, httpsRedirect bool, httpPort, httpsPort int, tlsPolicies []map[string]any) map[string]any {
	if httpPort == 0 {
		httpPort = config.DefaultCaddyHTTPPort
	}
	if httpsPort == 0 {
		httpsPort = config.DefaultCaddyHTTPSPort
	}
	httpsListen := ":" + strconv.Itoa(httpsPort)
	listen := []string{":" + strconv.Itoa(httpPort), httpsListen}
	if httpsRedirect {
		listen = []string{httpsListen}
	}
	var servers []any
	for _, r := range routes {
		handle := make([]any, 0, len(r.Handlers)+1)
		handle = append(handle, r.Handlers...) // snippet handlers run before the proxy
		handle = append(handle, map[string]any{
			"handler": "reverse_proxy",
			"upstreams": []any{
				map[string]any{"dial": r.Upstream},
			},
		})
		servers = append(servers, map[string]any{
			"match": []any{
				map[string]any{"host": []string{r.Domain}},
			},
			"handle": handle,
		})
	}

	// Without a tls block, every route matches specific domains so Caddy's
	// automatic HTTPS provisions certs for those hostnames over HTTP-01 (Let's
	// Encrypt for public domains, an internal CA for *.localhost). A DNS-managed
	// certificate instead contributes an automation policy (DNS-01), which is what
	// lets a wildcard be issued. on-demand TLS is avoided (needs a permission
	// module).
	apps := map[string]any{
		"http": map[string]any{
			"servers": map[string]any{
				"shuttle": map[string]any{
					"listen": listen,
					"routes": servers,
				},
			},
		},
	}
	if len(tlsPolicies) > 0 {
		apps["tls"] = map[string]any{
			"automation": map[string]any{
				"policies": tlsPolicies,
			},
		}
	}
	return map[string]any{
		"admin": map[string]any{"disabled": false},
		"apps":  apps,
	}
}
