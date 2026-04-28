#!/usr/bin/env bash
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly BINARY="${SCRIPT_DIR}/../dist/pgmint"

INSTANCE_NAME="e2e-test-$$"
PG_PORT=""
DAEMON_PORT=""
DAEMON_PID=""

cleanup() {
  echo ""
  echo "--- cleanup ---"
  if [[ -n "${DAEMON_PID}" ]]; then
    kill "${DAEMON_PID}" 2>/dev/null || true
    wait "${DAEMON_PID}" 2>/dev/null || true
  fi
  if [[ -n "${INSTANCE_NAME}" ]]; then
    "${BINARY}" teardown --name "${INSTANCE_NAME}" 2>/dev/null || true
  fi
  echo "cleanup done"
}
trap cleanup EXIT

get_free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()'
}

extract_json_field() {
  local json="$1"
  local field="$2"
  python3 -c "import sys,json; print(json.load(sys.stdin)['${field}'])" <<< "${json}"
}

assert_status() {
  local label="$1"
  local expected="$2"
  local actual="$3"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "FAIL: ${label} expected ${expected}, got ${actual}"
    exit 1
  fi
  echo "PASS: ${label}"
}

echo "=== pgmint e2e test ==="

echo ""
echo "--- building ---"
make build
echo "built: ${BINARY}"

echo ""
echo "--- version ---"
readonly VERSION_OUTPUT=$("${BINARY}" version)
echo "version: ${VERSION_OUTPUT}"

echo ""
echo "--- finding free ports ---"
PG_PORT=$(get_free_port)
DAEMON_PORT=$(get_free_port)
echo "PG_PORT=${PG_PORT} DAEMON_PORT=${DAEMON_PORT}"

echo ""
echo "--- init ---"
CONN_STR=$("${BINARY}" init --debug --name "${INSTANCE_NAME}" \
  --pg-port "${PG_PORT}" \
  --source-db "sourcedb" \
  --password "testpass" \
  --pg-host "localhost" \
  --pg-param max_connections=200)
echo "connection string: ${CONN_STR}"

echo ""
echo "--- verify source DB is reachable ---"
PG_READY=false
for i in $(seq 1 10); do
  if pg_isready -h localhost -p "${PG_PORT}" -U postgres -q 2>/dev/null; then
    echo "PostgreSQL is ready"
    PG_READY=true
    break
  fi
  echo "attempt ${i}: not ready yet..."
  sleep 1
done
if [[ "${PG_READY}" != "true" ]]; then
  echo "FAIL: PostgreSQL did not become ready"
  exit 1
fi

echo ""
echo "--- creating a test table in source DB ---"
if ! command -v psql &>/dev/null; then
  echo "FAIL: psql is required for e2e tests"
  exit 1
fi
PGPASSWORD=testpass psql -h localhost -p "${PG_PORT}" -U postgres -d sourcedb \
  -c "CREATE TABLE e2e_test (id serial PRIMARY KEY, value text); INSERT INTO e2e_test (value) VALUES ('hello');"

echo ""
echo "--- starting daemon ---"
"${BINARY}" serve --debug --name "${INSTANCE_NAME}" --listen-addr "127.0.0.1:${DAEMON_PORT}" --stats-interval 1s &
DAEMON_PID=$!
sleep 2

echo ""
echo "--- health check ---"
HEALTH=$(curl -sf "http://127.0.0.1:${DAEMON_PORT}/")
echo "health: ${HEALTH}"

echo ""
echo "--- get source connection string ---"
CONN_RESP=$(curl -sf "http://127.0.0.1:${DAEMON_PORT}/connection")
echo "connection response: ${CONN_RESP}"

echo ""
echo "--- create clone #1 (auto-named from source) ---"
CLONE1=$(curl -sf -X POST "http://127.0.0.1:${DAEMON_PORT}/clone")
echo "clone #1: ${CLONE1}"
CLONE1_NAME=$(extract_json_field "${CLONE1}" "clone_name")
CLONE1_CONN=$(extract_json_field "${CLONE1}" "connection_string")
echo "clone #1 name: ${CLONE1_NAME}"
echo "clone #1 conn: ${CLONE1_CONN}"

echo ""
echo "--- create named clone from source ---"
NAMED=$(curl -sf -X POST "http://127.0.0.1:${DAEMON_PORT}/clone?name=pr_42")
NAMED_NAME=$(extract_json_field "${NAMED}" "clone_name")
echo "named clone: ${NAMED_NAME}"
assert_status "named clone" "pr_42" "${NAMED_NAME}"

echo ""
echo "--- create clone from clone with custom name ---"
FORK=$(curl -sf -X POST "http://127.0.0.1:${DAEMON_PORT}/clone/pr_42?name=test_1")
FORK_NAME=$(extract_json_field "${FORK}" "clone_name")
echo "forked clone: ${FORK_NAME}"
assert_status "fork from pr_42" "test_1" "${FORK_NAME}"

