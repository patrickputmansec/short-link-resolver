# Short-Link Resolver

Small HTTP service that resolves a short code to a URL. Built for the Chronosphere take-home; the point is the instrumentation, not the resolver.

## Prerequisites

Go 1.23+ and Docker.

## Run

```sh
go run .
```

Listens on `:8080`. In another terminal:

```sh
docker compose up -d
```

Prometheus is at <http://localhost:9090>. The `resolver` target should be UP after a scrape or two.

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
| Counter | `resolver_resolutions_total` | — |
| Counter | `resolver_errors_total` | `reason` |
| Gauge | `resolver_inflight_requests` | — |
| Log | structured JSON via `slog` | — |

Errors are counted from inside the handler with a typed reason, not derived from HTTP status codes. Histogram buckets start at 5µs because lookups are sub-millisecond and the default Prometheus buckets (starting at 5ms) would collapse everything into the smallest one.

## Queries

The two essential queries per the assignment are the p95 latency under §1 and the top error reasons under §3. The rest are here so each instrumentation point is demoable on its own.

### 1. Histogram (latency)

p95 in ms:

```promql
1000 * histogram_quantile(
  0.95,
  sum by (le) (rate(resolver_request_duration_seconds_bucket{result="success"}[5m]))
)
```

Mean in ms, computed from `_sum / _count`. Bucket-independent, so it's the right query for sanity-checking that the histogram is recording what you'd expect:

```promql
1000 * (
  rate(resolver_request_duration_seconds_sum{result="success"}[5m])
  /
  rate(resolver_request_duration_seconds_count{result="success"}[5m])
)
```

Raw bucket counts (Table view shows one row per `le`):

```promql
resolver_request_duration_seconds_bucket{result="success"}
```

### 2. Success counter

Trigger one (resolves to `https://prometheus.io`):

```sh
curl -s 'localhost:8080/resolve?code=prom01'
```

Successes per second over the last minute:

```promql
rate(resolver_resolutions_total[1m])
```

Lifetime total (Table view):

```promql
resolver_resolutions_total
```

### 3. Error counter

Trigger `not_found` (well-formed but unseeded):

```sh
curl -s 'localhost:8080/resolve?code=prom03'
```

Trigger `bad_format` (regex rejects non-alphanumeric):

```sh
curl -s 'localhost:8080/resolve?code=abc!1'
```

Trigger `bad_format` (regex rejects wrong length):

```sh
curl -s 'localhost:8080/resolve?code=toolong'
```

Trigger `missing_code` (no code param):

```sh
curl -s 'localhost:8080/resolve'
```

Top-3 most active error reasons:

```promql
topk(3, sum by (reason) (rate(resolver_errors_total[5m])))
```

Errors per second by reason:

```promql
sum by (reason) (rate(resolver_errors_total[1m]))
```

A single reason in isolation:

```promql
rate(resolver_errors_total{reason="not_found"}[1m])
```

### 4. Gauge (inflight requests)

```promql
resolver_inflight_requests
```

Mostly reads 0 because lookups are sub-millisecond. To make it visibly spike, fire a parallel burst with a delay so requests overlap:

```sh
for i in {1..20}; do
  curl -s -o /dev/null 'localhost:8080/resolve?code=prom01&delay=200ms' &
done
```

### 5. Log line

Not a PromQL query — `slog` writes JSON to stdout, so look at the terminal running `go run .`. Each request emits one line:

```json
{"time":"2026-05-08T03:24:33Z","level":"INFO","msg":"resolve","code":"prom01","result":"success","target_url":"https://prometheus.io","duration_ms":0}
```

Pipe through `jq` for pretty-printing: `go run . | jq .`. In production this would ship to a log aggregator (Loki, Datadog, Splunk) and be queried like a database.

## Verifying the histogram

The handler accepts an optional `delay` param (anything `time.ParseDuration` accepts: `50ms`, `200ms`, `2s`) that sleeps inside the timed window. Single curls are enough to see the histogram do its job — no rolling windows, no sustained traffic. Just look at the cumulative counters before and after each request.

In the Prometheus UI, switch to **Table view** and run:

```promql
resolver_request_duration_seconds_bucket{result="success"}
```

Note the current bucket counts. Then fire one slow request:

```sh
curl -s 'localhost:8080/resolve?code=prom01&delay=200ms'
```

Re-execute the query. Every bucket with `le >= 0.5` increments by exactly 1 (because 200ms ≤ 500ms, 1s, and +Inf). Buckets with `le <= 0.1` are unchanged (because 200ms > 100ms). That cumulative pattern — observation lands in the smallest matching bucket and ripples up — is how `histogram_quantile` later interpolates percentiles.

You can also watch the bookkeeping counters:

```promql
resolver_request_duration_seconds_count{result="success"}
resolver_request_duration_seconds_sum{result="success"}
```

`_count` increments by 1 with each curl. `_sum` grows by the injected delay in seconds (~0.2 for `delay=200ms`). The mean is computed from these two — `_sum / _count`.

Try `delay=2s` next:

```sh
curl -s 'localhost:8080/resolve?code=prom01&delay=2s'
```

Only `le="+Inf"` increments, because 2s exceeds every finite bucket. That's the failure mode the `+Inf` bucket exists to catch.

Bump the delay (`500ms`, `2s`) to confirm both queries respond at multiple scales.

## Stop

```sh
docker compose down
```

Ctrl-C the other two terminals.
