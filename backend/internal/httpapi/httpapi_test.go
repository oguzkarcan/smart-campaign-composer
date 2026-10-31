package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
		wantDB     string
	}{
		{"database reachable", nil, http.StatusOK, "ok"},
		{"database down", errors.New("connection refused"), http.StatusServiceUnavailable, "unreachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv := New(fakePinger{tt.pingErr}, nil, nil)
			srv.LLM = LLMInfo{Provider: "claude", Model: "claude-sonnet-5-5"}
			srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			var body healthResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Checks["database"] != tt.wantDB {
				t.Fatalf("database check = %q, want %q", body.Checks["database"], tt.wantDB)
			}
			// The UI's badge reads the model even when the database is down.
			if body.Provider != "claude" || body.Model != "claude-sonnet-5-5" {
				t.Fatalf("llm = %+v", body.LLMInfo)
			}
		})
	}
}

type fakeMetrics struct{ err error }

func (f fakeMetrics) WriteMetrics(_ context.Context, w io.Writer) error {
	if f.err != nil {
		return f.err
	}
	_, err := io.WriteString(w, "scc_campaigns{status=\"sending\"} 1\n")
	return err
}

func TestMetrics(t *testing.T) {
	w := httptest.NewRecorder()
	New(fakePinger{}, nil, fakeMetrics{}).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") ||
		w.Body.String() != "scc_campaigns{status=\"sending\"} 1\n" {
		t.Fatalf("%d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body)
	}

	// A failed read is a clean 500, not a partial scrape.
	w = httptest.NewRecorder()
	New(fakePinger{}, nil, fakeMetrics{errors.New("db down")}).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "scc_") {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
}
