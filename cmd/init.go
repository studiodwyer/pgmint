package cmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/studiodwyer/pgmint/internal/docker"
	"github.com/studiodwyer/pgmint/internal/postgres"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	pgPort := fs.Int("pg-port", Defaults.PgPort, "host port to map PostgreSQL to (bound to 0.0.0.0)")
	postgresImage := fs.String("postgres-image", Defaults.PostgresImage, "PostgreSQL Docker image")
	sourceDB := fs.String("source-db", Defaults.SourceDB, "name of the source database")
	password := fs.String("password", "", "PostgreSQL password (env: PG_PASSWORD, default: "+Defaults.Password+")")
	pgHost := fs.String("pg-host", Defaults.PgHost, "host address used in connection strings")
	instanceName := fs.String("name", Defaults.Name, "instance name (used as Docker container name and label)")
	fs.Parse(args)

	ctx := context.Background()

	pw := *password
	if pw == "" {
		pw = os.Getenv("PG_PASSWORD")
	}
	if pw == "" {
		pw = "postgres"
	}

	mgr, err := docker.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create docker manager: %w", err)
	}

	opts := docker.CreateOpts{
		Name:     *instanceName,
		Image:    *postgresImage,
		PgPort:   *pgPort,
		SourceDB: *sourceDB,
		Password: pw,
		PgHost:   *pgHost,
	}

	slog.Info("creating container", "name", *instanceName, "image", *postgresImage, "port", *pgPort)

	container, err := mgr.Create(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	success := false
	defer func() {
		if !success {
			slog.Warn("init failed, cleaning up container")
			_ = mgr.Remove(ctx, *instanceName)
		}
	}()

	connStr := fmt.Sprintf("postgres://postgres:%s@localhost:%d/postgres?sslmode=disable", pw, container.Port)
	pgMgr := postgres.New(connStr)

	slog.Info("waiting for PostgreSQL to be ready")
	if err := pgMgr.WaitForReady(ctx, 60*time.Second); err != nil {
		return fmt.Errorf("PostgreSQL did not become ready: %w", err)
	}

	slog.Info("PostgreSQL is ready, creating source database", "database", *sourceDB)
	if err := pgMgr.CreateDatabase(ctx, *sourceDB); err != nil {
		return fmt.Errorf("failed to create source database: %w", err)
	}

	sourceConnStr := fmt.Sprintf("postgres://postgres:%s@%s:%d/%s?sslmode=disable", pw, *pgHost, container.Port, *sourceDB)
	fmt.Println(sourceConnStr)

	success = true
	return nil
}
