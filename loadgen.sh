#!/bin/bash
# Generates a weighted mix of valid and invalid /resolve traffic.
# Run alongside the service to give the Prometheus UI something to graph.
# Ctrl-C to stop.

set -u
URL="${URL:-http://localhost:8080/resolve}"

# A handful of "hot" valid codes (subset of the 20 seeded in main.go).
HOT=(prom01 chrn01 gthb01 glng01 dckr01)

# Malformed slugs that fail the regex.
BAD=("abc!1" "12345" "toolongslug" "ABCDE_")

# Well-formed but unseeded codes (regex passes, lookup misses).
MISS=(zzzzzz aaaaaa qqqqqq)

pick_from() {
  local arr=("$@")
  echo "${arr[RANDOM % ${#arr[@]}]}"
}

iter=0
while :; do
  iter=$((iter + 1))

  # Every ~30 iterations, fire a small parallel burst so the
  # inflight_requests gauge produces a visible spike.
  if (( iter % 30 == 0 )); then
    echo "[loadgen] burst"
    for _ in {1..20}; do
      curl -s -o /dev/null "$URL?code=prom01" &
    done
    wait
    continue
  fi

  # Weighted pick: 1-70 success, 71-85 bad_format, 86-95 not_found, 96-100 missing_code.
  pick=$((RANDOM % 100 + 1))

  if   (( pick <= 70 )); then curl -s -o /dev/null "$URL?code=$(pick_from "${HOT[@]}")"
  elif (( pick <= 85 )); then curl -s -o /dev/null "$URL?code=$(pick_from "${BAD[@]}")"
  elif (( pick <= 95 )); then curl -s -o /dev/null "$URL?code=$(pick_from "${MISS[@]}")"
  else                        curl -s -o /dev/null "$URL"
  fi

  # Sleep 50-150ms between requests for realistic pacing.
  sleep "0.$(printf '%03d' $((RANDOM % 100 + 50)))"
done
