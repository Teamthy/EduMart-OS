package httpserver

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)
func TestHealthz(t *testing.T) {
	s := New(":0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := httptest.NewRecorder(); req := httptest.NewRequest("GET", "/healthz", nil)
	s.Handler.ServeHTTP(r, req)
	if r.Code != 200 { t.Fatalf("expected 200, got %d", r.Code) }
}
