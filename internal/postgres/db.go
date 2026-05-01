package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ConnectionStats struct {
	TotalConnections int
	MaxConnections   int
	ByDatabase       map[string]int
	ByState          map[string]int
}

// DB wraps a PostgreSQL connection pool.
type DB struct {
	connString string
	pool       *pgxpool.Pool
}

// New creates a new DB with the given connection string.
func New(connString string) *DB {
	return &DB{connString: connString}
}

func (db *DB) connect(ctx context.Context) error {
	if db.pool != nil {
		return nil
	}
	config, err := pgxpool.ParseConfig(db.connString)
	if err != nil {
		return fmt.Errorf("failed to parse connection string: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}
	db.pool = pool
	return nil
}

// Ping verifies the database connection is alive.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.connect(ctx); err != nil {
		return err
	}
	return db.pool.Ping(ctx)
}

// WaitForReady retries Ping until PostgreSQL is ready or timeout is reached.
func (db *DB) WaitForReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for i := 0; time.Now().Before(deadline); i++ {
		if err := db.Ping(ctx); err == nil {
			return nil
		}
		slog.Debug("waiting for PostgreSQL", "attempt", i+1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("PostgreSQL did not become ready within %s", timeout)
}

// CreateDatabase creates a new PostgreSQL database.
func (db *DB) CreateDatabase(ctx context.Context, name string) error {
	if err := db.connect(ctx); err != nil {
		return err
	}
	_, err := db.pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", quoteIdent(name)))
	if err != nil {
		return fmt.Errorf("failed to create database %s: %w", name, err)
	}
	return nil
}

// CreateClone creates a new database copied from sourceDB using TEMPLATE.
func (db *DB) CreateClone(ctx context.Context, sourceDB, cloneName string) error {
	if err := db.connect(ctx); err != nil {
		return err
	}

	slog.Debug("terminating connections to source database", "database", sourceDB)
	_, err := db.pool.Exec(ctx,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
		sourceDB,
	)
	if err != nil {
		return fmt.Errorf("failed to terminate connections to source database: %w", err)
	}

	slog.Debug("creating clone", "clone", cloneName, "template", sourceDB)
	_, err = db.pool.Exec(ctx, fmt.Sprintf(
		"CREATE DATABASE %s WITH TEMPLATE %s",
		quoteIdent(cloneName), quoteIdent(sourceDB),
	))
	if err != nil {
		return fmt.Errorf("failed to create clone %s: %w", cloneName, err)
	}

	return nil
}

// DropClone terminates connections and drops the named database.
func (db *DB) DropClone(ctx context.Context, name string) error {
	if err := db.connect(ctx); err != nil {
		return err
	}

	slog.Debug("terminating connections to clone", "database", name)
	_, err := db.pool.Exec(ctx,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
		name,
	)
	if err != nil {
		return fmt.Errorf("failed to terminate connections: %w", err)
	}

	slog.Debug("dropping clone", "database", name)
	_, err = db.pool.Exec(ctx, fmt.Sprintf("DROP DATABASE %s", quoteIdent(name)))
	if err != nil {
		return fmt.Errorf("failed to drop database %s: %w", name, err)
	}

	return nil
}

// GetConnectionStats queries pg_stat_activity for connection metrics.
func (db *DB) GetConnectionStats(ctx context.Context) (*ConnectionStats, error) {
	if err := db.connect(ctx); err != nil {
		return nil, err
	}

	var maxConnStr string
	if err := db.pool.QueryRow(ctx, "SHOW max_connections").Scan(&maxConnStr); err != nil {
		return nil, fmt.Errorf("failed to query max_connections: %w", err)
	}
	maxConn, err := strconv.Atoi(strings.TrimSpace(maxConnStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse max_connections %q: %w", maxConnStr, err)
	}

	rows, err := db.pool.Query(ctx,
		"SELECT COALESCE(datname, '') AS datname, COALESCE(state, '') AS state, count(*) FROM pg_stat_activity GROUP BY datname, state",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query pg_stat_activity: %w", err)
	}
	defer rows.Close()

	stats := &ConnectionStats{
		MaxConnections: maxConn,
		ByDatabase:     make(map[string]int),
		ByState:        make(map[string]int),
	}

	for rows.Next() {
		var datname, state string
		var count int
		if err := rows.Scan(&datname, &state, &count); err != nil {
			return nil, fmt.Errorf("failed to scan pg_stat_activity row: %w", err)
		}
		stats.TotalConnections += count
		stats.ByState[state] += count
		if datname != "" {
			stats.ByDatabase[datname] += count
		}
	}

	return stats, rows.Err()
}

// Close releases the connection pool.
func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
