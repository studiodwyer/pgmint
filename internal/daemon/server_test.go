package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/studiodwyer/pgmint/internal/postgres"
)

type mockPostgres struct {
	pingErr        error
	createCloneErr error
	dropCloneErr   error
	createdClones  []string
	droppedClones  []string
	connStats      *postgres.ConnectionStats
	connStatsErr   error
}

func (m *mockPostgres) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *mockPostgres) CreateClone(ctx context.Context, sourceDB, cloneName string) error {
	m.createdClones = append(m.createdClones, cloneName)
	return m.createCloneErr
}

func (m *mockPostgres) DropClone(ctx context.Context, name string) error {
	m.droppedClones = append(m.droppedClones, name)
	return m.dropCloneErr
}

func (m *mockPostgres) GetConnectionStats(ctx context.Context) (*postgres.ConnectionStats, error) {
	if m.connStatsErr != nil {
		return nil, m.connStatsErr
	}
	if m.connStats != nil {
		return m.connStats, nil
	}
	return &postgres.ConnectionStats{
		TotalConnections: 1,
		MaxConnections:   100,
		ByDatabase:       map[string]int{"postgres": 1},
		ByState:          map[string]int{"active": 1},
	}, nil
}

func testServer(pg PostgresBackend) *Server {
	return New(pg, Config{
		PgHost:   "localhost",
		PgPort:   5432,
		Password: "testpass",
		SourceDB: "sourcedb",
	})
}

func TestHealthOK(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

func TestHealthUnhealthy(t *testing.T) {
	pg := &mockPostgres{pingErr: context.DeadlineExceeded}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestConnection(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/connection")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	expected := "postgres://postgres:testpass@localhost:5432/sourcedb?sslmode=disable"
	if body["connection_string"] != expected {
		t.Fatalf("expected %q, got %q", expected, body["connection_string"])
	}
}

func TestCreateClone(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/clone", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)

	if !strings.HasPrefix(body["clone_name"], "clone_") {
		t.Fatalf("expected clone_name to start with 'clone_', got %q", body["clone_name"])
	}
	if !strings.Contains(body["connection_string"], body["clone_name"]) {
		t.Fatalf("expected connection_string to contain clone name %q, got %q", body["clone_name"], body["connection_string"])
	}
	if len(pg.createdClones) != 1 {
		t.Fatalf("expected 1 clone created, got %d", len(pg.createdClones))
	}
}

func TestCreateCloneWithCustomName(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)

	if body["clone_name"] != "pr_123" {
		t.Fatalf("expected clone_name 'pr_123', got %q", body["clone_name"])
	}
	if !strings.Contains(body["connection_string"], "pr_123") {
		t.Fatalf("expected connection_string to contain 'pr_123', got %q", body["connection_string"])
	}
}

func TestCreateCloneWithReservedName(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/clone?name=clone_123", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCreateCloneWithSourceDBName(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/clone?name=sourcedb", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCreateCloneDuplicateName(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)
	resp.Body.Close()

	resp2, err := http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate name, got %d", resp2.StatusCode)
	}
}

func TestCreateCloneFromCloneWithCustomName(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp1, _ := http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)
	resp1.Body.Close()

	resp2, err := http.Post(ts.URL+"/clone/pr_123?name=test_1", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp2.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp2.Body).Decode(&body)

	if body["clone_name"] != "test_1" {
		t.Fatalf("expected clone_name 'test_1', got %q", body["clone_name"])
	}
	if len(pg.createdClones) != 2 || pg.createdClones[1] != "test_1" {
		t.Fatalf("expected second clone 'test_1', got %v", pg.createdClones)
	}
}

func TestCreateCloneEnvFormat(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/clone?name=pr_456&format=env", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
}

func TestCreateCloneError(t *testing.T) {
	pg := &mockPostgres{createCloneErr: context.DeadlineExceeded}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/clone", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, `pgmint_clones_failed_total{operation="create"} 1`) {
		t.Fatalf("expected clones_failed_total for create, got:\n%s", metrics)
	}
}

