package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/studiodwyer/pgmint/internal/postgres"
)

// PostgresBackend defines the database operations needed by the daemon.
type PostgresBackend interface {
	Ping(ctx context.Context) error
	CreateClone(ctx context.Context, sourceDB, cloneName string) error
	DropClone(ctx context.Context, name string) error
	GetConnectionStats(ctx context.Context) (*postgres.ConnectionStats, error)
}

// Config holds daemon configuration.
type Config struct {
	PgHost        string
	PgPort        int
	Password      string
	SourceDB      string
	StatsInterval time.Duration
}

// Server is the clone management HTTP daemon.
type Server struct {
	pg        PostgresBackend
	config    Config
	mu        sync.Mutex
	reg       *prometheus.Registry
	metrics   *metrics
	parent    map[string]string
	databases map[string]bool
	createdAt map[string]time.Time
}

type metrics struct {
	clonesCreated           prometheus.Counter
	clonesDestroyed         prometheus.Counter
	clonesFailed            *prometheus.CounterVec
	clonesActive            prometheus.Gauge
	cloneDuration           prometheus.Histogram
	cloneAge                prometheus.Histogram
	postgresUp              prometheus.Gauge
	httpRequests            *prometheus.CounterVec
	httpDuration            *prometheus.HistogramVec
	pgConnectionsTotal      prometheus.Gauge
	pgMaxConnections        prometheus.Gauge
	pgConnectionsByState    *prometheus.GaugeVec
	pgConnectionsByDatabase *prometheus.GaugeVec
}

// New creates a new Server with the given backend and configuration.
func New(pg PostgresBackend, config Config) *Server {
	reg := prometheus.NewRegistry()
	m := &metrics{
		clonesCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pgmint_clones_created_total",
			Help: "Total number of clones created",
		}),
		clonesDestroyed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pgmint_clones_destroyed_total",
			Help: "Total number of clones destroyed",
		}),
		clonesFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgmint_clones_failed_total",
			Help: "Total number of failed clone operations",
		}, []string{"operation"}),
		clonesActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pgmint_clones_active",
			Help: "Current number of active clones",
		}),
		cloneDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pgmint_clone_create_duration_seconds",
			Help:    "Time taken to create a clone",
			Buckets: prometheus.DefBuckets,
		}),
		cloneAge: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pgmint_clone_age_seconds",
			Help:    "Age of clones at destruction time",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400, 28800, 86400},
		}),
		postgresUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pgmint_postgres_up",
			Help: "Whether PostgreSQL is reachable (1=up, 0=down)",
		}),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgmint_http_requests_total",
			Help: "Total number of HTTP requests",
		}, []string{"method", "path", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pgmint_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path"}),
		pgConnectionsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pgmint_postgres_connections_total",
			Help: "Total number of active PostgreSQL connections",
		}),
		pgMaxConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pgmint_postgres_max_connections",
			Help: "Configured PostgreSQL max_connections setting",
		}),
		pgConnectionsByState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "pgmint_postgres_connections_by_state",
			Help: "PostgreSQL connections grouped by state",
		}, []string{"state"}),
		pgConnectionsByDatabase: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "pgmint_postgres_connections_by_database",
			Help: "PostgreSQL connections grouped by database",
		}, []string{"database"}),
	}
	reg.MustRegister(
		m.clonesCreated,
		m.clonesDestroyed,
		m.clonesFailed,
		m.clonesActive,
		m.cloneDuration,
		m.cloneAge,
		m.postgresUp,
		m.httpRequests,
		m.httpDuration,
		m.pgConnectionsTotal,
		m.pgMaxConnections,
		m.pgConnectionsByState,
		m.pgConnectionsByDatabase,
	)

	m.postgresUp.Set(1)

	return &Server{
		pg:        pg,
		config:    config,
		reg:       reg,
		metrics:   m,
		parent:    make(map[string]string),
		databases: make(map[string]bool),
		createdAt: make(map[string]time.Time),
	}
}

// Start begins the background stats collector goroutine.
func (s *Server) Start(ctx context.Context) {
	interval := s.config.StatsInterval
	if interval == 0 {
		interval = 5 * time.Second
	}
	go s.collectStats(ctx, interval)
}

func (s *Server) collectStats(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.updateConnectionMetrics(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.updateConnectionMetrics(ctx)
		}
	}
}

func (s *Server) updateConnectionMetrics(ctx context.Context) {
	stats, err := s.pg.GetConnectionStats(ctx)
	if err != nil {
		slog.Debug("failed to collect connection stats", "error", err)
		return
	}

	s.metrics.pgConnectionsTotal.Set(float64(stats.TotalConnections))
	s.metrics.pgMaxConnections.Set(float64(stats.MaxConnections))

	s.metrics.pgConnectionsByState.Reset()
	for state, count := range stats.ByState {
		s.metrics.pgConnectionsByState.WithLabelValues(state).Set(float64(count))
	}

	s.metrics.pgConnectionsByDatabase.Reset()
	for db, count := range stats.ByDatabase {
		s.metrics.pgConnectionsByDatabase.WithLabelValues(db).Set(float64(count))
	}
}

