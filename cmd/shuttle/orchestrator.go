package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/infisical"
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/mtls"
	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/neikow/shuttle/internal/secrets"
	"github.com/neikow/shuttle/internal/webhook"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var orchestratorCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "Run the orchestrator: watch the IaC repo and dispatch deploys",
	Long: `Runs the central control plane. It opens a gRPC server for agents to dial
into, an HTTP control plane (deploy/rollback/webhook/enroll), watches the IaC
git repo, and reconciles desired state against the deploy ledger.

Most settings live in the config file (default ./config.yml). The --addr,
--http-addr, and --data-dir flags only fill in values the config file leaves
empty, so the config file always wins.`,
	Example: `  # Run with the default ./config.yml
  shuttle orchestrator

  # Point at an explicit config and data directory
  shuttle orchestrator --config /etc/shuttle/config.yml --data-dir /var/lib/shuttle`,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		cfg, err := config.LoadOrchestratorConfig(configPath)
		if err != nil {
			return err
		}

		// Flags fill in any value the config file leaves empty.
		if cfg.GRPCAddr == "" {
			cfg.GRPCAddr, _ = cmd.Flags().GetString("addr")
		}
		if cfg.HTTPAddr == "" {
			cfg.HTTPAddr, _ = cmd.Flags().GetString("http-addr")
		}
		if cfg.DataDir == "" {
			cfg.DataDir, _ = cmd.Flags().GetString("data-dir")
		}

		return runOrchestrator(cmd.Context(), cfg)
	},
}