func TestDestroyCloneErrorFailedMetric(t *testing.T) {
	pg := &mockPostgres{dropCloneErr: context.DeadlineExceeded}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/clone/clone_123_ab", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, `pgmint_clones_failed_total{operation="destroy"} 1`) {
		t.Fatalf("expected clones_failed_total for destroy, got:\n%s", metrics)
	}
}

func TestHTTPRequestsMetric(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Get(ts.URL + "/")
	http.Get(ts.URL + "/")
	http.Get(ts.URL + "/connection")

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, `pgmint_http_requests_total{method="GET",path="/",status="200"} 2`) {
		t.Fatalf("expected 2 GET / requests, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `pgmint_http_requests_total{method="GET",path="/connection",status="200"} 1`) {
		t.Fatalf("expected 1 GET /connection request, got:\n%s", metrics)
	}
}

func TestHTTPDurationMetric(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Get(ts.URL + "/")

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, "pgmint_http_request_duration_seconds") {
		t.Fatalf("expected http_request_duration metric, got:\n%s", metrics)
	}
}

func TestCloneAgeMetric(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Post(ts.URL+"/clone?name=pr_age", "application/json", nil)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/clone/pr_age", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, "pgmint_clone_age_seconds") {
		t.Fatalf("expected clone_age metric, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, "pgmint_clone_age_seconds_count 1") {
		t.Fatalf("expected clone_age count 1, got:\n%s", metrics)
	}
}

func TestHTTPMetricsNormalizePath(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Post(ts.URL+"/clone?name=pr_path", "application/json", nil)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/clone/pr_path", nil)
	http.DefaultClient.Do(req)

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, `path="/clone/:name"`) {
		t.Fatalf("expected normalized path /clone/:name, got:\n%s", metrics)
	}
}

func getMetrics(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestListClonesEmpty(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/clone")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string][]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["clones"] == nil {
		t.Fatal("expected empty array, got nil")
	}
	if len(body["clones"]) != 0 {
		t.Fatalf("expected 0 clones, got %d", len(body["clones"]))
	}
}

func TestListClones(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)
	http.Post(ts.URL+"/clone?name=pr_456", "application/json", nil)

	resp, err := http.Get(ts.URL + "/clone")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string][]string
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body["clones"]) != 2 {
		t.Fatalf("expected 2 clones, got %d", len(body["clones"]))
	}
}

func TestDestroyClone(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/clone/pr_123", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if len(pg.droppedClones) != 1 || pg.droppedClones[0] != "pr_123" {
		t.Fatalf("expected pr_123 to be dropped, got %v", pg.droppedClones)
	}
}

func TestDestroyCloneWithRemoveOrphans(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	http.Post(ts.URL+"/clone?name=pr_123", "application/json", nil)
	http.Post(ts.URL+"/clone/pr_123?name=test_1", "application/json", nil)
	http.Post(ts.URL+"/clone/pr_123?name=test_2", "application/json", nil)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/clone/pr_123?remove-orphans=true", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if len(pg.droppedClones) != 3 {
		t.Fatalf("expected 3 drops, got %d: %v", len(pg.droppedClones), pg.droppedClones)
	}
}

