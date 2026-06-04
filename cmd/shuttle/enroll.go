package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/mtls"
	"github.com/spf13/cobra"
)

type enrollHost struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

// enrollResult mirrors the orchestrator's /enroll response: a single-use,
// short-lived join token bound to the chosen host.
type enrollResult struct {
	ID           string `json:"id"`
	Host         string `json:"host"`
	JoinToken    string `json:"join_token"`
	ExpiresAtUMS int64  `json:"expires_at_unix_ms"`
}

var enrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Enroll a new agent: pick a host and print its one-line join command",
	Long: `Talks to a running orchestrator's control plane. Lists the hosts declared
in the IaC repo, lets you pick one (or pass --host), mints a short-lived,
single-use join token bound to that host, and prints a ready-to-run
'shuttle agent join' command.

The powerful control-plane bearer token never leaves this machine — only the
scoped, expiring join token is carried to the target host. When the control
plane is reached over HTTPS, the command embeds a --pin of the orchestrator's
certificate (trust-on-first-use) so the host needs no CA file.

The URL and bearer token are resolved in this order (first non-empty wins):
explicit --url/--token flags, then --config (the orchestrator's config.yml,
reading advertise_control_url and bearer_token), then the SHUTTLE_URL and
SHUTTLE_TOKEN environment variables (which a local .env can supply). So on the
orchestrator host 'shuttle enroll --config config.yml --host web-1' needs no
secret on the command line.

Run the printed command once on the target host. It exchanges the join token
for a long-lived agent credential and starts the agent.`,
	Example: `  # On the orchestrator host: source url + token from its config
  shuttle enroll --config config.yml --host web-1

  # Explicit flags (or via $SHUTTLE_URL / $SHUTTLE_TOKEN)
  shuttle enroll --url https://orchestrator:8080 --token $SHUTTLE_TOKEN --host web-1`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		baseURL, _ := cmd.Flags().GetString("url")
		bearer, _ := cmd.Flags().GetString("token")
		configPath, _ := cmd.Flags().GetString("config")
		host, _ := cmd.Flags().GetString("host")

		var cfgURL, cfgToken string
		if configPath != "" {
			cfg, err := config.LoadOrchestratorConfig(configPath)
			if err != nil {
				return fmt.Errorf("load --config %s: %w", configPath, err)
			}
			cfgURL, cfgToken = cfg.AdvertiseControlURL, cfg.BearerToken
		}
		baseURL, bearer, err := resolveEnrollCreds(
			baseURL, bearer, cfgURL, cfgToken,
			os.Getenv("SHUTTLE_URL"), os.Getenv("SHUTTLE_TOKEN"),
		)
		if err != nil {
			return err
		}
		baseURL = strings.TrimRight(baseURL, "/")
		client := &http.Client{Timeout: 30 * time.Second}

		ctx := cmd.Context()
		if host == "" {
			hosts, err := listHosts(ctx, client, baseURL, bearer)
			if err != nil {
				return err
			}
			if len(hosts) == 0 {
				return fmt.Errorf("no hosts declared in the IaC repo")
			}
			host, err = pickHost(cmd.OutOrStdout(), hosts)
			if err != nil {
				return err
			}
		}

		res, pin, err := enrollHostReq(ctx, client, baseURL, bearer, host)
		if err != nil {
			return err
		}

		joinCommand := buildJoinCommand(baseURL, res.JoinToken, pin)
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "\nMinted join token for host %q (id %s), expires %s.\n\nRun this once on the host:\n\n  %s\n\n",
			res.Host, res.ID, time.UnixMilli(res.ExpiresAtUMS).Format(time.RFC3339), joinCommand)
		if pin == "" {
			_, _ = fmt.Fprintf(out, "WARNING: the control plane was not reached over HTTPS, so the join token\n"+
				"will travel without a pinned, encrypted channel. Prefer an https:// --url.\n")
		}
		_, _ = fmt.Fprintf(out, "The join token is single-use and expires; it is shown only once.\n")
		return nil
	},
}