func runOrchestrator(ctx context.Context, cfg *config.OrchestratorConfig) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	store, err := ledger.Open(filepath.Join(cfg.DataDir, "shuttle.db"))
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer func() { _ = store.Close() }()

	registry := orchestrator.NewRegistry()
	bus := orchestrator.NewEventBus()

	var grpcOpts []grpc.ServerOption
	switch {
	case cfg.MTLSEnabled():
		creds, err := mtls.ServerCreds(cfg.GRPCTLSCert, cfg.GRPCTLSKey, cfg.GRPCTLSCA)
		if err != nil {
			return fmt.Errorf("grpc mTLS: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		slog.Info("grpc mTLS enabled")
	case cfg.ServerTLSEnabled():
		creds, err := mtls.ServerTLSCreds(cfg.GRPCTLSCert, cfg.GRPCTLSKey)
		if err != nil {
			return fmt.Errorf("grpc server TLS: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		slog.Info("grpc server TLS enabled (agents authenticate by token)")
	default:
		slog.Warn("grpc transport is insecure; set grpc_tls_cert/key (+ agent_token_auth) or grpc_tls_ca for mTLS")
	}

	if cfg.AgentTokenAuth {
		grpcOpts = append(grpcOpts, grpc.ChainStreamInterceptor(orchestrator.TokenStreamInterceptor(store)))
		slog.Info("agent token auth enabled")
		if !cfg.ServerTLSEnabled() {
			slog.Warn("agent_token_auth without TLS sends tokens in cleartext; set grpc_tls_cert/key")
		}
	}

	tracker := orchestrator.NewStateTracker()
	agentServer := orchestrator.NewAgentServiceServer(registry, store)
	agentServer.SetStateTracker(tracker)
	agentServer.SetEventBus(bus)

	grpcServer := grpc.NewServer(grpcOpts...)
	shuttlev1.RegisterAgentServiceServer(grpcServer, agentServer)

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen grpc %s: %w", cfg.GRPCAddr, err)
	}

	httpHandler := orchestrator.NewHTTPServer(cfg.BearerToken, store, registry)
	httpHandler.SetEventBus(bus)
	metrics := orchestrator.NewMetrics(bus, registry)
	go metrics.Run(ctx, bus)
	httpHandler.EnableMetrics(metrics.Handler())
	httpHandler.SetStateTracker(tracker)
	httpHandler.EnableUI()
	if cfg.RepoURL != "" && cfg.WebhookSecret != "" {
		repoDir := cfg.RepoDir
		if repoDir == "" {
			repoDir = filepath.Join(cfg.DataDir, "repo")
		}
		secProvider, err := secrets.NewProvider(cfg.SecretsProvider)
		if err != nil {
			return fmt.Errorf("secrets provider: %w", err)
		}
		if secProvider != nil {
			slog.Info("secrets provider enabled", "provider", cfg.SecretsProvider)
		}
		syncer := orchestrator.NewGitSyncer(cfg.RepoURL, cfg.RepoBranch, repoDir, store, registry, secProvider)
		syncer.SetSecretsPaths(cfg.SecretsBasePath, cfg.SecretsPathTemplate)
		syncer.SetGitCredentials(cfg.GitCredentials)
		syncer.SetEventBus(bus)
		if cfg.CaddyAdminURL != "" {
			syncer.SetCaddy(orchestrator.NewCaddyClient(cfg.CaddyAdminURL))
			syncer.SetHTTPSRedirect(cfg.HTTPSRedirect)
			slog.Info("caddy route push enabled", "admin_url", cfg.CaddyAdminURL, "https_redirect", cfg.HTTPSRedirect)
		}
		wh := webhook.NewHandler(cfg.WebhookSecret, store)
		httpHandler.EnableWebhook(wh, syncer)
		httpHandler.EnableRepoWebhooks(syncer)
		slog.Info("webhook enabled", "repo", cfg.RepoURL, "branch", cfg.RepoBranch, "repo_dir", repoDir)

		if cfg.InfisicalWebhookSecret != "" {
			debounce := 5 * time.Second
			if cfg.InfisicalWebhookDebounce != "" {
				d, err := time.ParseDuration(cfg.InfisicalWebhookDebounce)
				if err != nil {
					return fmt.Errorf("infisical_webhook_debounce: %w", err)
				}
				debounce = d
			}
			infisicalHandler := infisical.NewHandler(cfg.InfisicalWebhookSecret)
			infisicalHandler.SetNonceStore(store)
			httpHandler.EnableInfisicalWebhook(
				infisicalHandler,
				syncer, debounce, os.Getenv("INFISICAL_ENV"),
			)
			slog.Info("infisical webhook enabled", "debounce", debounce)
		}

		if cfg.AgentTokenAuth {
			advertiseAddr := cfg.AdvertiseAddr
			if advertiseAddr == "" {
				advertiseAddr = cfg.GRPCAddr
			}
			// Hand the redeeming agent the material it needs to trust the gRPC
			// server: the CA when one is configured, otherwise the self-signed
			// server cert (which acts as its own root). This removes the need to
			// distribute a CA file to each host.
			caPEM := ""
			if cfg.ServerTLSEnabled() {
				src := cfg.GRPCTLSCA
				if src == "" {
					src = cfg.GRPCTLSCert
				}
				if b, rerr := os.ReadFile(src); rerr == nil {
					caPEM = string(b)
				} else {
					slog.Warn("could not read gRPC cert/CA for enrollment handoff; agents must supply --ca", "path", src, "err", rerr)
				}
			}
			httpHandler.EnableEnrollment(orchestrator.EnrollOptions{
				AdvertiseAddr: advertiseAddr,
				ServerName:    cfg.AdvertiseServerName,
				TLS:           cfg.ServerTLSEnabled(),
				CAPEM:         caPEM,
				Hosts:         syncer.Hosts,
			})
			slog.Info("agent enrollment endpoints enabled", "advertise_addr", advertiseAddr)
		}

		reconciler := orchestrator.NewDriftReconciler(syncer, tracker, 60*time.Second, 90*time.Second)
		reconciler.SetEventBus(bus)
		go reconciler.Run(ctx)
		slog.Info("drift reconciler started", "interval", "60s")

		if cfg.InfisicalPollInterval != "" {
			if secProvider == nil {
				return fmt.Errorf("infisical_poll_interval set but no secrets provider configured")
			}
			pollInterval, err := time.ParseDuration(cfg.InfisicalPollInterval)
			if err != nil {
				return fmt.Errorf("infisical_poll_interval: %w", err)
			}
			poller := orchestrator.NewSecretPoller(syncer, pollInterval, os.Getenv("INFISICAL_ENV"))
			go poller.Run(ctx)
			slog.Info("infisical secret polling started", "interval", pollInterval)
		}
	} else {
		slog.Info("webhook disabled; set repo_url + webhook_secret to enable git sync")
	}

	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: httpHandler,
	}

	errCh := make(chan error, 2)

	go func() {
		slog.Info("grpc listening", "addr", cfg.GRPCAddr)
		if err := grpcServer.Serve(lis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()

	go func() {
		slog.Info("http listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		stop()
		shutdown(grpcServer, httpServer)
		return err
	}

	shutdown(grpcServer, httpServer)
	return nil
}

// grpcShutdownTimeout bounds the graceful gRPC stop. Agents hold a long-lived
// bidi Register stream that never ends on its own, so an unbounded
// GracefulStop would block forever — making Ctrl+C appear dead while any agent
// is connected. After this grace period we force-close active streams.
const grpcShutdownTimeout = 5 * time.Second

func shutdown(grpcServer *grpc.Server, httpServer *http.Server) {
	if forced := stopGRPC(grpcServer, grpcShutdownTimeout); forced {
		slog.Warn("grpc graceful stop timed out; force-closing active streams", "timeout", grpcShutdownTimeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("http shutdown", "err", err)
	}
}

// stopGRPC gracefully stops the server, but force-closes (Stop) if the graceful
// stop does not finish within timeout. Returns true when it had to force.
func stopGRPC(grpcServer *grpc.Server, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return false
	case <-time.After(timeout):
		grpcServer.Stop() // unblocks the in-flight GracefulStop
		<-done
		return true
	}
}

func init() {
	orchestratorCmd.Flags().String("config", "config.yml", "Path to the orchestrator config file")
	orchestratorCmd.Flags().String("addr", ":9090", "gRPC listen address for agents (if unset in config)")
	orchestratorCmd.Flags().String("http-addr", ":8080", "HTTP control-plane listen address (if unset in config)")
	orchestratorCmd.Flags().String("data-dir", "./data", "Directory for the SQLite deploy ledger (if unset in config)")
}
