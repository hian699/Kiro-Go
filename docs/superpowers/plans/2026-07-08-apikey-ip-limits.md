# API Key IP Limits & Allowlist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per API key, cap concurrent + total distinct client IPs, optionally restrict to an IP allowlist, and surface IP usage in admin + self-service UIs.

**Architecture:** New fields on `config.ApiKeyEntry` (persisted in `config.json`). One atomic enforcement function in `config/apikeys.go` (single lock, marks dirty via background flush). A `clientIP` extractor in `proxy` reads CF/XFF/XRI headers. Enforcement is hooked into `authenticate()` in the multi-key path. Admin + portal show counts.

**Tech Stack:** Go stdlib (`net`, `net/http`), existing config JSON persistence, vanilla JS admin panel.

## Global Constraints

- Never write config synchronously on the request hot path — use `markDirtyLocked()` (matches `RecordApiKeyUsage`). Admin actions (reset) may use `saveLocked()`.
- Active window = 10 minutes. Caps: `0` = unlimited, independently.
- Client IP precedence: `CF-Connecting-IP` → first element of `X-Forwarded-For` → `X-Real-IP` → host of `RemoteAddr`.
- Over-cap → `429`; allowlist miss → `403`.
- Unlimited-total hard cap: `maxSeenIPsHardLimit = 1000`, evict oldest `LastSeen`.
- Enforce only in the multi-key path (`config.HasApiKeys()`); legacy single-key path unaffected.
- Comments may be English or Chinese; match the surrounding file.

---

### Task 1: Data model + IP helpers (config)

**Files:**
- Modify: `config/config.go` (add fields to `ApiKeyEntry` ~line 207-215; add `SeenIP` type near it)
- Create: `config/apikey_ip.go`
- Test: `config/apikey_ip_test.go`

**Interfaces:**
- Produces:
  - `SeenIP{IP string; FirstSeen, LastSeen, Count int64}`
  - `ApiKeyEntry` fields: `MaxConcurrentIPs int`, `MaxTotalIPs int`, `IPAllowlist []string`, `SeenIPs []SeenIP`
  - `func ipMatchesAllowlist(ip string, list []string) bool`
  - `func ApiKeyIPStats(e ApiKeyEntry, window time.Duration) (concurrent, total int)`
  - `const maxSeenIPsHardLimit = 1000`

- [ ] **Step 1: Add fields + type to `config/config.go`**

In `ApiKeyEntry`, after `RequestsCount` (line ~214), add:

```go
	// IP limits (0 = unlimited)
	MaxConcurrentIPs int      `json:"maxConcurrentIps,omitempty"`
	MaxTotalIPs      int      `json:"maxTotalIps,omitempty"`
	IPAllowlist      []string `json:"ipAllowlist,omitempty"` // exact IP or CIDR; empty = disabled
	SeenIPs          []SeenIP `json:"seenIps,omitempty"`      // distinct IPs seen by this key
```

After the `ApiKeyEntry` struct closes (line ~215), add:

```go
// SeenIP is one distinct client IP observed for an API key, with first/last-seen
// timestamps (Unix seconds) and a hit counter. Used to enforce per-key IP caps.
type SeenIP struct {
	IP        string `json:"ip"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	Count     int64  `json:"count,omitempty"`
}
```

- [ ] **Step 2: Write failing test `config/apikey_ip_test.go`**

```go
package config

