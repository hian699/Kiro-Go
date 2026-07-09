package proxy

import "sync"

// concReason names why a concurrency Acquire was rejected. concReasonNone means
// the slot was acquired.
type concReason string

const (
	concReasonNone   concReason = ""
	concReasonIP     concReason = "too_many_ip_connections"
	concReasonGlobal concReason = "server_saturated"
)

// concurrencyLimiter tracks in-flight request counts per IP and globally for the
// DoS guard. A slot is taken by Acquire and returned by the release func it
// hands back, so a slot is held for the entire request lifetime (including SSE
// streams). State is in-memory only.
type concurrencyLimiter struct {
	mu     sync.Mutex
	perIP  map[string]int
	global int
}

func newConcurrencyLimiter() *concurrencyLimiter {
	return &concurrencyLimiter{perIP: make(map[string]int)}
}

// Acquire attempts to take one in-flight slot for ip. perIPLimit / globalLimit
// <= 0 disable that check. The global cap is checked before the per-IP cap so a
// saturated process reports concReasonGlobal first. Returns a release func that
// must be called exactly once; on rejection the release is a safe no-op.
func (l *concurrencyLimiter) Acquire(ip string, perIPLimit, globalLimit int) (func(), concReason) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if globalLimit > 0 && l.global >= globalLimit {
		return func() {}, concReasonGlobal
	}
	if perIPLimit > 0 && l.perIP[ip] >= perIPLimit {
		return func() {}, concReasonIP
	}
	l.global++
	l.perIP[ip]++
	var once sync.Once
	release := func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.global--
			if l.perIP[ip] <= 1 {
				delete(l.perIP, ip)
			} else {
				l.perIP[ip]--
			}
		})
	}
	return release, concReasonNone
}
