package proxy

import (
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain initializes the global config singleton so requests that pass the
// guard and reach the auth/routing layer fail cleanly instead of dereferencing
// a nil config. The suite has no other TestMain; other tests re-init config with
// their own temp files, so this pre-init is harmless. It is required because the
// -run TestServeHTTPGuard filter runs these tests in isolation, where no other
// test would otherwise initialize config.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dosguard")
	if err != nil {
		panic(err)
	}
	if err := config.Init(filepath.Join(dir, "config.json")); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newHandlerWithGuard builds a Handler with only the guard wired, avoiding the
// full NewHandler startup (pool, goroutines). The routing switch only needs
// dosGuard to exercise the guard gate.
func newHandlerWithGuard(cfg dosConfig) *Handler {
	return &Handler{
		dosGuard: &dosGuard{cfg: cfg, rpm: newIPRPMLimiter(), conc: newConcurrencyLimiter()},
	}
}

func TestServeHTTPGuardBlocksBeforeAuth(t *testing.T) {
	h := newHandlerWithGuard(dosConfig{IPRPM: 1, TrustProxy: true})

	do := func() int {
		r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
		r.Header.Set("CF-Connecting-IP", "7.7.7.7")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// First request passes the guard (then fails downstream, but not 429).
	if code := do(); code == http.StatusTooManyRequests {
		t.Fatalf("first request should not be rate-limited")
	}
	// Second request from same IP is rejected by the guard with 429.
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("second request should be 429, got %d", code)
	}
}

func TestServeHTTPGuardExemptsHealth(t *testing.T) {
	h := newHandlerWithGuard(dosConfig{IPRPM: 1, TrustProxy: true})
	do := func() int {
		r := httptest.NewRequest(http.MethodGet, "/health", nil)
		r.Header.Set("CF-Connecting-IP", "8.8.8.8")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// Many liveness hits from one IP are never throttled.
	for i := 0; i < 5; i++ {
		if code := do(); code == http.StatusTooManyRequests {
			t.Fatalf("health check should never be rate-limited (hit %d)", i+1)
		}
	}
}