import (
	"net"
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
```

- [ ] **Step 3: Run — expect FAIL (undefined helpers)**

Run: `go test ./config/ -run 'TestIpMatchesAllowlist|TestApiKeyIPStats' -v`
Expected: FAIL (undefined: `ipMatchesAllowlist`, `ApiKeyIPStats`)

- [ ] **Step 4: Implement `config/apikey_ip.go`**

```go
package config

import (
	"net"
	"time"
)

// maxSeenIPsHardLimit bounds SeenIPs when MaxTotalIPs == 0 (unlimited), so the
// list cannot grow without bound. Oldest-LastSeen entries are evicted first.
const maxSeenIPsHardLimit = 1000

// ipMatchesAllowlist reports whether ip matches any entry in list. Each entry is
// parsed as a CIDR when it contains "/", otherwise as an exact IP. Unparseable
// entries are skipped. An empty list returns false (callers treat empty as "no
// allowlist" before calling this).
func ipMatchesAllowlist(ip string, list []string) bool {
	parsed := net.ParseIP(ip)
	for _, raw := range list {
		if raw == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(raw); err == nil {
			if parsed != nil && cidr.Contains(parsed) {
				return true
			}
			continue
		}
		if raw == ip {
			return true
		}
	}
	return false
}

// ApiKeyIPStats returns the concurrent (LastSeen within window) and total distinct
// IP counts for a copied entry. Pure; takes no lock.
func ApiKeyIPStats(e ApiKeyEntry, window time.Duration) (concurrent, total int) {
	total = len(e.SeenIPs)
	cutoff := time.Now().Add(-window).Unix()
	for _, s := range e.SeenIPs {
		if s.LastSeen >= cutoff {
			concurrent++
		}
	}
	return
}
```

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./config/ -run 'TestIpMatchesAllowlist|TestApiKeyIPStats' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/apikey_ip.go config/apikey_ip_test.go
git commit -m "feat(config): add API key IP-limit fields and helpers"
```

---

### Task 2: Enforcement + reset + UpdateApiKey patch (config)

**Files:**
- Modify: `config/apikey_ip.go` (add enforcement + reset)
- Modify: `config/apikeys.go` (`UpdateApiKey` — persist the 3 new config fields)
- Test: `config/apikey_ip_test.go`

**Interfaces:**
- Consumes: `SeenIP`, `ipMatchesAllowlist`, `maxSeenIPsHardLimit`, `markDirtyLocked`, `saveLocked`, `cfgLock`, `cfg`.
- Produces:
  - `type IPRejectReason string`
  - `const IPRejectForbidden/IPRejectTooManyConc/IPRejectTooManyTotal IPRejectReason`
  - `func EnforceAndRecordIP(keyID, ip string, window time.Duration) *IPRejectReason`
  - `func ResetApiKeyIPs(id string) error`

- [ ] **Step 1: Write failing tests (append to `config/apikey_ip_test.go`)**

```go
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
```

Add imports `"fmt"` and `"path/filepath"` to the test file. Add a test-only seam at the bottom of `config/apikey_ip.go`:

```go
// UpdateApiKeySeenIPsForTest overwrites SeenIPs for an entry. Test seam only.
func UpdateApiKeySeenIPsForTest(id string, seen []SeenIP) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	for i := range cfg.ApiKeys {
		if cfg.ApiKeys[i].ID == id {
			cfg.ApiKeys[i].SeenIPs = seen
			return saveLocked()
		}
	}
	return errors.New("api key not found")
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./config/ -run TestEnforce -v`
Expected: FAIL (undefined `EnforceAndRecordIP`, `ResetApiKeyIPs`, reject reasons)

- [ ] **Step 3: Implement enforcement in `config/apikey_ip.go`**

Add `"errors"` to imports. Append:

```go
// IPRejectReason names why an IP was rejected. A nil pointer = allowed.
type IPRejectReason string

const (
	IPRejectForbidden    IPRejectReason = "forbidden"               // allowlist miss -> 403
	IPRejectTooManyConc  IPRejectReason = "too_many_concurrent_ips" // -> 429
	IPRejectTooManyTotal IPRejectReason = "too_many_ips"            // -> 429
)

// EnforceAndRecordIP checks the allowlist and both IP caps for keyID against ip,
// records the hit on success, and returns nil when allowed or a non-nil reason
// when rejected. All under one cfgLock acquisition to avoid TOCTOU; successful
// mutations use markDirtyLocked (never a synchronous write on the request hot path).
func EnforceAndRecordIP(keyID, ip string, window time.Duration) *IPRejectReason {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	if cfg == nil {
		return nil
	}
	idx := -1
	for i := range cfg.ApiKeys {
		if cfg.ApiKeys[i].ID == keyID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil // auth already matched; defensive
	}
	e := &cfg.ApiKeys[idx]

	if len(e.IPAllowlist) > 0 && !ipMatchesAllowlist(ip, e.IPAllowlist) {
		r := IPRejectForbidden
		return &r
	}

	now := time.Now().Unix()
	cutoff := now - int64(window.Seconds())
	activeCount := 0
	knownIdx := -1
	for i := range e.SeenIPs {
		if e.SeenIPs[i].LastSeen >= cutoff {
			activeCount++
		}
		if e.SeenIPs[i].IP == ip {
			knownIdx = i
		}
	}

	if knownIdx >= 0 {
		wasActive := e.SeenIPs[knownIdx].LastSeen >= cutoff
		if !wasActive && e.MaxConcurrentIPs > 0 && activeCount >= e.MaxConcurrentIPs {
			r := IPRejectTooManyConc
			return &r
		}
		e.SeenIPs[knownIdx].LastSeen = now
		e.SeenIPs[knownIdx].Count++
		markDirtyLocked()
		return nil
	}

	// New IP.
	if e.MaxTotalIPs > 0 && len(e.SeenIPs) >= e.MaxTotalIPs {
		r := IPRejectTooManyTotal
		return &r
	}
	if e.MaxConcurrentIPs > 0 && activeCount >= e.MaxConcurrentIPs {
		r := IPRejectTooManyConc
		return &r
	}
	e.SeenIPs = append(e.SeenIPs, SeenIP{IP: ip, FirstSeen: now, LastSeen: now, Count: 1})
	if e.MaxTotalIPs == 0 && len(e.SeenIPs) > maxSeenIPsHardLimit {
		evictOldestSeenIP(e)
	}
	markDirtyLocked()
	return nil
}

// evictOldestSeenIP removes the entry with the smallest LastSeen. Caller holds cfgLock.
func evictOldestSeenIP(e *ApiKeyEntry) {
	if len(e.SeenIPs) == 0 {
		return
	}
	oldest := 0
	for i := 1; i < len(e.SeenIPs); i++ {
		if e.SeenIPs[i].LastSeen < e.SeenIPs[oldest].LastSeen {
			oldest = i
		}
	}
	e.SeenIPs = append(e.SeenIPs[:oldest], e.SeenIPs[oldest+1:]...)
}

// ResetApiKeyIPs clears the SeenIPs list for an entry (admin action, not hot path).
func ResetApiKeyIPs(id string) error {
	cfgLock.Lock()
	defer cfgLock.Unlock()
	if cfg == nil {
		return errors.New("config not initialized")
	}
	for i := range cfg.ApiKeys {
		if cfg.ApiKeys[i].ID == id {
			cfg.ApiKeys[i].SeenIPs = nil
			return saveLocked()
		}
	}
	return errors.New("api key not found")
}
```

- [ ] **Step 4: Persist new config fields in `UpdateApiKey` (`config/apikeys.go`)**

After the `cfg.ApiKeys[idx].ExpiresAt = patch.ExpiresAt` line (~131), add:

```go
	cfg.ApiKeys[idx].MaxConcurrentIPs = patch.MaxConcurrentIPs
	cfg.ApiKeys[idx].MaxTotalIPs = patch.MaxTotalIPs
	cfg.ApiKeys[idx].IPAllowlist = patch.IPAllowlist
```

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./config/ -v`
Expected: PASS (all config tests)

- [ ] **Step 6: Commit**

```bash
git add config/apikey_ip.go config/apikey_ip_test.go config/apikeys.go
git commit -m "feat(config): enforce per-key IP caps and allowlist"
```

---

### Task 3: Client IP extraction + auth hook (proxy)

**Files:**
- Create: `proxy/client_ip.go`
- Modify: `proxy/auth.go` (`authenticate` — call enforcement in the multi-key path)
- Test: `proxy/client_ip_test.go`

**Interfaces:**
- Consumes: `config.EnforceAndRecordIP`, `config.IPReject*`, `newAuthError`.
- Produces: `func clientIP(r *http.Request) string`
- Constant `ipActiveWindow = 10 * time.Minute` (in `proxy/client_ip.go`).

- [ ] **Step 1: Write failing test `proxy/client_ip_test.go`**

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIPPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{"cf wins", map[string]string{"CF-Connecting-IP": "1.1.1.1", "X-Forwarded-For": "2.2.2.2", "X-Real-IP": "3.3.3.3"}, "9.9.9.9:1234", "1.1.1.1"},
		{"xff first element", map[string]string{"X-Forwarded-For": "2.2.2.2, 4.4.4.4", "X-Real-IP": "3.3.3.3"}, "9.9.9.9:1234", "2.2.2.2"},
		{"x-real-ip", map[string]string{"X-Real-IP": "3.3.3.3"}, "9.9.9.9:1234", "3.3.3.3"},
		{"remoteaddr host only", nil, "9.9.9.9:1234", "9.9.9.9"},
		{"remoteaddr no port", nil, "9.9.9.9", "9.9.9.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
			r.RemoteAddr = c.remote
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != c.want {
				t.Fatalf("clientIP=%q want %q", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined clientIP)**

Run: `go test ./proxy/ -run TestClientIPPrecedence -v`
Expected: FAIL (undefined: `clientIP`)

- [ ] **Step 3: Implement `proxy/client_ip.go`**

```go
package proxy

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// ipActiveWindow is how long after its last request an IP still counts toward a
// key's concurrent-IP cap.
const ipActiveWindow = 10 * time.Minute

// clientIP resolves the real client IP behind cloudflared + traefik, in order of
// trust: CF-Connecting-IP, first X-Forwarded-For element, X-Real-IP, then the host
// portion of RemoteAddr. Proxy headers are trusted because the container only
// receives traffic from the reverse-proxy chain.
func clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./proxy/ -run TestClientIPPrecedence -v`
Expected: PASS

- [ ] **Step 5: Hook enforcement into `authenticate` (`proxy/auth.go`)**

In the `config.HasApiKeys()` block, after the over-limit check and before `return entry, nil` (line ~82), insert:

```go
		if reason := config.EnforceAndRecordIP(entry.ID, clientIP(r), ipActiveWindow); reason != nil {
			switch *reason {
			case config.IPRejectForbidden:
				return nil, newAuthError(http.StatusForbidden, "permission_error", "IP not allowed")
			case config.IPRejectTooManyConc:
				return nil, newAuthError(http.StatusTooManyRequests, "rate_limit_error", "concurrent IP limit exceeded")
			default:
				return nil, newAuthError(http.StatusTooManyRequests, "rate_limit_error", "IP limit exceeded")
			}
		}
```

- [ ] **Step 6: Write failing test for the auth hook (append to `proxy/auth_test.go`)**

```go
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
```

- [ ] **Step 7: Run — expect PASS**

Run: `go test ./proxy/ -run 'TestClientIPPrecedence|TestAuthenticateEnforcesAllowlist' -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add proxy/client_ip.go proxy/client_ip_test.go proxy/auth.go proxy/auth_test.go
git commit -m "feat(proxy): enforce per-key IP caps in authenticate"
```

---

### Task 4: Admin views + reset endpoint + route (proxy)

**Files:**
- Modify: `proxy/admin_apikeys.go` (`apiKeyView`/`toApiKeyView`, `apiKeyUpdateRequest`/`apiUpdateApiKey`, add `apiResetApiKeyIPs`)
- Modify: `proxy/handler.go` (`handleAdminAPI` routing ~line 2323)
- Test: `proxy/apikeys_test.go` (update-request round-trip is optional; covered by config tests)

**Interfaces:**
- Consumes: `config.ResetApiKeyIPs`, `config.ApiKeyIPStats`, `ipActiveWindow`.
- Produces: admin JSON fields `maxConcurrentIps`, `maxTotalIps`, `ipAllowlist`, `concurrentIps`, `totalIps`; route `POST /api-keys/{id}/reset-ips`.

- [ ] **Step 1: Extend `apiKeyView` + `toApiKeyView` (`proxy/admin_apikeys.go`)**

Add to the `apiKeyView` struct (after `RequestsCount`, line ~28):

```go
	MaxConcurrentIPs int      `json:"maxConcurrentIps,omitempty"`
	MaxTotalIPs      int      `json:"maxTotalIps,omitempty"`
	IPAllowlist      []string `json:"ipAllowlist,omitempty"`
	ConcurrentIPs    int      `json:"concurrentIps"`
	TotalIPs         int      `json:"totalIps"`
```

In `toApiKeyView`, compute stats and set the fields:

```go
func toApiKeyView(e config.ApiKeyEntry) apiKeyView {
	conc, total := config.ApiKeyIPStats(e, ipActiveWindow)
	return apiKeyView{
		ID:               e.ID,
		Name:             e.Name,
		KeyMasked:        config.MaskApiKey(e.Key),
		Enabled:          e.Enabled,
		Migrated:         e.Migrated,
		CreatedAt:        e.CreatedAt,
		LastUsedAt:       e.LastUsedAt,
		ExpiresAt:        e.ExpiresAt,
		TokenLimit:       e.TokenLimit,
		CreditLimit:      e.CreditLimit,
		TokensUsed:       e.TokensUsed,
		CreditsUsed:      e.CreditsUsed,
		RequestsCount:    e.RequestsCount,
		MaxConcurrentIPs: e.MaxConcurrentIPs,
		MaxTotalIPs:      e.MaxTotalIPs,
		IPAllowlist:      e.IPAllowlist,
		ConcurrentIPs:    conc,
		TotalIPs:         total,
	}
}
```

- [ ] **Step 2: Extend `apiKeyUpdateRequest` + `apiUpdateApiKey` (`proxy/admin_apikeys.go`)**

Add to `apiKeyUpdateRequest` (after `ExpiresAt`, line ~211):

```go
	MaxConcurrentIPs *int      `json:"maxConcurrentIps,omitempty"`
	MaxTotalIPs      *int      `json:"maxTotalIps,omitempty"`
	IPAllowlist      *[]string `json:"ipAllowlist,omitempty"`
```

In `apiUpdateApiKey`, after the `if req.ExpiresAt != nil {...}` block (~247), add:

```go
	if req.MaxConcurrentIPs != nil {
		patch.MaxConcurrentIPs = *req.MaxConcurrentIPs
	}
	if req.MaxTotalIPs != nil {
		patch.MaxTotalIPs = *req.MaxTotalIPs
	}
	if req.IPAllowlist != nil {
		patch.IPAllowlist = *req.IPAllowlist
	}
```

- [ ] **Step 3: Add `apiResetApiKeyIPs` handler (`proxy/admin_apikeys.go`)**

After `apiResetApiKeyUsage` (~line 480), add:

```go
func (h *Handler) apiResetApiKeyIPs(w http.ResponseWriter, r *http.Request, id string) {
	if err := config.ResetApiKeyIPs(id); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	updated := config.GetApiKeyEntry(id)
	if updated == nil {
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"apiKey":  toApiKeyView(*updated),
	})
}
```

- [ ] **Step 4: Wire the route (`proxy/handler.go`)**

Immediately after the `reset-usage` case (~line 2325), add a matching `reset-ips` case (place it before the generic `/api-keys/` GET/PUT/DELETE cases):

```go
	case strings.HasPrefix(path, "/api-keys/") && strings.HasSuffix(path, "/reset-ips") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api-keys/"), "/reset-ips")
		h.apiResetApiKeyIPs(w, r, id)
