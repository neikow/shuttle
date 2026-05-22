package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
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
}

// ApplyRoutes replaces the entire Caddy config with the given routes.
// Each route: HTTPS + auto-TLS via Let's Encrypt.
func (c *CaddyClient) ApplyRoutes(routes []CaddyRoute) error {
	cfg := buildCaddyConfig(routes)
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.adminURL+"/load", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("caddy load: %w", err)
	}
	defer resp.Body.Close()
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
		servers = append(servers, map[string]any{
			"match": []any{
				map[string]any{"host": []string{r.Domain}},
			},
			"handle": []any{
				map[string]any{
					"handler": "reverse_proxy",
					"upstreams": []any{
						map[string]any{"dial": r.Upstream},
					},
				},
			},
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
