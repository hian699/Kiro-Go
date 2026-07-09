package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadDosConfig(t *testing.T) {
	t.Setenv("DOS_IP_RPM", "10")
	t.Setenv("DOS_IP_CONCURRENCY", "-3")   // negative -> 0
	t.Setenv("DOS_MAX_CONCURRENCY", "junk") // unparseable -> 0
	t.Setenv("DOS_TRUST_PROXY_HEADERS", "false")
	c := loadDosConfig()
	if c.IPRPM != 10 {
		t.Fatalf("IPRPM=%d want 10", c.IPRPM)
	}
	if c.IPConcurrency != 0 {
		t.Fatalf("IPConcurrency=%d want 0", c.IPConcurrency)
	}
	if c.MaxConcurrency != 0 {
		t.Fatalf("MaxConcurrency=%d want 0", c.MaxConcurrency)
	}
	if c.TrustProxy {
		t.Fatalf("TrustProxy should be false")
	}
	if !c.enabled() {
		t.Fatalf("enabled() should be true when IPRPM>0")
	}
}

func TestLoadDosConfigDefaults(t *testing.T) {
	t.Setenv("DOS_IP_RPM", "")
	t.Setenv("DOS_IP_CONCURRENCY", "")
	t.Setenv("DOS_MAX_CONCURRENCY", "")
	t.Setenv("DOS_TRUST_PROXY_HEADERS", "")
	c := loadDosConfig()
	if c.enabled() {
		t.Fatalf("no env => disabled")
	}
	if !c.TrustProxy {
		t.Fatalf("TrustProxy defaults true")
	}
}

func TestDosGuardDisabledPassthrough(t *testing.T) {
	g := &dosGuard{cfg: dosConfig{TrustProxy: true}, rpm: newIPRPMLimiter(), conc: newConcurrencyLimiter()}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	release, reject := g.check(r)
	if reject != nil {
		t.Fatalf("disabled guard must not reject")
	}
	if release == nil {
		t.Fatalf("disabled guard must return a non-nil no-op release")
	}
	release() // must not panic
}

func TestDosGuardRPMReject(t *testing.T) {
	g := &dosGuard{cfg: dosConfig{IPRPM: 1, TrustProxy: true}, rpm: newIPRPMLimiter(), conc: newConcurrencyLimiter()}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	if rel, reject := g.check(r); reject != nil {
		t.Fatalf("first request should pass, got reject; %v", reject)
	} else {
		rel()
	}
	_, reject := g.check(r)
	if reject == nil || reject.status != http.StatusTooManyRequests {
		t.Fatalf("second request should 429, got %v", reject)
	}
}

func TestDosGuardIPConcurrencyReject(t *testing.T) {
	g := &dosGuard{cfg: dosConfig{IPConcurrency: 1, TrustProxy: true}, rpm: newIPRPMLimiter(), conc: newConcurrencyLimiter()}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	rel, reject := g.check(r)
	if reject != nil {
		t.Fatalf("first should pass, got %v", reject)
	}
	_, reject = g.check(r) // slot still held
	if reject == nil || reject.status != http.StatusTooManyRequests {
		t.Fatalf("second concurrent should 429, got %v", reject)
	}
	rel()
}

func TestDosGuardGlobalReject(t *testing.T) {
	g := &dosGuard{cfg: dosConfig{MaxConcurrency: 1, TrustProxy: true}, rpm: newIPRPMLimiter(), conc: newConcurrencyLimiter()}
	r1 := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r1.Header.Set("CF-Connecting-IP", "1.1.1.1")
	rel, reject := g.check(r1)
	if reject != nil {
		t.Fatalf("first should pass, got %v", reject)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r2.Header.Set("CF-Connecting-IP", "2.2.2.2")
	_, reject = g.check(r2)
	if reject == nil || reject.status != http.StatusServiceUnavailable {
		t.Fatalf("global cap should 503, got %v", reject)
	}
	rel()
}

func TestDosGuardKeyTrustProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.RemoteAddr = "9.9.9.9:1234"
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")

	trusting := &dosGuard{cfg: dosConfig{TrustProxy: true}}
	if got := trusting.key(r); got != "1.2.3.4" {
		t.Fatalf("trusting key=%q want 1.2.3.4", got)
	}
	untrusting := &dosGuard{cfg: dosConfig{TrustProxy: false}}
	if got := untrusting.key(r); got != "9.9.9.9" {
		t.Fatalf("untrusting key=%q want 9.9.9.9", got)
	}
}

func TestWriteDosReject(t *testing.T) {
	w := httptest.NewRecorder()
	writeDosReject(w, &dosReject{status: http.StatusServiceUnavailable, retryAfter: 5, message: "busy", errType: "overloaded_error"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", w.Code)
	}
	if w.Header().Get("Retry-After") != "5" {
		t.Fatalf("Retry-After=%q want 5", w.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Type != "overloaded_error" || body.Error.Message != "busy" {
		t.Fatalf("body=%+v", body.Error)
	}
}

func TestIsLivenessPath(t *testing.T) {
	for _, p := range []string{"/health", "/"} {
		if !isLivenessPath(p) {
			t.Fatalf("%q should be liveness", p)
		}
	}
	if isLivenessPath("/v1/messages") {
		t.Fatalf("/v1/messages is not liveness")
	}
}
