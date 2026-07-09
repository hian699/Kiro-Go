# API key IP limits & allowlist — design

Date: 2026-07-08

## Problem

API keys currently limit usage by tokens and credits only (`config.ApiKeyEntry`
`TokenLimit`/`CreditLimit`). For an API-reselling setup, an operator also wants to
bound how many distinct client IPs a single key may be shared across, and to
optionally pin a key to a known set of IPs. Today one key can be freely shared
across unlimited machines.

## Goal

Per API key, let the operator:

- Cap the number of **concurrent** IPs (distinct IPs active within a rolling
  window).
- Cap the number of **total distinct** IPs seen (cumulative, manual reset only).
- Optionally restrict the key to an **IP allowlist** (exact IP or CIDR).
- See, in both the admin panel and the self-service portal, how many IPs a key
  has used (concurrent now, and total distinct).

## Decisions locked (from user)

- **Two separate caps**: `MaxConcurrentIPs` (distinct IPs active within the
  window) and `MaxTotalIPs` (cumulative distinct IPs). Value `0` = unlimited for
  each, independently.
- **Active window**: 10 minutes. An IP counts toward the concurrent cap if its
  last request was within the last 10 minutes.
- **Total reset**: manual only, via an admin endpoint. Never auto-resets.
- **Allowlist**: optional per key; empty = disabled (no allowlist filtering).
  Supports exact IPs and CIDR ranges.
- **Client IP source**: `CF-Connecting-IP` → first element of `X-Forwarded-For`
  → `X-Real-IP` → `RemoteAddr`. Deployment runs behind cloudflared + traefik, so
  proxy headers must be trusted; `RemoteAddr` alone is always the internal
  traefik IP.
- **Over-cap behavior**: reject the request. `429` for cap violations
  (concurrent / total), `403` for allowlist violations.
- **Storage**: persist the distinct-IP list into `config.json`. A count alone
  cannot tell whether a new IP is a repeat, so the list of seen IPs is required.

## Components

### 1. Data model (`config/config.go`)

Add to `ApiKeyEntry`:

```go
// IP limits (0 = unlimited)
MaxConcurrentIPs int      `json:"maxConcurrentIps,omitempty"`
MaxTotalIPs      int      `json:"maxTotalIps,omitempty"`
IPAllowlist      []string `json:"ipAllowlist,omitempty"` // exact IP or CIDR; empty = disabled
SeenIPs          []SeenIP `json:"seenIps,omitempty"`      // distinct IPs seen by this key
```

New type:

```go
type SeenIP struct {
    IP        string `json:"ip"`
    FirstSeen int64  `json:"firstSeen"`
    LastSeen  int64  `json:"lastSeen"`
    Count     int64  `json:"count,omitempty"`
}
```

`SeenIPs` bounding: when `MaxTotalIPs > 0`, the total cap naturally bounds the
slice. When `MaxTotalIPs == 0` (unlimited), a hard constant cap
(`maxSeenIPsHardLimit = 1000`) applies, evicting the oldest-`LastSeen` entry when
full. Under that condition the displayed "total distinct" means "distinct
recently seen".

### 2. Client IP extraction (`proxy`)

`clientIP(r *http.Request) string` returns the first non-empty of:

1. `CF-Connecting-IP`
2. first comma-separated element of `X-Forwarded-For` (trimmed)
3. `X-Real-IP`
4. host portion of `r.RemoteAddr` (strip `:port`)

### 3. Enforcement (`config/apikeys.go`)

Everything under one `cfgLock` acquisition to avoid TOCTOU, then
`markDirtyLocked()` (never a synchronous write on the request hot path — matches
`RecordApiKeyUsage`).

```go
type IPRejectReason string
const (
    IPRejectForbidden      IPRejectReason = "forbidden"                // allowlist miss → 403
    IPRejectTooManyConc    IPRejectReason = "too_many_concurrent_ips"  // → 429
    IPRejectTooManyTotal   IPRejectReason = "too_many_ips"             // → 429
)

// EnforceAndRecordIP checks allowlist + caps for keyID against ip, records the
// hit on success, and returns nil when allowed or a non-nil reason when rejected.
func EnforceAndRecordIP(keyID, ip string, window time.Duration) *IPRejectReason
```

Algorithm (holding the lock):

1. Find the entry by `keyID`. Not found → treat as allowed (auth already matched;
   defensive).
2. If `IPAllowlist` non-empty and `ip` matches no entry (exact or CIDR) →
   `IPRejectForbidden`.
