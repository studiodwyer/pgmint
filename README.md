# pgmint

`pgmint` manages a PostgreSQL container and lets you create lightweight database clones using `CREATE DATABASE ... WITH TEMPLATE`. 

```bash
Usage: pgmint <command> [flags]

Commands:
  init        Start postgres container and create source database
  serve       Start the HTTP daemon for clone management
  connection  Print source database connection string
  clone       Request a clone from the daemon
  list        List active clones
  destroy     Destroy a clone
  teardown    Stop and remove the container
  version     Show version

Global flags:
  --debug     Enable debug logging
  --name      Instance name (default "pgmint")

Use "pgmint <command> --help" for command-specific flags.
```

## Usage example

```bash
# 1. Start a postgres container and create the empty source database
pgmint init --pg-port 5432

# 1a. With custom PostgreSQL parameters
pgmint init --pg-port 5432 \
  --pg-param max_connections=200 \
  --pg-param shared_buffers=256MB

# 2. Start the clone daemon
pgmint serve --listen-addr localhost:9876

# 3. Clone from source, migrate and seed
curl -s -X POST "http://localhost:9876/clone?name=pr_123&format=env" > .env.db
source .env.db
# run migrations against $DATABASE_HOST $DATABASE_USER ...

# 4. Fork test databases from the migrated clone
curl -s -X POST "http://localhost:9876/clone/pr_123?name=test_1&format=env" > .env.test1
curl -s -X POST "http://localhost:9876/clone/pr_123?name=test_2&format=env" > .env.test2

# 5. Clean up — destroy the PR clone and all its test forks
curl -X DELETE "http://localhost:9876/clone/pr_123?remove-orphans=true"

# 6. Tear down the container
pgmint teardown
```

## Prometheus Metrics

The daemon exposes metrics at `/metrics`. All metrics are prefixed with `pgmint_`.

### Clone Metrics

| Metric | Type | Description |
|---|---|---|
| `pgmint_clones_created_total` | Counter | Total clones created |
| `pgmint_clones_destroyed_total` | Counter | Total clones destroyed |
| `pgmint_clones_active` | Gauge | Current active clone count |
| `pgmint_clones_failed_total` | CounterVec (`operation`) | Failed operations |
| `pgmint_clone_create_duration_seconds` | Histogram | Clone creation latency |
| `pgmint_clone_age_seconds` | Histogram | Clone age at destruction |

### PostgreSQL Connection Metrics

Collected by a background goroutine (interval configurable via `--stats-interval`, default 5s).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `pgmint_postgres_connections_total` | Gauge | — | Total active connections |
| `pgmint_postgres_max_connections` | Gauge | — | Configured `max_connections` |
| `pgmint_postgres_connections_by_state` | GaugeVec | `state` | Connections by state (`active`, `idle`, etc.) |
| `pgmint_postgres_connections_by_database` | GaugeVec | `database` | Connections per database |

### Useful PromQL Queries

```promql
# Total active connections
sum(pgmint_postgres_connections_by_database)

# Connections for a specific database
pgmint_postgres_connections_by_database{database="sourcedb"}

# Active (non-idle) connections
pgmint_postgres_connections_by_state{state="active"}

# Connection utilization as a percentage
sum(pgmint_postgres_connections_by_database) / pgmint_postgres_max_connections * 100

# Connections per database over time
sum by (database) (pgmint_postgres_connections_by_database)

# Clone creation rate (per minute)
rate(pgmint_clones_created_total[5m]) * 60

# P99 clone creation latency
histogram_quantile(0.99, rate(pgmint_clone_create_duration_seconds_bucket[5m]))

# Average clone age at destruction
rate(pgmint_clone_age_seconds_sum[5m]) / rate(pgmint_clone_age_seconds_count[5m])
```

