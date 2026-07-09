package proxy

import (
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthenticateEnforcesAllowlist(t *testing.T) {
	mustInitConfig(t)
	created, err := config.AddApiKey(config.ApiKeyEntry{Name: "ip", Key: "sk-ip", Enabled: true, IPAllowlist: []string{"5.5.5.5"}})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	_ = created

	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-ip")
	r.Header.Set("CF-Connecting-IP", "1.2.3.4") // not in allowlist
	if _, err := h.authenticate(r); err == nil {
		t.Fatalf("expected forbidden for disallowed IP")
	} else if ae, ok := err.(*authError); !ok || ae.status != http.StatusForbidden {
		t.Fatalf("expected 403 authError, got %v", err)
	}

	r2 := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r2.Header.Set("Authorization", "Bearer sk-ip")
	r2.Header.Set("CF-Connecting-IP", "5.5.5.5") // allowed
	if _, err := h.authenticate(r2); err != nil {
		t.Fatalf("expected allow for allowlisted IP, got %v", err)
	}
}

func TestAuthenticateCustomExpiredMessage(t *testing.T) {
	mustInitConfig(t)
	// expired 1h ago
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "exp", Key: "sk-exp", Enabled: true, ExpiresAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	if err := config.UpdateBranding("", "your key has expired, contact support", ""); err != nil {
		t.Fatalf("branding: %v", err)
	}

	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-exp")
	_, err := h.authenticate(r)
	ae, ok := err.(*authError)
	if !ok {
		t.Fatalf("expected *authError, got %T", err)
	}
	if ae.status != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", ae.status)
	}
	if ae.message != "your key has expired, contact support" {
		t.Fatalf("expected custom expired message, got %q", ae.message)
	}
}

func TestAuthenticateCustomQuotaMessage(t *testing.T) {
	mustInitConfig(t)
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "q", Key: "sk-q", Enabled: true, TokenLimit: 10, TokensUsed: 10}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	if err := config.UpdateBranding("", "", "out of credits, please top up"); err != nil {
		t.Fatalf("branding: %v", err)
	}

	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-q")
	_, err := h.authenticate(r)
	ae, ok := err.(*authError)
	if !ok {
		t.Fatalf("expected *authError, got %T", err)
	}
	if ae.status != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", ae.status)
	}
	if ae.message != "out of credits, please top up" {
		t.Fatalf("expected custom quota message, got %q", ae.message)
	}
}

func TestAuthenticateDefaultMessagesWhenUnset(t *testing.T) {
	mustInitConfig(t)
	if _, err := config.AddApiKey(config.ApiKeyEntry{Name: "d", Key: "sk-d", Enabled: true, ExpiresAt: time.Now().Add(-time.Hour).Unix()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireAuth(t)
	h := &Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer sk-d")
	_, err := h.authenticate(r)
	ae, _ := err.(*authError)
	if ae == nil || ae.message != config.DefaultExpiredMessage {
		t.Fatalf("expected default expired message %q, got %v", config.DefaultExpiredMessage, err)
	}
}
