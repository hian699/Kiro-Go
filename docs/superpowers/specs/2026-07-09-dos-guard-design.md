# App-layer DoS guard — design

Date: 2026-07-09

## Problem

The proxy has two request-limiting layers today, but neither defends against
denial-of-service:

- Per-API-key RPM/TPM (`proxy/rate_limit.go`) — runs *after* auth, keyed on the
  matched key ID. A flood of requests with invalid or missing keys never reaches
  it.
- Per-key IP caps / allowlist (`config/apikey_ip.go`) — also post-auth, and is a
  key-sharing control (how many distinct IPs a key spans), not a throughput
  control.

So an attacker can hammer `/v1/*` with garbage keys, brute-force `/admin`, or open
many simultaneous streaming connections, and nothing throttles them before the
request has already consumed auth/config work and a goroutine. We want an
app-layer guard that sits in front of everything, keyed on client IP, configured
by environment variables, and disabled by default so existing deployments are
unaffected until they opt in.

## Goal

A single pre-auth gate in `ServeHTTP` that, per client IP, enforces:

- **Requests per minute** (fixed 60s window).
- **Concurrent in-flight requests per IP** (held for the whole request lifetime,
  including SSE streams).
- **Global concurrent in-flight requests** across all IPs (a process ceiling).

All limits are in-memory, opt-in (`0` = off), and read once from env at startup.
Reference reverse-proxy configs (nginx, traefik, cloudflared) document a layered
defense in front of the app guard.

## Decisions locked (from user)

- **Placement**: pre-auth, all routes. Runs at the top of `ServeHTTP` after the
  `OPTIONS` early-return and before routing, so invalid-key floods and
  `/admin` brute-force are covered too. `/health` and `/` (liveness) are exempt so
  monitors are never throttled.
- **Mechanism**: per-IP RPM + per-IP concurrency + global concurrency. No token
  bucket (fixed window matches the existing limiter).
- **Config surface**: ENV only, off by default. No `config.json` fields, no admin
  UI, no version bump.
- **Client IP source**: reuse `clientIP(r)` (CF-Connecting-IP → first XFF element
  → X-Real-IP → RemoteAddr). A `DOS_TRUST_PROXY_HEADERS=false` toggle falls back
  to `RemoteAddr` host only, for the case where the app is exposed directly and
  proxy headers could be forged.