func buildJoinCommand(redeemURL, joinToken, pin string) string {
	parts := []string{
		"shuttle agent join",
		"--redeem-url " + redeemURL,
		"--token " + joinToken,
	}
	if pin != "" {
		parts = append(parts, "--pin "+pin)
	}
	return strings.Join(parts, " ")
}

func listHosts(ctx context.Context, client *http.Client, baseURL, bearer string) ([]enrollHost, error) {
	body, _, err := doJSON(ctx, client, http.MethodGet, baseURL+"/hosts", bearer, nil)
	if err != nil {
		return nil, err
	}
	var hosts []enrollHost
	if err := json.Unmarshal(body, &hosts); err != nil {
		return nil, fmt.Errorf("decode hosts: %w", err)
	}
	return hosts, nil
}

func pickHost(out io.Writer, hosts []enrollHost) (string, error) {
	_, _ = fmt.Fprintln(out, "Available hosts:")
	for i, h := range hosts {
		label := ""
		if len(h.Labels) > 0 {
			pairs := make([]string, 0, len(h.Labels))
			for k, v := range h.Labels {
				pairs = append(pairs, k+"="+v)
			}
			label = "  (" + strings.Join(pairs, ", ") + ")"
		}
		_, _ = fmt.Fprintf(out, "  %d) %s%s\n", i+1, h.Name, label)
	}
	_, _ = fmt.Fprint(out, "Select a host [1]: ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return hosts[0].Name, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(hosts) {
		return "", fmt.Errorf("invalid selection %q", line)
	}
	return hosts[n-1].Name, nil
}

// enrollHostReq mints a join token and, when the control plane was reached over
// TLS, returns the orchestrator certificate's pin (computed from the live peer
// cert over this already-authenticated channel — trust-on-first-use).
func enrollHostReq(ctx context.Context, client *http.Client, baseURL, bearer, host string) (*enrollResult, string, error) {
	reqBody, _ := json.Marshal(map[string]string{"host": host})
	body, pin, err := doJSON(ctx, client, http.MethodPost, baseURL+"/enroll", bearer, reqBody)
	if err != nil {
		return nil, "", err
	}
	var res enrollResult
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, "", fmt.Errorf("decode enroll response: %w", err)
	}
	return &res, pin, nil
}

// doJSON performs the request and returns the body plus, when the response came
// over TLS, the server certificate pin (so the caller can embed it for TOFU).
func doJSON(ctx context.Context, client *http.Client, method, url, bearer string, body []byte) ([]byte, string, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, strings.TrimSpace(string(data)))
	}
	pin := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		pin = mtls.SPKIPin(resp.TLS.PeerCertificates[0])
	}
	return data, pin, nil
}

// resolveEnrollCreds merges the URL and bearer token from, in precedence order,
// explicit flags, the orchestrator config file (advertise_control_url +
// bearer_token), and the SHUTTLE_URL / SHUTTLE_TOKEN environment variables. It
// errors if either is still unset after all sources, naming every way to supply
// it.
func resolveEnrollCreds(flagURL, flagToken, cfgURL, cfgToken, envURL, envToken string) (url, token string, err error) {
	url = firstNonEmpty(flagURL, cfgURL, envURL)
	token = firstNonEmpty(flagToken, cfgToken, envToken)
	if url == "" {
		return "", "", fmt.Errorf("orchestrator URL not set: pass --url, set advertise_control_url in --config, or set SHUTTLE_URL")
	}
	if token == "" {
		return "", "", fmt.Errorf("bearer token not set: pass --token, set bearer_token in --config, or set SHUTTLE_TOKEN")
	}
	return url, token, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func init() {
	enrollCmd.Flags().String("url", "", "Orchestrator control-plane URL, e.g. https://orchestrator:8080 (overrides --config / $SHUTTLE_URL)")
	enrollCmd.Flags().String("token", "", "Control-plane bearer token (overrides --config / $SHUTTLE_TOKEN)")
	enrollCmd.Flags().String("config", "", "Orchestrator config.yml to read advertise_control_url + bearer_token from")
	enrollCmd.Flags().String("host", "", "Host to enroll; skips the interactive picker")
}
