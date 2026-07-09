package proxy

import (
	"sync"
	"time"
)

// dosRPMWindowSeconds is the fixed window over which the per-IP RPM cap is
// enforced.
const dosRPMWindowSeconds int64 = 60

// dosRPMStaleSeconds is how long an IP's window may sit untouched before the
// background sweep drops it, bounding the map to recently-active IPs.
const dosRPMStaleSeconds int64 = 300

// ipRPMWindow holds a single IP's fixed-window request counter. windowStart is
// the Unix second the current window began; requests accumulate until it rolls.
type ipRPMWindow struct {
	windowStart int64
	requests    int
	lastSeen    int64
}

// ipRPMLimiter enforces a per-IP requests-per-minute cap for the DoS guard.
// State is in-memory only and independent of the per-key rateLimiter.
type ipRPMLimiter struct {
	mu      sync.Mutex
	windows map[string]*ipRPMWindow
}

func newIPRPMLimiter() *ipRPMLimiter {
	return &ipRPMLimiter{windows: make(map[string]*ipRPMWindow)}
}

// Allow rolls ip's window and, when under limit, counts this request and returns
// true. limit <= 0 means unlimited (always allowed, not counted). A rejected
// request is not counted.
func (l *ipRPMLimiter) Allow(ip string, limit int) bool {
	if limit <= 0 {
		return true
	}
	now := time.Now().Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[ip]
	if w == nil {
		w = &ipRPMWindow{windowStart: now}
		l.windows[ip] = w
	}
	if now-w.windowStart >= dosRPMWindowSeconds {
		w.windowStart = now
		w.requests = 0
	}
	w.lastSeen = now
	if w.requests >= limit {
		return false
	}
	w.requests++
	return true
}

// sweep drops windows untouched for longer than dosRPMStaleSeconds.
func (l *ipRPMLimiter) sweep() {
	cutoff := time.Now().Unix() - dosRPMStaleSeconds
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, w := range l.windows {
		if w.lastSeen < cutoff {
			delete(l.windows, ip)
		}
	}
}
