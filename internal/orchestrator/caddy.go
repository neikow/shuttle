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

// RoutesFromRepo derives the desired Caddy routes from the repo. Each service
// domain maps to an upstream of <host>:<healthcheck.port>; services without
// domains or a healthcheck port are skipped (nothing to route). A service's
// caddy_snippet, when set, must be a JSON array of Caddy HTTP handler objects;
// an invalid snippet is a hard error.
func RoutesFromRepo(repo *config.Repo) ([]CaddyRoute, error) {
	var routes []CaddyRoute
	for _, svc := range repo.Services {
		if len(svc.Domains) == 0 || svc.Healthcheck == nil || svc.Healthcheck.Port == 0 {
			continue
		}
		handlers, err := parseSnippet(svc.CaddySnippet)
		if err != nil {
			return nil, fmt.Errorf("service %q caddy_snippet: %w", svc.Name, err)
		}
		upstream := svc.Host + ":" + strconv.Itoa(svc.Healthcheck.Port)
		for _, domain := range svc.Domains {
			routes = append(routes, CaddyRoute{Domain: domain, Upstream: upstream, Handlers: handlers})
		}
	}
	return routes, nil
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

// ApplyRoutes replaces the entire Caddy config with the given routes.
// Each route: HTTPS + auto-TLS via Let's Encrypt.
func (c *CaddyClient) ApplyRoutes(ctx context.Context, routes []CaddyRoute) error {
	cfg := buildCaddyConfig(routes)
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

// buildCaddyConfig produces a minimal Caddy JSON config for the given routes.
func buildCaddyConfig(routes []CaddyRoute) map[string]any {
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

	return map[string]any{
		"admin": map[string]any{"disabled": false},
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"shuttle": map[string]any{
						"listen": []string{":443"},
						"routes": servers,
					},
				},
			},
			"tls": map[string]any{
				"automation": map[string]any{
					"policies": []any{
						map[string]any{"on_demand": true},
					},
				},
			},
		},
	}
}