echo ""
echo "--- reject reserved clone_ name ---"
RESERVED_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:${DAEMON_PORT}/clone?name=clone_test")
assert_status "reserved name rejected" "400" "${RESERVED_STATUS}"

echo ""
echo "--- reject duplicate name ---"
DUP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:${DAEMON_PORT}/clone?name=pr_42")
assert_status "duplicate name rejected" "400" "${DUP_STATUS}"

echo ""
echo "--- test env format ---"
ENV_OUTPUT=$(curl -sf -X POST "http://127.0.0.1:${DAEMON_PORT}/clone?name=env_test&format=env")
echo "${ENV_OUTPUT}"
if ! echo "${ENV_OUTPUT}" | grep -q "DATABASE_HOST="; then
  echo "FAIL: env output missing DATABASE_HOST"
  exit 1
fi
if ! echo "${ENV_OUTPUT}" | grep -q "DATABASE_NAME=env_test"; then
  echo "FAIL: env output missing DATABASE_NAME=env_test"
  exit 1
fi
echo "PASS: env format works"

echo ""
echo "--- list clones ---"
CLONES=$(curl -sf "http://127.0.0.1:${DAEMON_PORT}/clone")
echo "clones: ${CLONES}"
CLONE_COUNT=$(python3 -c "import sys,json; print(len(json.load(sys.stdin)['clones']))" <<< "${CLONES}")
if (( CLONE_COUNT != 4 )); then
  echo "FAIL: expected 4 clones, got ${CLONE_COUNT}"
  exit 1
fi
echo "PASS: 4 clones listed"

echo ""
echo "--- verify clone data ---"
PGPASSWORD=testpass psql -h localhost -p "${PG_PORT}" -U postgres -d "${CLONE1_NAME}" \
  -c "SELECT * FROM e2e_test;" 2>/dev/null
if [[ $? -ne 0 ]]; then
  echo "FAIL: could not query clone data"
  exit 1
fi
echo "PASS: clone has source data"

echo ""
echo "--- destroy with remove-orphans ---"
DESTROY_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "http://127.0.0.1:${DAEMON_PORT}/clone/pr_42?remove-orphans=true")
assert_status "destroy with orphans" "204" "${DESTROY_STATUS}"

echo ""
echo "--- verify orphans removed ---"
CLONES_AFTER=$(curl -sf "http://127.0.0.1:${DAEMON_PORT}/clone")
CLONE_COUNT_AFTER=$(python3 -c "import sys,json; print(len(json.load(sys.stdin)['clones']))" <<< "${CLONES_AFTER}")
if (( CLONE_COUNT_AFTER != 2 )); then
  echo "FAIL: expected 2 clones after remove-orphans, got ${CLONE_COUNT_AFTER}"
  exit 1
fi
echo "PASS: 2 clones remaining (pr_42 and test_1 were removed)"

echo ""
echo "--- destroy clone #1 (no orphans) ---"
DESTROY_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "http://127.0.0.1:${DAEMON_PORT}/clone/${CLONE1_NAME}")
assert_status "destroy clone #1" "204" "${DESTROY_STATUS}"

echo ""
echo "--- check metrics ---"
METRICS=$(curl -sf "http://127.0.0.1:${DAEMON_PORT}/metrics")
echo "${METRICS}" | grep "pgmint_clones_created_total 5" >/dev/null && echo "PASS: clones_created_total = 5" || echo "WARN: clones_created_total not found"
echo "${METRICS}" | grep "pgmint_clones_destroyed_total 3" >/dev/null && echo "PASS: clones_destroyed_total = 3" || echo "WARN: clones_destroyed_total not found"
echo "${METRICS}" | grep "pgmint_clones_active 2" >/dev/null && echo "PASS: clones_active = 2" || echo "WARN: clones_active not found"

echo ""
echo "--- check postgres config metrics ---"
echo "${METRICS}" | grep "pgmint_postgres_max_connections 200" >/dev/null && echo "PASS: max_connections = 200" || echo "WARN: max_connections not 200"
echo "${METRICS}" | grep "pgmint_postgres_connections_total" >/dev/null && echo "PASS: connections_total present" || echo "WARN: connections_total not found"
echo "${METRICS}" | grep "pgmint_postgres_connections_by_state" >/dev/null && echo "PASS: connections_by_state present" || echo "WARN: connections_by_state not found"
echo "${METRICS}" | grep "pgmint_postgres_connections_by_database" >/dev/null && echo "PASS: connections_by_database present" || echo "WARN: connections_by_database not found"

echo ""
echo "--- verify pg_param applied in postgres ---"
MAX_CONN_VAL=$(PGPASSWORD=testpass psql -h localhost -p "${PG_PORT}" -U postgres -t -c "SHOW max_connections;" 2>/dev/null | tr -d '[:space:]')
assert_status "max_connections in postgres" "200" "${MAX_CONN_VAL}"

echo ""
echo "=== ALL TESTS PASSED ==="