- **Reject codes**: per-IP RPM and per-IP concurrency → `429`; global concurrency
  → `503` (saturation is not the individual client's fault). All carry
  `Retry-After` and a small JSON body matching the existing error style.
- **Proxy configs**: reference snippets under `docs/deploy/` for nginx, traefik,
  and cloudflared. Not wired into the build or compose topology.

## Components

Package layout mirrors the existing `rate_limit.go` / `client_ip.go` split. The
existing per-key `rateLimiter` in `rate_limit.go` is left untouched; the guard
gets its own independent state so the two never share maps.

### 1. Env config (`proxy/dos_guard.go`)

```go
// dosConfig holds the DoS-guard limits parsed once from the environment at
// startup. A zero value for any cap means that cap is disabled.
type dosConfig struct {
    IPRPM          int  // DOS_IP_RPM: max requests/min per IP
    IPConcurrency  int  // DOS_IP_CONCURRENCY: max in-flight per IP
    MaxConcurrency int  // DOS_MAX_CONCURRENCY: max in-flight globally
    TrustProxy     bool // DOS_TRUST_PROXY_HEADERS: false => key on RemoteAddr only
}

func loadDosConfig() dosConfig
```

| ENV var | Meaning | Default |
|---|---|---|
| `DOS_IP_RPM` | max requests/min per IP | `0` (off) |
| `DOS_IP_CONCURRENCY` | max in-flight per IP | `0` (off) |
| `DOS_MAX_CONCURRENCY` | max in-flight globally | `0` (off) |
| `DOS_TRUST_PROXY_HEADERS` | trust CF/XFF/XRI headers for keying | `true` |

Parsing rules: integers via `strconv.Atoi`, negative or unparseable → `0` (off).
`DOS_TRUST_PROXY_HEADERS` is `false` only for exact `"false"`/`"0"` (case-insensitive);
anything else, including empty, is `true`.

`(c dosConfig) enabled() bool` reports whether any cap is > 0. When disabled the
guard is a no-op passthrough (no map allocation cost on the hot path beyond a
bool check).

### 2. Per-IP RPM (`proxy/rpm_limiter.go`)

A fixed-window limiter keyed on IP string, structurally identical to the existing
per-key one but independent state:

```go
const dosRPMWindowSeconds int64 = 60
const dosRPMStaleSeconds  int64 = 300

type ipRPMLimiter struct {
    mu      sync.Mutex
    windows map[string]*ipRPMWindow
}

type ipRPMWindow struct {
    windowStart int64
    requests    int
    lastSeen    int64
}

func newIPRPMLimiter() *ipRPMLimiter
// Allow rolls the window and, when under limit, counts the request. limit <= 0 =>
// always allowed. Returns true when allowed.
func (l *ipRPMLimiter) Allow(ip string, limit int) bool
// sweep drops windows untouched longer than dosRPMStaleSeconds.
func (l *ipRPMLimiter) sweep()
```

### 3. Per-IP + global concurrency (`proxy/ip_limiter.go`)

Tracks in-flight request counts. `Acquire` is called at the top of `ServeHTTP`;
the returned release func is `defer`red so a slot is held for the whole request
(including long SSE streams), then freed.

```go
type concurrencyLimiter struct {
    mu       sync.Mutex
    perIP    map[string]int
    global   int
}

func newConcurrencyLimiter() *concurrencyLimiter

// Acquire attempts to take one in-flight slot for ip. perIPLimit / globalLimit
// <= 0 disable that check. Returns (release, reason):
//   - reason == "" => acquired; call release() exactly once when the request ends.
//   - reason == concReasonIP     => per-IP cap hit; release is a no-op.
//   - reason == concReasonGlobal => global cap hit; release is a no-op.
// Global is checked before per-IP so a saturated process reports 503 first.
func (l *concurrencyLimiter) Acquire(ip string, perIPLimit, globalLimit int) (release func(), reason concReason)
```

`release` decrements both counters under the lock and deletes the per-IP map
entry when it reaches zero (so the map stays bounded to currently-active IPs — no
sweep needed for concurrency, only for RPM windows).

### 4. Guard orchestrator (`proxy/dos_guard.go`)

```go
type dosGuard struct {
    cfg  dosConfig
    rpm  *ipRPMLimiter
    conc *concurrencyLimiter
}

func newDosGuard() *dosGuard // reads loadDosConfig()

// key resolves the guard key for r: clientIP(r) when TrustProxy, else the host
// of RemoteAddr only.
func (g *dosGuard) key(r *http.Request) string

// check runs the RPM cap then acquires a concurrency slot. Returns:
//   - release func (nil when rejected or guard disabled)
//   - a *dosReject describing the rejection (nil when allowed)
func (g *dosGuard) check(r *http.Request) (release func(), reject *dosReject)

type dosReject struct {
    status     int    // 429 or 503
    retryAfter int    // seconds
    message    string
}
```

Order inside `check` (guard disabled → immediate allow with a no-op release):
1. RPM: `rpm.Allow(key, cfg.IPRPM)` false → `429`, `Retry-After: 60`.
2. Concurrency: `conc.Acquire(key, cfg.IPConcurrency, cfg.MaxConcurrency)`.
   - `concReasonGlobal` → `503`, `Retry-After: 5`.
   - `concReasonIP` → `429`, `Retry-After: 5`.
3. Allowed → return the acquire release func.

RPM is counted before the concurrency slot is acquired, so a rejected-by-RPM
request never holds a slot. If RPM passes but concurrency is rejected, no RPM
"refund" is issued (fixed-window limiters don't refund; matches existing behavior).

### 5. Hook into `ServeHTTP` (`proxy/handler.go`)

`Handler` gets a `dosGuard *dosGuard` field, initialized in `NewHandler`
(`dosGuard: newDosGuard()`). At the top of `ServeHTTP`, after the `OPTIONS`
early-return (~line 380) and before the routing `switch`:

```go
if r.Method != http.MethodOptions && !isLivenessPath(path) {
    release, reject := h.dosGuard.check(r)
    if reject != nil {
        writeDosReject(w, reject)
        return
    }
    defer release()
}
```

`isLivenessPath(path)` is true for `/health` and `/`. `writeDosReject` sets
`Content-Type: application/json`, `Retry-After`, the status, and a body like
`{"error":{"type":"rate_limit_error","message":"..."}}` (503 uses
`"type":"overloaded_error"`). Guard-disabled `check` returns a no-op release and
nil reject, so the `defer` is harmless.

The `defer release()` inside `ServeHTTP` holds the concurrency slot for the full
handler duration including streaming, which is exactly the slow-loris / connection
-exhaustion defense we want.

### 6. Background sweep (`proxy/handler.go`)

Extend the existing `backgroundRateSweep` to also sweep the guard's RPM windows:

```go
case <-ticker.C:
    h.rateLimiter.sweep()
    if h.dosGuard != nil {
        h.dosGuard.rpm.sweep()
    }
```

Concurrency needs no sweep (entries are deleted on release).

### 7. Reverse-proxy reference configs (`docs/deploy/`)

Reference snippets, not wired into build or compose. Each documents a layered
defense in front of the app guard and how to preserve the real client IP so
`clientIP(r)` still works.

- `docs/deploy/nginx.conf` — `limit_req_zone` + `limit_req` (RPM), `limit_conn_zone`
  + `limit_conn` (per-IP concurrency), Cloudflare `set_real_ip_from` /
  `real_ip_header CF-Connecting-IP`, and `proxy_set_header X-Forwarded-For`.
- `docs/deploy/traefik.md` — dynamic-config + docker-label forms of the
  `rateLimit` middleware (average/burst) and `inFlightReq` middleware
  (amount), plus `forwardedHeaders.trustedIPs` guidance.
- `docs/deploy/cloudflared.yml` — tunnel `ingress` config pointing at the app,
  with a note that Cloudflare-dashboard rate-limiting rules and
  `CF-Connecting-IP` propagation are the outermost layer.
- A short intro paragraph (in `docs/deploy/README.md`) explaining the three
  layers: Cloudflare edge → nginx/traefik → app guard, and that the app guard is
  the last-resort backstop when the proxy chain is bypassed or misconfigured.

### 8. Tests

- `proxy/rpm_limiter_test.go`: window roll resets count; cap enforced at N+1;
  per-IP isolation (IP A hitting its cap doesn't block IP B); `limit<=0` always
  allows; `sweep` drops stale windows.
- `proxy/ip_limiter_test.go`: acquire up to per-IP cap then reject with
  `concReasonIP`; release frees a slot; global cap rejects with `concReasonGlobal`
  and is checked before per-IP; `limit<=0` disables each check; map entry deleted
  when count hits zero.
- `proxy/dos_guard_test.go`: `loadDosConfig` parses each env var (including
  negative/garbage → 0 and the TrustProxy truthiness rule); disabled guard is a
  passthrough returning a non-nil no-op release; each cap produces the right
  status (429 RPM, 429 per-IP conc, 503 global); `key` honors `TrustProxy`
  (header vs RemoteAddr). Env vars set via `t.Setenv`.

## Out of scope

- Token-bucket / burst-refill limiting (fixed window matches the existing limiter).
- Persisted or admin-tunable limits (ENV only by decision).
- Geo/ASN blocking, per-route differentiated limits, IP ban lists.
- Changes to the existing per-key `rateLimiter` or per-key IP caps.
