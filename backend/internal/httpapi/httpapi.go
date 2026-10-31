// Package httpapi wires the HTTP routes of the API service.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/oguzkarcan/smart-campaign-composer/backend/internal/campaign"
)

const (
	healthTimeout  = 2 * time.Second
	metricsTimeout = 5 * time.Second
)

// Pinger reports whether a dependency is reachable; *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// MetricsWriter writes /metrics in the Prometheus text format;
// *metrics.PG satisfies it.
type MetricsWriter interface {
	WriteMetrics(ctx context.Context, w io.Writer) error
}

type Server struct {
	db        Pinger
	campaigns *campaign.Service
	metrics   MetricsWriter

	// eventsInterval is how often a campaign event stream checks for changes.
	eventsInterval time.Duration
}

func New(db Pinger, campaigns *campaign.Service, metrics MetricsWriter) *Server {
	return &Server{db: db, campaigns: campaigns, metrics: metrics, eventsInterval: time.Second}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /campaigns", s.handleListCampaigns)
	mux.HandleFunc("POST /campaigns", s.handleCreateCampaign)
	mux.HandleFunc("GET /campaigns/{id}", s.handleGetCampaign)
	mux.HandleFunc("POST /campaigns/{id}/segment", s.handleGenerateSegment)
	mux.HandleFunc("POST /campaigns/{id}/content", s.handleGenerateContent)
	mux.HandleFunc("PUT /campaigns/{id}/content/{language}/{channel}", s.handleEditContent)
	mux.HandleFunc("POST /campaigns/{id}/approve", s.handleApprove)
	mux.HandleFunc("GET /campaigns/{id}/recipients", s.handleRecipients)
	mux.HandleFunc("GET /campaigns/{id}/progress", s.handleProgress)
	mux.HandleFunc("GET /campaigns/{id}/events", s.handleEvents)
	return logRequests(mux)
}

type healthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// handleHealth returns 200 when the API can reach its database and 503
// otherwise, so orchestrators stop routing to an instance that cannot work.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	resp := healthResponse{Status: "ok", Checks: map[string]string{"database": "ok"}}
	status := http.StatusOK
	if err := s.db.Ping(ctx); err != nil {
		slog.Warn("health check failed", "check", "database", "err", err)
		resp.Status = "unavailable"
		resp.Checks["database"] = "unreachable"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

// handleMetrics renders into a buffer first, so a failed database read is a
// clean 500 instead of a truncated scrape.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), metricsTimeout)
	defer cancel()
	var buf bytes.Buffer
	if err := s.metrics.WriteMetrics(ctx, &buf); err != nil {
		slog.Error("metrics failed", "err", err)
		http.Error(w, "metrics unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// statusRecorder remembers the response status for the request log. Unwrap
// lets http.ResponseController reach the real writer to flush SSE.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
			return // polled every few seconds by compose and Prometheus
		}
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}
