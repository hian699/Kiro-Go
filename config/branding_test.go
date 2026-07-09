package config

import (
	"path/filepath"
	"testing"
)

func TestBrandingDefaults(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := GetSiteName(); got != DefaultSiteName {
		t.Fatalf("GetSiteName default = %q, want %q", got, DefaultSiteName)
	}
	if got := GetExpiredMessage(); got != DefaultExpiredMessage {
		t.Fatalf("GetExpiredMessage default = %q, want %q", got, DefaultExpiredMessage)
	}
	if got := GetQuotaMessage(); got != DefaultQuotaMessage {
		t.Fatalf("GetQuotaMessage default = %q, want %q", got, DefaultQuotaMessage)
	}
	sn, em, qm := GetBrandingRaw()
	if sn != "" || em != "" || qm != "" {
		t.Fatalf("GetBrandingRaw unset = (%q,%q,%q), want all empty", sn, em, qm)
	}
}

func TestUpdateBrandingRoundTrip(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := UpdateBranding("  My Proxy  ", " gone ", " no credits "); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := GetSiteName(); got != "My Proxy" {
		t.Fatalf("GetSiteName = %q, want trimmed %q", got, "My Proxy")
	}
	if got := GetExpiredMessage(); got != "gone" {
		t.Fatalf("GetExpiredMessage = %q, want %q", got, "gone")
	}
	if got := GetQuotaMessage(); got != "no credits" {
		t.Fatalf("GetQuotaMessage = %q, want %q", got, "no credits")
	}
	// Reload from disk and confirm persistence.
	if err := Init(cfgFile); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if got := GetSiteName(); got != "My Proxy" {
		t.Fatalf("after reload GetSiteName = %q, want %q", got, "My Proxy")
	}
	// Empty values reset to defaults on read.
	if err := UpdateBranding("", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := GetSiteName(); got != DefaultSiteName {
		t.Fatalf("after clear GetSiteName = %q, want default %q", got, DefaultSiteName)
	}
}