```

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output (success)

- [ ] **Step 6: Commit**

```bash
git add proxy/admin_apikeys.go proxy/handler.go
git commit -m "feat(proxy): admin IP-limit config, stats, and reset-ips endpoint"
```

---

### Task 5: Self-service surface (proxy)

**Files:**
- Modify: `proxy/request_log.go` (`apiKeySelfInfo` struct + `apiKeySelfInfo` handler)
- Test: none (covered by config tests + manual portal check in Task 7)

**Interfaces:**
- Consumes: `config.ApiKeyIPStats`, `ipActiveWindow`.
- Produces: self-info JSON fields `concurrentIps`, `totalIps`, `maxConcurrentIps`, `maxTotalIps`.

- [ ] **Step 1: Extend the `apiKeySelfInfo` struct (`proxy/request_log.go`)**

After `ExpiresAt` (~line 236), add:

```go
	ConcurrentIPs    int   `json:"concurrentIps"`
	TotalIPs         int   `json:"totalIps"`
	MaxConcurrentIPs int   `json:"maxConcurrentIps"`
	MaxTotalIPs      int   `json:"maxTotalIps"`
```

- [ ] **Step 2: Populate them in the handler (`proxy/request_log.go`)**

In `apiKeySelfInfo` handler, before the final `json.NewEncoder(w).Encode(...)` (~line 344), compute stats:

```go
	concIPs, totalIPs := config.ApiKeyIPStats(*entry, ipActiveWindow)