3. Compute `now`, `activeCount` = count of `SeenIPs` with
   `LastSeen >= now-window`, and whether `ip` is already known.
4. If `ip` known:
   - already active (its `LastSeen >= now-window`) → bump `LastSeen`/`Count`,
     allow.
   - stale (outside window) → it will re-enter the active set. If
     `MaxConcurrentIPs > 0 && activeCount >= MaxConcurrentIPs` →
     `IPRejectTooManyConc`. Else bump `LastSeen`/`Count`, allow.
5. If `ip` new:
   - `MaxTotalIPs > 0 && len(SeenIPs) >= MaxTotalIPs` → `IPRejectTooManyTotal`.
   - `MaxConcurrentIPs > 0 && activeCount >= MaxConcurrentIPs` →
     `IPRejectTooManyConc`.
   - else append the new `SeenIP` (evicting oldest if over
     `maxSeenIPsHardLimit`), allow.
6. On any allow that mutated state, `markDirtyLocked()`.

Helper `ipMatchesAllowlist(ip string, list []string) bool` parses each list
entry as CIDR (if it contains `/`) or exact IP.

Also add:

- `ResetApiKeyIPs(id string) error` — clears `SeenIPs`, persists (synchronous
  `saveLocked`, admin action, not hot path).
- `ApiKeyIPStats(e ApiKeyEntry, window time.Duration) (concurrent, total int)` —
  pure helper for display.

### 4. Hook into auth (`proxy/auth.go`)

In `authenticate()`, after the existing token/credit over-limit check and before
returning the entry, extract `clientIP(r)` and call `EnforceAndRecordIP`. Map the
reason to an `authError`:

- `IPRejectForbidden` → `403 permission_error` "IP not allowed".
- `IPRejectTooManyConc` → `429 rate_limit_error` "concurrent IP limit exceeded".
- `IPRejectTooManyTotal` → `429 rate_limit_error` "IP limit exceeded".

`authenticate` already receives `r *http.Request`, so `clientIP(r)` reads the
headers directly — no signature change. The existing Claude/OpenAI error mappers
already translate `authError.status`/`code`/`message`, so no change there.

Only enforce when the entry path is used (multi-key). The legacy single-key path
and the master-switch-off path are unaffected (no per-key IP state exists).

### 5. Admin surface

- `apiKeyUsageView` (`proxy/request_log.go`): add `ConcurrentIPs`, `TotalIPs`,
  `MaxConcurrentIPs`, `MaxTotalIPs`, `IPAllowlist`.
- `UpdateApiKey` patch (`config/apikeys.go`): accept `MaxConcurrentIPs`,
  `MaxTotalIPs`, `IPAllowlist` (always overwritten, zero/empty valid). Never
  touches `SeenIPs`.
- New endpoint `POST /admin/api/apikeys/{id}/reset-ips` → `ResetApiKeyIPs`.
  Wire into `handleAdminAPI` routing next to the existing reset-usage route.
- `web/app.js` + admin form: inputs for the two caps and the allowlist
  (textarea, one IP/CIDR per line), display of concurrent/total IP counts, and a
  "Reset IPs" button.

### 6. Self-service surface

- `apiKeySelfInfo` struct (`proxy/request_log.go`): add `ConcurrentIPs`,
  `TotalIPs`, `MaxConcurrentIPs`, `MaxTotalIPs` so a customer sees how many IPs
  their key has used and the caps. Do not expose the raw IP list (privacy —
  customer already knows their own IPs; the list is operator diagnostics).
- `web/portal.html`: show concurrent / total IP usage vs caps alongside the
  existing token/credit display.

### 7. Tests

- `config/apikeys_test.go`:
  - allowlist: exact match allow, CIDR match allow, miss → forbidden, empty →
    no filtering.
  - concurrent cap: N active IPs allowed, N+1th new IP → too-many-concurrent;
    an IP going stale frees a slot.
  - total cap: N distinct IPs allowed, N+1th new → too-many-total; a repeat of
    a known IP does not consume total budget.
  - unlimited pruning: with `MaxTotalIPs==0`, exceeding `maxSeenIPsHardLimit`
    evicts oldest.
  - `ResetApiKeyIPs` clears the list.
  - `EnforceAndRecordIP` records `LastSeen`/`Count` correctly.
- `proxy` test for `clientIP` header precedence.

## Out of scope

- Rate limiting by request frequency (this is IP-cardinality, not throughput).
- Geo/ASN restrictions.
- Auto-expiring total distinct IPs (decision: manual reset only).