// Handler returns an http.Handler for the daemon.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHealth)
	mux.Handle("/metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/connection", s.handleConnection)
	mux.HandleFunc("/clone", s.handleClone)
	mux.HandleFunc("/clone/", s.handleCloneByName)
	return s.metricsMiddleware(s.loggingMiddleware(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if err := s.pg.Ping(r.Context()); err != nil {
		s.metrics.postgresUp.Set(0)
		slog.Error("PostgreSQL health check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	s.metrics.postgresUp.Set(1)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"connection_string": s.connectionString(s.config.SourceDB),
	})
}

func (s *Server) handleClone(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListClones(w, r)
	case http.MethodPost:
		s.handleCreateCloneFromSource(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleCloneByName(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateCloneFromClone(w, r)
	case http.MethodDelete:
		s.handleDestroyClone(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleListClones(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.databases))
	for name := range s.databases {
		names = append(names, name)
	}
	sort.Strings(names)

	writeJSON(w, http.StatusOK, map[string][]string{"clones": names})
}

func (s *Server) handleCreateCloneFromSource(w http.ResponseWriter, r *http.Request) {
	s.createClone(w, r, s.config.SourceDB, "")
}

func (s *Server) handleCreateCloneFromClone(w http.ResponseWriter, r *http.Request) {
	sourceName := strings.TrimPrefix(r.URL.Path, "/clone/")
	if sourceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source clone name required"})
		return
	}
	s.createClone(w, r, sourceName, sourceName)
}

func (s *Server) resolveCloneName(r *http.Request) (string, error) {
	customName := r.URL.Query().Get("name")
	if customName == "" {
		return generateCloneName(), nil
	}

	if strings.HasPrefix(customName, "clone_") {
		return "", fmt.Errorf("name %q is reserved (clone_* names are auto-generated)", customName)
	}
	if customName == s.config.SourceDB {
		return "", fmt.Errorf("name %q is the source database", customName)
	}
	if s.databases[customName] {
		return "", fmt.Errorf("name %q already exists", customName)
	}

	return customName, nil
}

func (s *Server) createClone(w http.ResponseWriter, r *http.Request, sourceDB, parentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cloneName, err := s.resolveCloneName(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ctx := r.Context()

	start := time.Now()
	slog.Info("creating clone", "name", cloneName, "source", sourceDB)

	if err := s.pg.CreateClone(ctx, sourceDB, cloneName); err != nil {
		s.metrics.clonesFailed.WithLabelValues("create").Inc()
		slog.Error("failed to create clone", "name", cloneName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.databases[cloneName] = true
	s.createdAt[cloneName] = time.Now()
	if parentName != "" {
		s.parent[cloneName] = parentName
	}

	duration := time.Since(start)
	s.metrics.cloneDuration.Observe(duration.Seconds())
	s.metrics.clonesCreated.Inc()
	s.metrics.clonesActive.Set(float64(len(s.databases)))

	connStr := s.connectionString(cloneName)
	slog.Info("clone created", "name", cloneName, "source", sourceDB, "duration", duration.Round(time.Millisecond))

	if r.URL.Query().Get("format") == "env" {
		s.writeEnv(w, cloneName, connStr)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"connection_string": connStr,
		"clone_name":        cloneName,
	})
}

func (s *Server) handleDestroyClone(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cloneName := strings.TrimPrefix(r.URL.Path, "/clone/")
	if cloneName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "clone name required"})
		return
	}

	removeOrphans := r.URL.Query().Get("remove-orphans") == "true"
	ctx := r.Context()

	toDestroy := []string{cloneName}
	if removeOrphans {
		descendants := s.findDescendants(cloneName)
		toDestroy = append(toDestroy, descendants...)
	}

	for _, name := range toDestroy {
		slog.Info("destroying clone", "name", name, "remove_orphans", removeOrphans)

		if err := s.pg.DropClone(ctx, name); err != nil {
			s.metrics.clonesFailed.WithLabelValues("destroy").Inc()
			slog.Error("failed to destroy clone", "name", name, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		if created, ok := s.createdAt[name]; ok {
			s.metrics.cloneAge.Observe(time.Since(created).Seconds())
			delete(s.createdAt, name)
		}

		delete(s.databases, name)
		delete(s.parent, name)
		s.metrics.clonesDestroyed.Inc()
		slog.Info("clone destroyed", "name", name)
	}

	s.metrics.clonesActive.Set(float64(len(s.databases)))

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) findDescendants(name string) []string {
	var descendants []string
	for child, parent := range s.parent {
		if parent == name {
			descendants = append(descendants, child)
			descendants = append(descendants, s.findDescendants(child)...)
		}
	}
	return descendants
}

func (s *Server) connectionString(dbName string) string {
	return fmt.Sprintf("postgres://postgres:%s@%s:%d/%s?sslmode=disable",
		s.config.Password, s.config.PgHost, s.config.PgPort, dbName)
}

func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)

		path := normalizePath(r.URL.Path)
		status := strconv.Itoa(wrapped.statusCode)
		s.metrics.httpRequests.WithLabelValues(r.Method, path, status).Inc()
		s.metrics.httpDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start).String(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

func normalizePath(path string) string {
	if strings.HasPrefix(path, "/clone/") && path != "/clone/" {
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 {
			return "/clone/:name"
		}
	}
	return path
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (s *Server) writeEnv(w http.ResponseWriter, dbName, connStr string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "DATABASE_HOST=%s\n", s.config.PgHost)
	fmt.Fprintf(w, "DATABASE_PORT=%s\n", strconv.Itoa(s.config.PgPort))
	fmt.Fprintf(w, "DATABASE_USER=postgres\n")
	fmt.Fprintf(w, "DATABASE_PASSWORD=%s\n", s.config.Password)
	fmt.Fprintf(w, "DATABASE_NAME=%s\n", dbName)
	fmt.Fprintf(w, "DATABASE_URL=%s\n", connStr)
}

func generateCloneName() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("clone_%d_%s", time.Now().Unix(), hex.EncodeToString(b))
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
