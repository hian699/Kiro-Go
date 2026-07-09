package proxy

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// ipActiveWindow is how long after its last request an IP still counts toward a
// key's concurrent-IP cap.
const ipActiveWindow = 10 * time.Minute

// clientIP resolves the real client IP behind cloudflared + traefik, in order of
// trust: CF-Connecting-IP, first X-Forwarded-For element, X-Real-IP, then the host
// portion of RemoteAddr. Proxy headers are trusted because the container only
// receives traffic from the reverse-proxy chain.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
