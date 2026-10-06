# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Summary
Go Prometheus exporter for gemini-tracker.org (UNIT3D). Gin + prometheus client. Plain HTTP, no FlareSolverr.

## Auth
- `GET /login` -> parse `_token`, `_captcha`, honeypot (`_username` empty + random-named timestamp input)
- sleep ~4s, `POST /login` (username, password, remember, hidden fields); 302 not to `/login` = OK
- cookie jar keeps `XSRF-TOKEN` + `g3mini_tr4ck3r_session` (2h lifetime)
- `FetchMetrics` re-logins once if `/users/<username>` redirects to `/login`

## Metrics
`GET /users/<username>` -> HTML `.ratio-bar__uploaded` / `.ratio-bar__downloaded` (`50 GiB`, 1024-based) -> bytes.
Gauges: `geminitracker_total_uploaded_bytes`, `geminitracker_total_downloaded_bytes`.

## Config
`GEMINITRACKER_BASE_URL`, `GEMINITRACKER_USERNAME`, `GEMINITRACKER_PASSWORD`, `PORT`, `METRICS_PATH`, `SCRAPE_INTERVAL` (min delay between upstream fetches).

## Commands
```bash
docker compose -f docker-compose.dev.yml up --build   # host port 9102
curl http://localhost:9102/metrics
curl http://localhost:9102/health
```
