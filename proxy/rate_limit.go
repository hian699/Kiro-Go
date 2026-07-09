package proxy

import (
	"sync"
	"time"
)

// rateWindowSeconds is the fixed window over which RPM/TPM limits are enforced.
const rateWindowSeconds int64 = 60

// rateStaleSeconds is how long a key's window may sit untouched before the
// background sweep drops it, bounding the map size to recently-active keys.
const rateStaleSeconds int64 = 300

// RateRejectReason names why a request was rejected by the rate limiter. A nil
// pointer means allowed.
type RateRejectReason string

const (
	RateRejectRPM RateRejectReason = "rpm_exceeded" // -> 429
	RateRejectTPM RateRejectReason = "tpm_exceeded" // -> 429
)

// rateWindow holds the per-key fixed-window counters. windowStart is the Unix
// second at which the current 60s window began; requests/tokens accumulate until
// the window rolls over.
type rateWindow struct {
	windowStart int64
	requests    int
	tokens      int64
	lastSeen    int64
}

// rateLimiter enforces per-key requests-per-minute and tokens-per-minute caps.
// Counters live in memory only (never persisted): they are ephemeral per-minute
// state, so writing them to config on every request would be wrong and costly.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*rateWindow
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windows: make(map[string]*rateWindow)}
}

// rollLocked returns the current window for keyID, resetting its counters when
// the 60s window has elapsed. Caller holds mu.
func (rl *rateLimiter) rollLocked(keyID string, now int64) *rateWindow {
	w := rl.windows[keyID]
	if w == nil {
		w = &rateWindow{windowStart: now}
		rl.windows[keyID] = w
	}
	if now-w.windowStart >= rateWindowSeconds {
		w.windowStart = now
		w.requests = 0
		w.tokens = 0
	}
	w.lastSeen = now
	return w
}

// Allow checks the RPM and TPM caps for keyID and, when allowed, counts this
// request against the RPM budget. TPM is checked against tokens already consumed
// in the current window (actual token cost is added later via AddTokens once the
// response completes). Limits of 0 mean unlimited. Returns nil when allowed or a
// non-nil reason when rejected; a rejected request is NOT counted.
func (rl *rateLimiter) Allow(keyID string, rpmLimit int, tpmLimit int64) *RateRejectReason {
	if rpmLimit <= 0 && tpmLimit <= 0 {
		return nil
	}
	now := time.Now().Unix()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	w := rl.rollLocked(keyID, now)

	if tpmLimit > 0 && w.tokens >= tpmLimit {
		r := RateRejectTPM
		return &r
	}
	if rpmLimit > 0 && w.requests >= rpmLimit {
		r := RateRejectRPM
		return &r
	}
	w.requests++
	return nil
}

// AddTokens adds the actual tokens consumed by a completed request to the key's
// current window. Called after a response finishes so subsequent requests in the
// same window see the accumulated cost. A no-op for empty keyID or non-positive n.
func (rl *rateLimiter) AddTokens(keyID string, n int64) {
	if keyID == "" || n <= 0 {
		return
	}
	now := time.Now().Unix()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	w := rl.rollLocked(keyID, now)
	w.tokens += n
}

// snapshot returns the current-window request and token counts for keyID, or
// zeros when no live window exists. Used by admin/self-info to show how much of
// the per-minute budget is currently consumed. A window whose 60s has elapsed
// reports zeros (it will reset on the next Allow/AddTokens).
func (rl *rateLimiter) snapshot(keyID string) (requests int, tokens int64) {
	if keyID == "" {
		return 0, 0
	}
	now := time.Now().Unix()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	w := rl.windows[keyID]
	if w == nil || now-w.windowStart >= rateWindowSeconds {
		return 0, 0
	}
	return w.requests, w.tokens
}

// reset clears the window for a single key. Used when an admin resets a key's
// usage so the per-minute counters start fresh too.
func (rl *rateLimiter) reset(keyID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.windows, keyID)
}

// sweep drops windows untouched for longer than rateStaleSeconds, keeping the
// map bounded to recently-active keys.
func (rl *rateLimiter) sweep() {
	cutoff := time.Now().Unix() - rateStaleSeconds
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for id, w := range rl.windows {
		if w.lastSeen < cutoff {
			delete(rl.windows, id)
		}
	}
}
