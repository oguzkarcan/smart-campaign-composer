package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
			New(fakePinger{tt.pingErr}, nil).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

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
		})
	}
}
