package proxy

import "testing"

func TestConcurrencyPerIPCap(t *testing.T) {
	l := newConcurrencyLimiter()
	r1, reason := l.Acquire("1.1.1.1", 2, 0)
	if reason != concReasonNone {
		t.Fatalf("1st acquire should succeed, got %q", reason)
	}
	r2, reason := l.Acquire("1.1.1.1", 2, 0)
	if reason != concReasonNone {
		t.Fatalf("2nd acquire should succeed, got %q", reason)
	}
	if _, reason := l.Acquire("1.1.1.1", 2, 0); reason != concReasonIP {
		t.Fatalf("3rd acquire should hit per-IP cap, got %q", reason)
	}
	// Releasing frees a slot.
	r1()
	if r3, reason := l.Acquire("1.1.1.1", 2, 0); reason != concReasonNone {
		t.Fatalf("after release should succeed, got %q", reason)
	} else {
		r3()
	}
	r2()
}

func TestConcurrencyGlobalCapCheckedFirst(t *testing.T) {
	l := newConcurrencyLimiter()
	// Global cap 1, generous per-IP cap. Second acquire (different IP) hits global.
	r1, reason := l.Acquire("1.1.1.1", 100, 1)
	if reason != concReasonNone {
		t.Fatalf("1st acquire should succeed, got %q", reason)
	}
	if _, reason := l.Acquire("2.2.2.2", 100, 1); reason != concReasonGlobal {
		t.Fatalf("global cap should fire, got %q", reason)
	}
	r1()
}

func TestConcurrencyUnlimited(t *testing.T) {
	l := newConcurrencyLimiter()
	rels := make([]func(), 0, 50)
	for i := 0; i < 50; i++ {
		rel, reason := l.Acquire("1.1.1.1", 0, 0)
		if reason != concReasonNone {
			t.Fatalf("limits 0 must always acquire, got %q", reason)
		}
		rels = append(rels, rel)
	}
	for _, rel := range rels {
		rel()
	}
}

func TestConcurrencyMapCleanup(t *testing.T) {
	l := newConcurrencyLimiter()
	rel, _ := l.Acquire("1.1.1.1", 5, 0)
	rel()
	l.mu.Lock()
	_, ok := l.perIP["1.1.1.1"]
	g := l.global
	l.mu.Unlock()
	if ok {
		t.Fatalf("per-IP entry should be deleted at zero")
	}
	if g != 0 {
		t.Fatalf("global count should be 0, got %d", g)
	}
}

func TestConcurrencyRejectReleaseIsNoop(t *testing.T) {
	l := newConcurrencyLimiter()
	r1, _ := l.Acquire("1.1.1.1", 1, 0)
	rel, reason := l.Acquire("1.1.1.1", 1, 0)
	if reason != concReasonIP {
		t.Fatalf("expected per-IP reject, got %q", reason)
	}
	rel() // no-op release on a rejected acquire must not underflow counters
	l.mu.Lock()
	c := l.perIP["1.1.1.1"]
	l.mu.Unlock()
	if c != 1 {
		t.Fatalf("count should stay 1 after no-op release, got %d", c)
	}
	r1()
}
