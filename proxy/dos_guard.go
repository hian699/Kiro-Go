package proxy

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// dosConfig holds the DoS-guard limits parsed once from the environment at
// startup. A zero value for any cap disables that cap.
type dosConfig struct {
	IPRPM          int  // DOS_IP_RPM: max requests/min per IP
	IPConcurrency  int  // DOS_IP_CONCURRENCY: max in-flight per IP
	MaxConcurrency int  // DOS_MAX_CONCURRENCY: max in-flight globally
	TrustProxy     bool // DOS_TRUST_PROXY_HEADERS: false => key on RemoteAddr only
}

// envInt reads name as a non-negative int; empty, negative, or unparseable => 0.
func envInt(name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// loadDosConfig reads the DOS_* env vars. TrustProxy defaults true; only an
// explicit "false"/"0" (case-insensitive) disables it.
func loadDosConfig() dosConfig {
	trust := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DOS_TRUST_PROXY_HEADERS"))) {
	case "false", "0":
		trust = false
	}
	return dosConfig{
		IPRPM:          envInt("DOS_IP_RPM"),
		IPConcurrency:  envInt("DOS_IP_CONCURRENCY"),
		MaxConcurrency: envInt("DOS_MAX_CONCURRENCY"),
		TrustProxy:     trust,
	}
}

// enabled reports whether any cap is active.
func (c dosConfig) enabled() bool {
	return c.IPRPM > 0 || c.IPConcurrency > 0 || c.MaxConcurrency > 0
}

// dosReject describes a rejected request.
type dosReject struct {
	status     int
	retryAfter int
	message    string
	errType    string
}

// dosGuard combines the per-IP RPM limiter and the concurrency limiter behind
// one env-driven config. When cfg is disabled, check is a passthrough.
type dosGuard struct {
	cfg  dosConfig
	rpm  *ipRPMLimiter
	conc *concurrencyLimiter
}

func newDosGuard() *dosGuard {
	return &dosGuard{
		cfg:  loadDosConfig(),
		rpm:  newIPRPMLimiter(),
		conc: newConcurrencyLimiter(),
	}
}

// key resolves the guard key for r: the proxy-aware client IP when TrustProxy,
// otherwise the host portion of RemoteAddr only (headers ignored).
func (g *dosGuard) key(r *http.Request) string {
	if g.cfg.TrustProxy {
		return clientIP(r)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// check enforces the RPM cap then acquires a concurrency slot. It returns a
// release func to be deferred (a no-op when the guard is disabled or the request
// is rejected) and a non-nil *dosReject when the request must be refused. RPM is
// checked before the slot is acquired so a rejected request never holds a slot.
func (g *dosGuard) check(r *http.Request) (func(), *dosReject) {
	if !g.cfg.enabled() {
		return func() {}, nil
	}
	k := g.key(r)
	if !g.rpm.Allow(k, g.cfg.IPRPM) {
		return func() {}, &dosReject{
			status: http.StatusTooManyRequests, retryAfter: 60,
			message: "requests-per-minute limit exceeded", errType: "rate_limit_error",
		}
	}
	release, reason := g.conc.Acquire(k, g.cfg.IPConcurrency, g.cfg.MaxConcurrency)
	switch reason {
	case concReasonGlobal:
		return func() {}, &dosReject{
			status: http.StatusServiceUnavailable, retryAfter: 5,
			message: "server is at capacity, retry shortly", errType: "overloaded_error",
		}
	case concReasonIP:
		return func() {}, &dosReject{
			status: http.StatusTooManyRequests, retryAfter: 5,
			message: "too many concurrent connections from your IP", errType: "rate_limit_error",
		}
	}
	return release, nil
}

// writeDosReject writes a JSON error body for a rejected request.
func writeDosReject(w http.ResponseWriter, rj *dosReject) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Retry-After", strconv.Itoa(rj.retryAfter))
	w.WriteHeader(rj.status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{"type": rj.errType, "message": rj.message},
	})
}

// isLivenessPath reports whether path is exempt from the DoS guard (liveness).
func isLivenessPath(path string) bool {
	return path == "/health" || path == "/"
}
