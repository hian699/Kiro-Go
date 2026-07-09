package config

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestIpMatchesAllowlist(t *testing.T) {
	cases := []struct {
		ip   string
		list []string
		want bool
	}{
		{"1.2.3.4", nil, false},
		{"1.2.3.4", []string{"1.2.3.4"}, true},
		{"1.2.3.5", []string{"1.2.3.4"}, false},
		{"10.0.0.7", []string{"10.0.0.0/24"}, true},
		{"10.0.1.7", []string{"10.0.0.0/24"}, false},
		{"1.2.3.4", []string{"bogus", "1.2.3.4"}, true},
	}
	for _, c := range cases {
		if got := ipMatchesAllowlist(c.ip, c.list); got != c.want {
			t.Fatalf("ipMatchesAllowlist(%q,%v)=%v want %v", c.ip, c.list, got, c.want)
		}
	}
}

func TestApiKeyIPStats(t *testing.T) {
	now := time.Now().Unix()
	e := ApiKeyEntry{SeenIPs: []SeenIP{
		{IP: "a", LastSeen: now},
		{IP: "b", LastSeen: now - 60},
		{IP: "c", LastSeen: now - 3600}, // stale beyond 10m
	}}
	conc, total := ApiKeyIPStats(e, 10*time.Minute)
	if total != 3 {
		t.Fatalf("total=%d want 3", total)
	}
	if conc != 2 {
		t.Fatalf("concurrent=%d want 2", conc)
	}
	_ = net.ParseIP // keep net import if unused later
}

func seedKey(t *testing.T, e ApiKeyEntry) ApiKeyEntry {
	t.Helper()
	cfgFile := filepath.Join(t.TempDir(), "config.json")
	if err := Init(cfgFile); err != nil {
		t.Fatalf("init: %v", err)
	}
	created, err := AddApiKey(e)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return created
}

func TestEnforceAllowlist(t *testing.T) {
	k := seedKey(t, ApiKeyEntry{Key: "sk-a", Enabled: true, IPAllowlist: []string{"9.9.9.9"}})
	if r := EnforceAndRecordIP(k.ID, "1.1.1.1", time.Minute); r == nil || *r != IPRejectForbidden {
		t.Fatalf("expected forbidden, got %v", r)
	}
	if r := EnforceAndRecordIP(k.ID, "9.9.9.9", time.Minute); r != nil {
		t.Fatalf("expected allow, got %v", *r)
	}
}

func TestEnforceTotalCap(t *testing.T) {
	k := seedKey(t, ApiKeyEntry{Key: "sk-t", Enabled: true, MaxTotalIPs: 2})
	if r := EnforceAndRecordIP(k.ID, "1.0.0.1", time.Minute); r != nil {
		t.Fatalf("ip1 allow, got %v", *r)
	}
	if r := EnforceAndRecordIP(k.ID, "1.0.0.2", time.Minute); r != nil {
		t.Fatalf("ip2 allow, got %v", *r)
	}
	// repeat of a known ip must not consume budget
	if r := EnforceAndRecordIP(k.ID, "1.0.0.1", time.Minute); r != nil {
		t.Fatalf("repeat allow, got %v", *r)
	}
	if r := EnforceAndRecordIP(k.ID, "1.0.0.3", time.Minute); r == nil || *r != IPRejectTooManyTotal {
		t.Fatalf("expected too-many-total, got %v", r)
	}
}

func TestEnforceConcurrentCap(t *testing.T) {
	k := seedKey(t, ApiKeyEntry{Key: "sk-c", Enabled: true, MaxConcurrentIPs: 1})
	if r := EnforceAndRecordIP(k.ID, "2.0.0.1", time.Minute); r != nil {
		t.Fatalf("ip1 allow, got %v", *r)
	}
	if r := EnforceAndRecordIP(k.ID, "2.0.0.2", time.Minute); r == nil || *r != IPRejectTooManyConc {
		t.Fatalf("expected too-many-conc, got %v", r)
	}
	// make ip1 stale, then a second IP fits within the concurrent budget again
	e := GetApiKeyEntry(k.ID)
	e.SeenIPs[0].LastSeen = time.Now().Add(-time.Hour).Unix()
	if err := UpdateApiKeySeenIPsForTest(k.ID, e.SeenIPs); err != nil {
		t.Fatalf("stale: %v", err)
	}
	if r := EnforceAndRecordIP(k.ID, "2.0.0.2", time.Minute); r != nil {
		t.Fatalf("expected allow after stale, got %v", *r)
	}
}

func TestEnforceUnlimitedPruning(t *testing.T) {
	k := seedKey(t, ApiKeyEntry{Key: "sk-p", Enabled: true}) // both caps 0 = unlimited
	seen := make([]SeenIP, maxSeenIPsHardLimit)
	base := time.Now().Unix()
	for i := range seen {
		seen[i] = SeenIP{IP: fmt.Sprintf("10.%d.%d.%d", i/65536, (i/256)%256, i%256), FirstSeen: base + int64(i), LastSeen: base + int64(i)}
	}
	if err := UpdateApiKeySeenIPsForTest(k.ID, seen); err != nil {
		t.Fatalf("seed seen: %v", err)
	}
	if r := EnforceAndRecordIP(k.ID, "200.200.200.200", time.Minute); r != nil {
		t.Fatalf("expected allow, got %v", *r)
	}
	got := GetApiKeyEntry(k.ID)
	if len(got.SeenIPs) != maxSeenIPsHardLimit {
		t.Fatalf("expected list capped at %d, got %d", maxSeenIPsHardLimit, len(got.SeenIPs))
	}
	if got.SeenIPs[0].IP == "10.0.0.0" {
		t.Fatalf("expected oldest evicted")
	}
}

func TestResetApiKeyIPs(t *testing.T) {
	k := seedKey(t, ApiKeyEntry{Key: "sk-r", Enabled: true})
	EnforceAndRecordIP(k.ID, "3.0.0.1", time.Minute)
	if err := ResetApiKeyIPs(k.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := GetApiKeyEntry(k.ID); len(got.SeenIPs) != 0 {
		t.Fatalf("expected empty seenIps, got %d", len(got.SeenIPs))
	}
}
