package auth

import (
	"kiro-go/config"
	"path/filepath"
	"strings"
	"testing"
)

// TestRefreshTokenBlocksWhenRequireProxyAndNoProxy verifies the require-proxy
// gate fires inside RefreshToken before any network client is built, so a
// token refresh never leaks the server's real IP when require-proxy is on and
// no proxy is configured. The error carries "require-proxy" so the failover
// classifier cools down and rotates the account.
func TestRefreshTokenBlocksWhenRequireProxyAndNoProxy(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("config.Init: %v", err)
	}
	if err := config.UpdateRequireProxy(true); err != nil {
		t.Fatalf("enable require-proxy: %v", err)
	}

	acc := &config.Account{AuthMethod: "social", RefreshToken: "rt", ProxyURL: ""}
	_, _, _, _, err := RefreshToken(acc)
	if err == nil {
		t.Fatal("expected refresh to be blocked, got nil error")
	}
	if !strings.Contains(err.Error(), "require-proxy") {
		t.Fatalf("expected require-proxy error, got %v", err)
	}
}

// TestRefreshTokenDoesNotBlockWhenRequireProxyOff confirms the gate is inert
// when require-proxy is off: RefreshToken proceeds past the gate (and fails
// later for an unrelated reason), never returning the require-proxy error.
func TestRefreshTokenDoesNotBlockWhenRequireProxyOff(t *testing.T) {
	if err := config.Init(filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatalf("config.Init: %v", err)
	}

	// OIDC account with empty clientID/secret fails at refreshOIDCToken's own
	// guard — proving the require-proxy gate did NOT fire.
	acc := &config.Account{AuthMethod: "oidc", RefreshToken: "rt"}
	_, _, _, _, err := RefreshToken(acc)
	if err == nil {
		t.Fatal("expected an error from downstream refresh, got nil")
	}
	if strings.Contains(err.Error(), "require-proxy") {
		t.Fatalf("require-proxy gate fired while disabled: %v", err)
	}
}
