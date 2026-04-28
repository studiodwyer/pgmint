package cmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/studiodwyer/pgmint/internal/daemon"
	"github.com/studiodwyer/pgmint/internal/docker"
	"github.com/studiodwyer/pgmint/internal/postgres"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listenAddr := fs.String("listen-addr", Defaults.ListenAddr, "address to listen on")
	instanceName := fs.String("name", Defaults.Name, "instance name")
	statsInterval := fs.Duration("stats-interval", 5*time.Second, "interval for collecting PostgreSQL connection metrics")
	fs.Parse(args)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr, err := docker.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create docker manager: %w", err)
	}

	container, err := mgr.FindByName(ctx, *instanceName)
	if err != nil {
		return fmt.Errorf("failed to find container: %w", err)
	}
	if container == nil {
		return fmt.Errorf("container %q not found — run 'init' first", *instanceName)
	}

	slog.Info("found container", "container_id", container.ID[:12], "port", container.Port)

	connStr := fmt.Sprintf("postgres://postgres:%s@localhost:%d/postgres?sslmode=disable",
		container.Password, container.Port)
	pgMgr := postgres.New(connStr)

	if err := pgMgr.Ping(ctx); err != nil {
		return fmt.Errorf("cannot connect to PostgreSQL: %w", err)
	}
	slog.Info("connected to PostgreSQL")

	cfg := daemon.Config{
		PgHost:        container.PgHost,
		PgPort:        container.Port,
		Password:      container.Password,
		SourceDB:      container.SourceDB,
		StatsInterval: *statsInterval,
	}

	srv := daemon.New(pgMgr, cfg)
	srv.Start(ctx)

	httpServer := &http.Server{Addr: *listenAddr, Handler: srv.Handler()}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		httpServer.Shutdown(context.Background())
	}()

	slog.Info("starting daemon", "addr", *listenAddr)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	slog.Info("daemon stopped")
	return nil
}
