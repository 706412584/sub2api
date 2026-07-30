# Sub2API Docker Image

Sub2API is an AI API Gateway Platform for distributing and managing AI product subscription API quotas.

## Quick Start

```bash
docker run -d \
  --name sub2api \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/sub2api" \
  -e REDIS_URL="redis://host:6379" \
  weishaw/sub2api:latest
```

## Docker Compose

```yaml
version: '3.8'

services:
  sub2api:
    image: weishaw/sub2api:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://postgres:postgres@db:5432/sub2api?sslmode=disable
      - REDIS_URL=redis://redis:6379
    depends_on:
      - db
      - redis

  db:
    image: postgres:15-alpine
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=sub2api
    volumes:
      - postgres_data:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    volumes:
      - redis_data:/data

volumes:
  postgres_data:
  redis_data:
```

## Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `DATABASE_URL` | PostgreSQL connection string | Yes | - |
| `REDIS_URL` | Redis connection string | Yes | - |
| `PORT` | Server port | No | `8080` |
| `GIN_MODE` | Gin framework mode (`debug`/`release`) | No | `release` |
| `PROXY_SUBSCRIPTION_MIHOMO_BINARY` | Path to mihomo for embedded airport subscriptions | No | `/usr/local/bin/mihomo` (official images) |
| `PROXY_SUBSCRIPTION_DATA_DIR` | Per-source mihomo config/workdir | No | `/app/data/proxy-subscriptions` |
| `PROXY_SUBSCRIPTION_ALLOW_INSECURE` | Allow non-loopback `http://` subscription URLs | No | `false` |
| `PROXY_SUBSCRIPTION_ALLOW_NON_LOCAL_BIND` | Allow non-loopback mixed bind | No | `false` |
| `PROXY_SUBSCRIPTION_RUNNER_INTERVAL_SEC` | Background due-scan interval | No | `30` |

See `.env.example` and `docker-compose*.yml` for the full compose-oriented variable set.

## Proxy subscription (mihomo)

Official release images **bundle mihomo** (currently pinned at build time, e.g. MetaCubeX `v1.19.29`) at `/usr/local/bin/mihomo`, with:

- `PROXY_SUBSCRIPTION_MIHOMO_BINARY=/usr/local/bin/mihomo`
- `PROXY_SUBSCRIPTION_DATA_DIR=/app/data/proxy-subscriptions` (under the existing `/app/data` volume)

### How to use

1. Deploy **one** `sub2api` container (do not scale replicas for this feature).
2. Open Admin → IP / Proxies → subscription panel.
3. Create a source (HTTPS URL or paste body) → **Sync**.
4. Confirm engine status shows binary found and running sources ≥ 1.
5. `sidecar-*` proxies appear; **manually** bind accounts (no auto-bind).

Mixed listeners bind **container loopback** (`127.0.0.1`). The gateway and mihomo must share the same container/network namespace. You normally **do not** publish those ports on the host.

### Limits

- **Single replica only** for subscription mode: DB rows point at `127.0.0.1:<port>`; other replicas cannot use the leader’s listeners.
- Leader lock avoids double mihomo start, but does **not** make multi-replica traffic correct.
- Account `proxy_id` is still manual.
- Independent `subscription-sidecar` can still run alongside with a different `sidecar-` name prefix.

### Troubleshooting

| Symptom | Check |
|---------|--------|
| Engine missing binary | Image too old / custom build without mihomo; set `PROXY_SUBSCRIPTION_MIHOMO_BINARY` or mount a linux static binary |
| Engine idle (0 sources) | No successful sync yet, or process just restarted — run **Sync** or wait for due interval |
| Sync error | `last_sync_error` on the source; subscription fetch/parse; port conflict; binary not executable |
| Proxies exist but traffic fails | Multi-replica or proxy host not loopback on the instance handling the request |

### Advanced: bring your own mihomo

```yaml
volumes:
  - ./mihomo-linux:/usr/local/bin/mihomo:ro
  - sub2api_data:/app/data
environment:
  - PROXY_SUBSCRIPTION_MIHOMO_BINARY=/usr/local/bin/mihomo
```

Binary must be **linux** and match container arch (`amd64`/`arm64`).

### Bare metal

Install `mihomo` on `PATH`, or set `PROXY_SUBSCRIPTION_MIHOMO_BINARY` to an absolute path. Data defaults to `data/proxy-subscriptions` under the process working directory / `DATA_DIR`.

mihomo is a third-party project ([MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo)); see its license/NOTICE when redistributing images.

## Supported Architectures

- `linux/amd64`
- `linux/arm64`

## Tags

- `latest` - Latest stable release
- `x.y.z` - Specific version
- `x.y` - Latest patch of minor version
- `x` - Latest minor of major version

## Links

- [GitHub Repository](https://github.com/weishaw/sub2api)
- [Documentation](https://github.com/weishaw/sub2api#readme)
- [Deployment README](./README.md)
