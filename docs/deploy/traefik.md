# Traefik reference: rate + in-flight middlewares fronting Kiro-Go

## Dynamic configuration (file provider)

```yaml
# dynamic.yml
http:
  middlewares:
    kiro-ratelimit:
      rateLimit:
        average: 60      # requests per period per source IP
        period: 1m
        burst: 20
    kiro-inflight:
      inFlightReq:
        amount: 10       # max concurrent requests per source IP
        sourceCriterion:
          ipStrategy:
            depth: 1     # trust the first XFF hop (the Cloudflare edge)
  routers:
    kiro:
      rule: "PathPrefix(`/`)"
      service: kiro
      middlewares:
        - kiro-ratelimit
        - kiro-inflight
  services:
    kiro:
      loadBalancer:
        servers:
          - url: "http://kiro-go:8080"
```

Static config must trust the proxy in front so the client IP is read from the
forwarded headers:

```yaml
# traefik.yml (static)
entryPoints:
  web:
    address: ":80"
    forwardedHeaders:
      trustedIPs:
        - "173.245.48.0/20"   # Cloudflare ranges; keep current
        - "103.21.244.0/22"
```

## Docker labels (compose)

```yaml
labels:
  - "traefik.http.middlewares.kiro-ratelimit.ratelimit.average=60"
  - "traefik.http.middlewares.kiro-ratelimit.ratelimit.period=1m"
  - "traefik.http.middlewares.kiro-ratelimit.ratelimit.burst=20"
  - "traefik.http.middlewares.kiro-inflight.inflightreq.amount=10"
  - "traefik.http.routers.kiro.middlewares=kiro-ratelimit,kiro-inflight"
```
