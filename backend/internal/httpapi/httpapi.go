// Package httpapi wires the HTTP routes of the API service.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const healthTimeout = 2 * time.Second

// Pinger reports whether a dependency is reachable; *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Server struct {
	db Pinger
}

func New(db Pinger) *Server {
	return &Server{db: db}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
