# geminitracker Exporter

A Go-based Prometheus exporter that scrapes your upload/download totals from [gemini-tracker.org](https://gemini-tracker.org) (G3MINI TR4CK3R, a UNIT3D tracker) and exposes them for Prometheus.

- Logs in with the site's web form (CSRF + honeypot fields), keeps the session cookie
- Reads `/users/<username>` and parses the ratio bar (Uploaded / Downloaded)
- Re-logs in automatically when the session expires
- Exposes `/metrics` and `/health`

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `geminitracker_total_uploaded_bytes` | Gauge | Total bytes uploaded |
| `geminitracker_total_downloaded_bytes` | Gauge | Total bytes downloaded |

The site shows human-readable sizes (`65.67 GiB`, 2 decimals, 1024-based); they are converted to bytes, so precision is limited to the displayed rounding.

## Configuration

```bash
GEMINITRACKER_BASE_URL=https://gemini-tracker.org
GEMINITRACKER_USERNAME=<your_username>
GEMINITRACKER_PASSWORD=<your_password>
PORT=9090
METRICS_PATH=/metrics
SCRAPE_INTERVAL=5m
```

| Variable | Description | Default |
|----------|-------------|---------|
| `GEMINITRACKER_BASE_URL` | Site base URL | `https://gemini-tracker.org` |
| `GEMINITRACKER_USERNAME` | Site username | *required* |
| `GEMINITRACKER_PASSWORD` | Site password | *required* |
| `PORT` | Listen port | `9090` |
| `METRICS_PATH` | Metrics path | `/metrics` |
| `SCRAPE_INTERVAL` | Minimum delay between upstream fetches | `5m` |

> The account must not require 2FA at login: the exporter cannot answer a TOTP challenge.

## Run

```bash
go build -o geminitracker_exporter . && ./geminitracker_exporter
# or
docker compose -f docker-compose.dev.yml up --build   # host port EXPORTER_PORT (default 9102)
curl http://localhost:9102/metrics
```

## Authentication Flow

1. `GET /login`: collect `_token` (CSRF), `_captcha`, and the honeypot fields (`_username` empty, random-named timestamp field)
2. Wait a few seconds (honeypot rejects instant submissions)
3. `POST /login` (form-urlencoded: `username`, `password`, `remember`, hidden fields). A 302 to anything but `/login` means success
4. `GET /users/<username>`: if redirected to `/login`, the session expired and the exporter logs in again

No Cloudflare bypass / FlareSolverr needed: plain HTTP with a browser User-Agent works.

## Prometheus

```yaml
scrape_configs:
  - job_name: 'geminitracker_exporter'
    static_configs:
      - targets: ['localhost:9090']
```

## License

[MIT](./LICENSE)
