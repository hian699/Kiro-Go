# Deployment: layered DoS protection

Three layers sit in front of Kiro-Go, outermost first:

1. **Cloudflare edge** — DDoS absorption, WAF, and dashboard rate-limiting
   rules. Propagates the real client IP in `CF-Connecting-IP`.
2. **Reverse proxy (nginx or traefik)** — per-IP request-rate and
   connection-count limits close to the origin. Must forward the client IP
   (`CF-Connecting-IP` / `X-Forwarded-For`) so the app can key on it.
3. **App-layer DoS guard (built in)** — the last-resort backstop, enforced in
   the process itself so limits still apply if the proxy chain is bypassed or
   misconfigured. Configured by env vars, disabled by default:

   | ENV var | Meaning | Default |
   |---|---|---|
   | `DOS_IP_RPM` | max requests/min per client IP | `0` (off) |
   | `DOS_IP_CONCURRENCY` | max concurrent in-flight requests per IP | `0` (off) |
   | `DOS_MAX_CONCURRENCY` | max concurrent in-flight requests globally | `0` (off) |
   | `DOS_TRUST_PROXY_HEADERS` | trust `CF-Connecting-IP`/`X-Forwarded-For`/`X-Real-IP` for keying; set `false` if the app is exposed directly | `true` |

   The app resolves the client IP as: `CF-Connecting-IP` → first
   `X-Forwarded-For` element → `X-Real-IP` → `RemoteAddr`. Set
   `DOS_TRUST_PROXY_HEADERS=false` only when nothing trusted sits in front,
   otherwise a forged `X-Forwarded-For` lets an attacker mint a fresh key per
   request and bypass the per-IP caps.

The reference configs in this directory (`nginx.conf`, `traefik.md`,
`cloudflared.yml`) are starting points, not wired into the build or
`docker-compose.yml`. Tune the numbers to your traffic.
