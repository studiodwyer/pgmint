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