func TestDestroyCloneError(t *testing.T) {
	pg := &mockPostgres{dropCloneErr: context.DeadlineExceeded}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/clone/clone_123_ab", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	pg := &mockPostgres{}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/connection", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestGenerateCloneName(t *testing.T) {
	name := generateCloneName()
	if !strings.HasPrefix(name, "clone_") {
		t.Fatalf("expected name to start with 'clone_', got %q", name)
	}
	parts := strings.SplitN(name, "_", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts in clone name, got %d", len(parts))
	}
	if len(parts[2]) != 4 {
		t.Fatalf("expected 4-char hex suffix, got %q (len %d)", parts[2], len(parts[2]))
	}
}

func TestConnectionStatsMetrics(t *testing.T) {
	pg := &mockPostgres{
		connStats: &postgres.ConnectionStats{
			TotalConnections: 5,
			MaxConnections:   200,
			ByDatabase:       map[string]int{"sourcedb": 3, "clone_123": 2},
			ByState:          map[string]int{"active": 2, "idle": 3},
		},
	}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.updateConnectionMetrics(ctx)

	metrics := getMetrics(t, ts)

	if !strings.Contains(metrics, "pgmint_postgres_connections_total 5") {
		t.Fatalf("expected connections_total 5, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, "pgmint_postgres_max_connections 200") {
		t.Fatalf("expected max_connections 200, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `pgmint_postgres_connections_by_state{state="active"} 2`) {
		t.Fatalf("expected connections_by_state active=2, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `pgmint_postgres_connections_by_state{state="idle"} 3`) {
		t.Fatalf("expected connections_by_state idle=3, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `pgmint_postgres_connections_by_database{database="sourcedb"} 3`) {
		t.Fatalf("expected connections_by_database sourcedb=3, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, `pgmint_postgres_connections_by_database{database="clone_123"} 2`) {
		t.Fatalf("expected connections_by_database clone_123=2, got:\n%s", metrics)
	}
}

func TestConnectionStatsMetricsReset(t *testing.T) {
	pg := &mockPostgres{
		connStats: &postgres.ConnectionStats{
			TotalConnections: 3,
			MaxConnections:   100,
			ByDatabase:       map[string]int{"db_a": 3},
			ByState:          map[string]int{"active": 3},
		},
	}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx := context.Background()
	srv.updateConnectionMetrics(ctx)

	metrics1 := getMetrics(t, ts)
	if !strings.Contains(metrics1, `pgmint_postgres_connections_by_database{database="db_a"} 3`) {
		t.Fatalf("expected db_a=3 after first update, got:\n%s", metrics1)
	}

	pg.connStats = &postgres.ConnectionStats{
		TotalConnections: 1,
		MaxConnections:   100,
		ByDatabase:       map[string]int{"db_b": 1},
		ByState:          map[string]int{"idle": 1},
	}

	srv.updateConnectionMetrics(ctx)

	metrics2 := getMetrics(t, ts)
	if strings.Contains(metrics2, `pgmint_postgres_connections_by_database{database="db_a"}`) {
		t.Fatalf("expected db_a to be removed after reset, got:\n%s", metrics2)
	}
	if !strings.Contains(metrics2, `pgmint_postgres_connections_by_database{database="db_b"} 1`) {
		t.Fatalf("expected db_b=1 after second update, got:\n%s", metrics2)
	}
}

func TestConnectionStatsError(t *testing.T) {
	pg := &mockPostgres{
		connStatsErr: context.DeadlineExceeded,
	}
	srv := testServer(pg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx := context.Background()
	srv.updateConnectionMetrics(ctx)

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, "pgmint_postgres_connections_total 0") {
		t.Fatalf("expected connections_total 0 after error, got:\n%s", metrics)
	}
	if !strings.Contains(metrics, "pgmint_postgres_max_connections 0") {
		t.Fatalf("expected max_connections 0 after error, got:\n%s", metrics)
	}
	if strings.Contains(metrics, "pgmint_postgres_connections_by_state{") {
		t.Fatalf("expected no state metrics after error, got:\n%s", metrics)
	}
	if strings.Contains(metrics, "pgmint_postgres_connections_by_database{") {
		t.Fatalf("expected no database metrics after error, got:\n%s", metrics)
	}
}

func TestCollectStatsBackground(t *testing.T) {
	pg := &mockPostgres{
		connStats: &postgres.ConnectionStats{
			TotalConnections: 7,
			MaxConnections:   100,
			ByDatabase:       map[string]int{"testdb": 7},
			ByState:          map[string]int{"active": 7},
		},
	}
	srv := testServer(pg)
	srv.config.StatsInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.Start(ctx)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	time.Sleep(150 * time.Millisecond)

	metrics := getMetrics(t, ts)
	if !strings.Contains(metrics, "pgmint_postgres_connections_total 7") {
		t.Fatalf("expected connection metrics after background collection, got:\n%s", metrics)
	}
}
