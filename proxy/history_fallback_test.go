package proxy

import (
	"errors"
	"testing"
)

func TestIsMalformedPayloadError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"HTTP 400 from Kiro IDE: Improperly formed request", true},
		{"HTTP 400 from Kiro IDE: CONTENT_LENGTH_EXCEEDS_THRESHOLD", true},
		{"http 400: improperly formed", true},
		{"HTTP 401 from Kiro IDE: unauthorized", false},
		{"HTTP 403 forbidden", false},
		{"HTTP 429 quota exceeded", false},
		{"HTTP 402 overage limit", false},
		{"HTTP 500 internal error", false},
		{"HTTP 400 from Kiro IDE: some other 400 reason", false}, // 400 but not payload-shape
		{"", false},
	}
	for _, c := range cases {
		if got := isMalformedPayloadError(c.msg); got != c.want {
			t.Errorf("isMalformedPayloadError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// fakeCall lets tests drive callWithHistoryFallback without real network I/O.
// It records which payloads were attempted, in order.
type fakeCall struct {
	attempts []*KiroPayload
	errs     []error // error to return for the i-th call
}

func (f *fakeCall) call(p *KiroPayload) error {
	f.attempts = append(f.attempts, p)
	if len(f.attempts)-1 < len(f.errs) {
		return f.errs[len(f.attempts)-1]
	}
	return nil
}

// callWithHistoryFallbackTestable mirrors callWithHistoryFallback but takes an
// injectable call func, so the fallback control flow can be tested in isolation
// (the production function calls CallKiroAPI directly).
func callWithHistoryFallbackTestable(rich, safe *KiroPayload, started func() bool, call func(*KiroPayload) error) error {
	err := call(rich)
	if err == nil {
		return nil
	}
	if rich == safe || !isMalformedPayloadError(err.Error()) || started() {
		return err
	}
	return call(safe)
}

func TestFallbackRetriesSafeOnMalformed400(t *testing.T) {
	rich := &KiroPayload{}
	safe := &KiroPayload{}
	f := &fakeCall{errs: []error{errors.New("HTTP 400 from Kiro IDE: Improperly formed request"), nil}}

	err := callWithHistoryFallbackTestable(rich, safe, func() bool { return false }, f.call)
	if err != nil {
		t.Fatalf("expected success after safe fallback, got %v", err)
	}
	if len(f.attempts) != 2 {
		t.Fatalf("expected 2 attempts (rich then safe), got %d", len(f.attempts))
	}
	if f.attempts[0] != rich || f.attempts[1] != safe {
		t.Fatalf("expected rich then safe payload order")
	}
}

func TestFallbackSkippedWhenAlreadyStreamed(t *testing.T) {
	rich := &KiroPayload{}
	safe := &KiroPayload{}
	f := &fakeCall{errs: []error{errors.New("HTTP 400 from Kiro IDE: Improperly formed request")}}

	err := callWithHistoryFallbackTestable(rich, safe, func() bool { return true }, f.call)
	if err == nil {
		t.Fatalf("expected error to propagate when already streamed")
	}
	if len(f.attempts) != 1 {
		t.Fatalf("expected no safe retry after streaming started, got %d attempts", len(f.attempts))
	}
}

func TestFallbackSkippedOnNonMalformedError(t *testing.T) {
	rich := &KiroPayload{}
	safe := &KiroPayload{}
	f := &fakeCall{errs: []error{errors.New("HTTP 429 quota exceeded")}}

	err := callWithHistoryFallbackTestable(rich, safe, func() bool { return false }, f.call)
	if err == nil {
		t.Fatalf("expected quota error to propagate")
	}
	if len(f.attempts) != 1 {
		t.Fatalf("expected no safe retry on non-malformed error, got %d attempts", len(f.attempts))
	}
}

func TestFallbackNoRetryWhenRichEqualsSafe(t *testing.T) {
	same := &KiroPayload{}
	f := &fakeCall{errs: []error{errors.New("HTTP 400 from Kiro IDE: Improperly formed request")}}

	err := callWithHistoryFallbackTestable(same, same, func() bool { return false }, f.call)
	if err == nil {
		t.Fatalf("expected error to propagate when rich==safe")
	}
	if len(f.attempts) != 1 {
		t.Fatalf("expected single attempt when rich==safe, got %d", len(f.attempts))
	}
}
