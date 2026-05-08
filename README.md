# Short-Link Resolver

Small HTTP service that maps a 6-char code to a URL. Built for the Chronosphere take-home; the metrics are the point.

## Prerequisites

Go 1.23+ and Docker.

## Run

```sh
go run .
```

Service runs on `:8080`.

```sh
docker compose up -d
```

Prometheus is at <http://localhost:9090>. The `resolver` target should show UP after a scrape or two.

## Endpoints

| Request | Result |
|---|---|
| `curl 'localhost:8080/resolve?code=prom01'` | 200, `https://prometheus.io` |
| `curl 'localhost:8080/resolve?code=zzzzzz'` | 404, `not_found` |
| `curl 'localhost:8080/resolve?code=abc!1'` | 400, `bad_format` |
| `curl 'localhost:8080/resolve'` | 400, `missing_code` |
| `curl 'localhost:8080/healthz'` | 200 |
| `curl 'localhost:8080/metrics'` | Prometheus exposition |

## Instrumentation

| Type | Name | Labels |
|---|---|---|
| Histogram | `resolver_request_duration_seconds` | `result` |
| Counter | `resolver_resolutions_total` | |
| Counter | `resolver_errors_total` | `reason` |
| Gauge | `resolver_inflight_requests` | |
| Log | structured JSON via `slog` | |

Errors are counted inside the handler with a typed reason rather than derived from HTTP status codes. Histogram buckets start at 5µs since lookups are sub-millisecond; the default buckets start at 5ms and would just collapse everything into the smallest one.

## Queries

The two queries the assignment asks for are the p95 latency under Histogram and the top errors under Error counter. The rest cover the other metric types.

### Histogram

p95 in ms:

```promql
1000 * histogram_quantile(
  0.95,
  sum by (le) (rate(resolver_request_duration_seconds_bucket{result="success"}[5m]))
)
```

Mean in ms, computed from `_sum / _count` (no bucket math):

```promql
1000 * (
  rate(resolver_request_duration_seconds_sum{result="success"}[5m])
  /
  rate(resolver_request_duration_seconds_count{result="success"}[5m])
)
```

Raw bucket counts:

```promql
resolver_request_duration_seconds_bucket{result="success"}
```

### Success counter

A successful curl:

```sh
curl -s 'localhost:8080/resolve?code=prom01'
```

Per-second rate:

```promql
rate(resolver_resolutions_total[1m])
```

Lifetime total:

```promql
resolver_resolutions_total
```

### Error counter

Trigger curls, one per reason.

`not_found`:

```sh
curl -s 'localhost:8080/resolve?code=prom03'
```

`bad_format` from invalid chars:

```sh
curl -s 'localhost:8080/resolve?code=abc!1'
```

`bad_format` from wrong length:

```sh
curl -s 'localhost:8080/resolve?code=toolong'
```

`missing_code`:

```sh
curl -s 'localhost:8080/resolve'
```

Top three by rate:

```promql
topk(3, sum by (reason) (rate(resolver_errors_total[5m])))
```

Errors per second by reason:

```promql
sum by (reason) (rate(resolver_errors_total[1m]))
```

One reason on its own:

```promql
rate(resolver_errors_total{reason="not_found"}[1m])
```

### Gauge

```promql
resolver_inflight_requests
```

Sits at zero most of the time. To get it to spike, fire 20 parallel curls with a delay so they overlap:

```sh
for i in {1..20}; do
  curl -s -o /dev/null 'localhost:8080/resolve?code=prom01&delay=200ms' &
done
```

### Log line

Not a Prometheus query. `slog` writes JSON to stdout, so the `go run .` terminal shows one line per request:

```json
{"time":"2026-05-08T03:24:33Z","level":"INFO","msg":"resolve","code":"prom01","result":"success","target_url":"https://prometheus.io","duration_ms":0}
```

Pretty-printed: `go run . | jq .`. In production these would ship to Loki, Splunk, or whatever the team uses.

## Verifying the histogram

The handler accepts an optional `delay` param that sleeps inside the timed section. Anything `time.ParseDuration` accepts works (`50ms`, `200ms`, `2s`). Single curls are enough.

Look at the bucket counts in Table view:

```promql
resolver_request_duration_seconds_bucket{result="success"}
```

Note the values, then:

```sh
curl -s 'localhost:8080/resolve?code=prom01&delay=200ms'
```

Re-run the query. Every bucket with `le >= 0.5` is up by 1 (200ms fits in 500ms, 1s, and +Inf). Buckets with `le <= 0.1` are unchanged (200ms doesn't fit in 100ms). That's the cumulative layout `histogram_quantile` reads when computing percentiles.

The bookkeeping counters also move:

```promql
resolver_request_duration_seconds_count{result="success"}
resolver_request_duration_seconds_sum{result="success"}
```

`_count` ticks up by 1, `_sum` by the injected delay in seconds (about 0.2 for `delay=200ms`). The mean is derived from these.

Try a delay that overflows:

```sh
curl -s 'localhost:8080/resolve?code=prom01&delay=2s'
```

Only `le="+Inf"` increments since 2s exceeds every finite bucket. That's what `+Inf` is for.

## Stop

```sh
docker compose down
```

Then Ctrl-C the other terminals.
