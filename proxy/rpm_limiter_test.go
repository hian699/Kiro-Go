package proxy

import "testing"

func TestIPRPMLimiterCap(t *testing.T) {
	l := newIPRPMLimiter()
	// limit 3: first 3 allowed, 4th rejected within same window.
	for i := 0; i < 3; i++ {
		if !l.Allow("1.1.1.1", 3) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow("1.1.1.1", 3) {
		t.Fatalf("4th request should be rejected")
	}
}

func TestIPRPMLimiterPerIPIsolation(t *testing.T) {
	l := newIPRPMLimiter()
	if !l.Allow("1.1.1.1", 1) {
		t.Fatalf("A first should be allowed")
	}
	if l.Allow("1.1.1.1", 1) {
		t.Fatalf("A second should be rejected")
	}
	// A different IP has its own budget.
	if !l.Allow("2.2.2.2", 1) {
		t.Fatalf("B first should be allowed despite A being capped")
	}
}

func TestIPRPMLimiterUnlimited(t *testing.T) {
	l := newIPRPMLimiter()
	for i := 0; i < 100; i++ {
		if !l.Allow("1.1.1.1", 0) {
			t.Fatalf("limit 0 must always allow")
		}
	}
}

func TestIPRPMLimiterWindowRoll(t *testing.T) {
	l := newIPRPMLimiter()
	if !l.Allow("1.1.1.1", 1) {
		t.Fatalf("first allowed")
	}
	if l.Allow("1.1.1.1", 1) {
		t.Fatalf("second rejected in same window")
	}
	// Force the window to look expired.
	l.mu.Lock()
	l.windows["1.1.1.1"].windowStart -= dosRPMWindowSeconds
	l.mu.Unlock()
	if !l.Allow("1.1.1.1", 1) {
		t.Fatalf("after window roll, should be allowed again")
	}
}

func TestIPRPMLimiterSweep(t *testing.T) {
	l := newIPRPMLimiter()
	l.Allow("1.1.1.1", 5)
	l.mu.Lock()
	l.windows["1.1.1.1"].lastSeen -= dosRPMStaleSeconds + 1
	l.mu.Unlock()
	l.sweep()
	l.mu.Lock()
	_, ok := l.windows["1.1.1.1"]
	l.mu.Unlock()
	if ok {
		t.Fatalf("stale window should be swept")
	}
}
