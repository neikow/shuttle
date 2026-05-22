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
	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/mtls"
	"github.com/neikow/shuttle/internal/orchestrator"
	"github.com/neikow/shuttle/internal/webhook"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var orchestratorCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "Run the Shuttle orchestrator",
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
	defer store.Close()

	registry := orchestrator.NewRegistry()

	var grpcOpts []grpc.ServerOption
	if cfg.MTLSEnabled() {
		creds, err := mtls.ServerCreds(cfg.GRPCTLSCert, cfg.GRPCTLSKey, cfg.GRPCTLSCA)
		if err != nil {
			return fmt.Errorf("grpc mTLS: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		slog.Info("grpc mTLS enabled")
	} else {
		slog.Warn("grpc transport is insecure; set grpc_tls_* for mTLS")
	}

	tracker := orchestrator.NewStateTracker()
	agentServer := orchestrator.NewAgentServiceServer(registry, store)
	agentServer.SetStateTracker(tracker)

	grpcServer := grpc.NewServer(grpcOpts...)
	shuttlev1.RegisterAgentServiceServer(grpcServer, agentServer)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen grpc %s: %w", cfg.GRPCAddr, err)
	}

	httpHandler := orchestrator.NewHTTPServer(cfg.BearerToken, store, registry)
	if cfg.RepoURL != "" && cfg.WebhookSecret != "" {
		repoDir := cfg.RepoDir
		if repoDir == "" {
			repoDir = filepath.Join(cfg.DataDir, "repo")
		}
		// Secrets provider wiring (Infisical) is deferred; nil = env passthrough off.
		syncer := orchestrator.NewGitSyncer(cfg.RepoURL, cfg.RepoBranch, repoDir, store, registry, nil)
		wh := webhook.NewHandler(cfg.WebhookSecret, store)
		httpHandler.EnableWebhook(wh, syncer)
		slog.Info("webhook enabled", "repo", cfg.RepoURL, "branch", cfg.RepoBranch, "repo_dir", repoDir)

		reconciler := orchestrator.NewDriftReconciler(syncer, tracker, 60*time.Second, 90*time.Second)
		go reconciler.Run(ctx)
		slog.Info("drift reconciler started", "interval", "60s")
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

func shutdown(grpcServer *grpc.Server, httpServer *http.Server) {
	grpcServer.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("http shutdown", "err", err)
	}
}

func init() {
	orchestratorCmd.Flags().String("config", "config.yml", "Path to config file")
	orchestratorCmd.Flags().String("addr", ":9090", "gRPC listen address")
	orchestratorCmd.Flags().String("http-addr", ":8080", "HTTP listen address")
	orchestratorCmd.Flags().String("data-dir", "./data", "Data directory for SQLite ledger")
}
