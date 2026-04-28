package cmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/studiodwyer/pgmint/internal/docker"
	"github.com/studiodwyer/pgmint/internal/postgres"
)

type pgParamValue map[string]string

func (p pgParamValue) String() string {
	parts := make([]string, 0, len(p))
	for k, v := range p {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (p pgParamValue) Set(value string) error {
	k, v, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("invalid postgres parameter %q: expected key=value format", value)
	}
	if k == "" {
		return fmt.Errorf("empty key in postgres parameter %q", value)
	}
	p[k] = v
	return nil
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	pgPort := fs.Int("pg-port", Defaults.PgPort, "host port to map PostgreSQL to (bound to 0.0.0.0)")
	postgresImage := fs.String("postgres-image", Defaults.PostgresImage, "PostgreSQL Docker image")
	sourceDB := fs.String("source-db", Defaults.SourceDB, "name of the source database")
	password := fs.String("password", "", "PostgreSQL password (env: PG_PASSWORD, default: "+Defaults.Password+")")
	pgHost := fs.String("pg-host", Defaults.PgHost, "host address used in connection strings")
	instanceName := fs.String("name", Defaults.Name, "instance name (used as Docker container name and label)")
	pgParams := make(pgParamValue)
	fs.Var(pgParams, "pg-param", "PostgreSQL config parameter in key=value format (repeatable, e.g. --pg-param max_connections=200)")
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
		PgParams: pgParams,
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
