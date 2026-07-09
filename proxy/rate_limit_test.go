package proxy

import "testing"

// TestRateLimiterUnlimited: both limits 0 → always allowed, never counted.
func TestRateLimiterUnlimited(t *testing.T) {
	rl := newRateLimiter()
	for i := 0; i < 100; i++ {
		if r := rl.Allow("k", 0, 0); r != nil {
			t.Fatalf("unlimited key rejected at call %d: %v", i, *r)
		}
	}
	if req, tok := rl.snapshot("k"); req != 0 || tok != 0 {
		t.Fatalf("unlimited key should not accumulate counters, got req=%d tok=%d", req, tok)
	}
}

// TestRateLimiterRPM: the (N+1)th request within the window is rejected.
func TestRateLimiterRPM(t *testing.T) {
	rl := newRateLimiter()
	const rpm = 3
	for i := 0; i < rpm; i++ {
		if r := rl.Allow("k", rpm, 0); r != nil {
			t.Fatalf("request %d should be allowed, got %v", i, *r)
		}
	}
	r := rl.Allow("k", rpm, 0)
	if r == nil || *r != RateRejectRPM {
		t.Fatalf("expected RPM rejection on request %d, got %v", rpm+1, r)
	}
	if req, _ := rl.snapshot("k"); req != rpm {
		t.Fatalf("rejected request must not be counted, snapshot req=%d want %d", req, rpm)
	}
}

// TestRateLimiterTPM: once accumulated tokens meet the cap, the next request is
// rejected before it runs (tokens are added post-completion via AddTokens).
func TestRateLimiterTPM(t *testing.T) {
	rl := newRateLimiter()
	const tpm = 100
	// First request passes (0 tokens consumed so far), then we record its cost.
	if r := rl.Allow("k", 0, tpm); r != nil {
		t.Fatalf("first request should pass under TPM, got %v", *r)
	}
	rl.AddTokens("k", tpm) // consumed the whole budget
	r := rl.Allow("k", 0, tpm)
	if r == nil || *r != RateRejectTPM {
		t.Fatalf("expected TPM rejection after budget consumed, got %v", r)
	}
	if _, tok := rl.snapshot("k"); tok != tpm {
		t.Fatalf("snapshot tokens=%d want %d", tok, tpm)
	}
}

// TestRateLimiterWindowRoll: forcing the window start into the past resets counters.
func TestRateLimiterWindowRoll(t *testing.T) {
	rl := newRateLimiter()
	if r := rl.Allow("k", 1, 0); r != nil {
		t.Fatalf("first request should pass, got %v", *r)
	}
	if r := rl.Allow("k", 1, 0); r == nil {
		t.Fatalf("second request in same window should be rejected")
	}
	// Age the window past the limit so the next Allow rolls it over.
	rl.mu.Lock()
	rl.windows["k"].windowStart -= rateWindowSeconds
	rl.mu.Unlock()
	if r := rl.Allow("k", 1, 0); r != nil {
		t.Fatalf("request after window roll should pass, got %v", *r)
	}
}

// TestRateLimiterReset clears a key's window.
func TestRateLimiterReset(t *testing.T) {
	rl := newRateLimiter()
	rl.Allow("k", 5, 0)
	rl.AddTokens("k", 42)
	rl.reset("k")
	if req, tok := rl.snapshot("k"); req != 0 || tok != 0 {
		t.Fatalf("after reset snapshot should be zero, got req=%d tok=%d", req, tok)
	}
}

// TestRateLimiterSweep drops stale windows but keeps fresh ones.
func TestRateLimiterSweep(t *testing.T) {
	rl := newRateLimiter()
	rl.Allow("stale", 5, 0)
	rl.Allow("fresh", 5, 0)
	rl.mu.Lock()
	rl.windows["stale"].lastSeen -= rateStaleSeconds + 1
	rl.mu.Unlock()
	rl.sweep()
	rl.mu.Lock()
	_, staleOK := rl.windows["stale"]
	_, freshOK := rl.windows["fresh"]
	rl.mu.Unlock()
	if staleOK {
		t.Fatalf("stale window should have been swept")
	}
	if !freshOK {
		t.Fatalf("fresh window should have survived sweep")
	}
}