```

Then add to the `apiKeySelfInfo{...}` literal:

```go
		ConcurrentIPs:    concIPs,
		TotalIPs:         totalIPs,
		MaxConcurrentIPs: entry.MaxConcurrentIPs,
		MaxTotalIPs:      entry.MaxTotalIPs,
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add proxy/request_log.go
git commit -m "feat(proxy): expose per-key IP usage in self-service key info"
```

---

### Task 6: Admin frontend (web/index.html + web/app.js + locales)

**Files:**
- Modify: `web/index.html` (apiKeyForm — add 3 inputs after `apiKeyForm_expiresAt`, ~line 814)
- Modify: `web/app.js` (`openApiKeyModal` populate ~line 1930, `submitApiKeyModal` payload ~line 2020, key-card render ~line 1879, add `resetApiKeyIPs`)
- Modify: `web/locales/*.json` (labels)

**Interfaces:**
- Consumes: admin JSON fields from Task 4 (`maxConcurrentIps`, `maxTotalIps`, `ipAllowlist`, `concurrentIps`, `totalIps`).

- [ ] **Step 1: Add form inputs (`web/index.html`)**

After the `apiKeyForm_expiresAt` form-group (closes ~line 814), insert:

```html
        <div class="form-group">
          <label data-i18n="apiKeys.maxConcurrentIps"></label>
          <input type="number" id="apiKeyForm_maxConcurrentIps" min="0" step="1" value="0" />
          <small data-i18n="apiKeys.ipLimitHint"></small>
        </div>
        <div class="form-group">
          <label data-i18n="apiKeys.maxTotalIps"></label>
          <input type="number" id="apiKeyForm_maxTotalIps" min="0" step="1" value="0" />
          <small data-i18n="apiKeys.ipLimitHint"></small>
        </div>
        <div class="form-group">
          <label data-i18n="apiKeys.ipAllowlist"></label>
          <textarea id="apiKeyForm_ipAllowlist" rows="3" data-i18n-placeholder="apiKeys.ipAllowlistPlaceholder"></textarea>
          <small data-i18n="apiKeys.ipAllowlistHint"></small>
        </div>
```

- [ ] **Step 2: Populate on open (`web/app.js`, in `openApiKeyModal` ~line 1930)**

After the `$('apiKeyForm_expiresAt').value = ...` line, add:

```javascript
    $('apiKeyForm_maxConcurrentIps').value = entry ? String(entry.maxConcurrentIps || 0) : '0';
    $('apiKeyForm_maxTotalIps').value = entry ? String(entry.maxTotalIps || 0) : '0';
    $('apiKeyForm_ipAllowlist').value = (entry && entry.ipAllowlist) ? entry.ipAllowlist.join('\n') : '';
```

- [ ] **Step 3: Send in payload (`web/app.js`, in `submitApiKeyModal` ~line 2020)**

After the `expiresAt` computation, add:

```javascript
      const maxConcurrentIps = parseInt($('apiKeyForm_maxConcurrentIps').value, 10);
      const maxTotalIps = parseInt($('apiKeyForm_maxTotalIps').value, 10);
      const ipAllowlist = $('apiKeyForm_ipAllowlist').value
        .split('\n').map(function (s) { return s.trim(); }).filter(Boolean);
```

Then extend the `payload` object literal with:

```javascript
        maxConcurrentIps: isNaN(maxConcurrentIps) || maxConcurrentIps < 0 ? 0 : maxConcurrentIps,
        maxTotalIps: isNaN(maxTotalIps) || maxTotalIps < 0 ? 0 : maxTotalIps,
        ipAllowlist: ipAllowlist,
```

- [ ] **Step 4: Show IP usage on the key card (`web/app.js` ~line 1879)**

After the `creditsLine` definition, add:

```javascript
      const ipLine = usageLine(t('apiKeys.ipsUsed'), item.concurrentIps || 0, item.maxConcurrentIps || 0)
        + ' / ' + t('apiKeys.totalIps') + ': ' + (item.totalIps || 0)
        + (item.maxTotalIps ? ' / ' + item.maxTotalIps : '');
```

Include `ipLine` in the card markup where `tokensLine`/`creditsLine` are rendered.

- [ ] **Step 5: Add a "Reset IPs" action (`web/app.js`, near `resetApiKeyUsage` ~line 2092)**

```javascript
  async function resetApiKeyIPs(id) {
    if (!confirm(t('apiKeys.confirmResetIps'))) return;
    try {
      const res = await api('/api-keys/' + encodeURIComponent(id) + '/reset-ips', { method: 'POST' });
      const d = await res.json().catch(() => ({}));
      if (!res.ok || d.success === false) throw new Error(d.error || t('common.failed'));
      toast(t('apiKeys.ipsReset'), 'success');
      await loadApiKeys();
    } catch (e) {
      toast((e && e.message) || t('common.failed'), 'error');
    }
  }
```

Wire a button that calls `resetApiKeyIPs(item.id)` next to the existing reset-usage control, and expose it on `window` if the codebase attaches handlers that way (match the existing `resetApiKeyUsage` wiring).

- [ ] **Step 6: Add locale strings (`web/locales/en.json` and each other locale)**

Add under the `apiKeys` object (translate per locale; English shown):

```json
"maxConcurrentIps": "Max concurrent IPs",
"maxTotalIps": "Max total IPs",
"ipLimitHint": "0 = unlimited",
"ipAllowlist": "IP allowlist",
"ipAllowlistPlaceholder": "One IP or CIDR per line",
"ipAllowlistHint": "Empty = no restriction",
"ipsUsed": "IPs (active)",
"totalIps": "Total IPs",
"confirmResetIps": "Reset the seen-IP list for this key?",
"ipsReset": "IP list reset"
```

- [ ] **Step 7: Manual check**

Build and run: `go build -o kiro-go . && CONFIG_PATH=data/config.json ./kiro-go`
Open `/admin`, create/edit a key, set caps + allowlist, save, confirm values persist and the card shows IP counts.

- [ ] **Step 8: Commit**

```bash
git add web/index.html web/app.js web/locales
git commit -m "feat(web): API key IP-limit config and usage display in admin"
```

---

### Task 7: Self-service portal + full verification

**Files:**
- Modify: `web/portal.html` (show IP usage alongside token/credit ~line 848)

- [ ] **Step 1: Render IP usage in `web/portal.html`**

Where the info response `d` is consumed (after the token/credit gauges ~line 849), add a line showing:
`d.concurrentIps` / `d.maxConcurrentIps` (active) and `d.totalIps` / `d.maxTotalIps` (total). Follow the existing gauge/label markup so it matches the page style. Only show a cap when the corresponding `max*` is > 0; otherwise show the used count with no denominator.

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: no output

- [ ] **Step 4: End-to-end manual check**

Run the server. Using two different `CF-Connecting-IP` header values against `/v1/messages` with a key whose `maxConcurrentIps=1`:
- first IP → served
- second IP within 10m → `429` "concurrent IP limit exceeded"

With `ipAllowlist` set to one IP, a request from a different IP → `403`. Check `/check` portal shows the IP counts. Reset via admin, confirm counts clear.

- [ ] **Step 5: Commit**

```bash
git add web/portal.html
git commit -m "feat(web): show per-key IP usage in self-service portal"
```

## Self-review notes

- Spec §1 data model → Task 1. §2 client IP → Task 3. §3 enforcement → Task 2. §4 auth hook → Task 3. §5 admin → Tasks 4+6. §6 self-service → Tasks 5+7. §7 tests → Tasks 1-3,7.
- Per user global instruction, do NOT run the `git commit` steps unless the user asks; implement and leave changes pending, batching into one commit on request.
