#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The lab on one machine, without a cluster: the orchestrator, the parcels service and the
# parcels team's dispatcher, as processes, on the lab's ports. Enough for the features not tagged
# @lab (no Kafka, Postgres or Prometheus):
#
#   examples/lab/scripts/local.sh                 # until Ctrl-C
#   cd examples/acceptance && axx run --tags 'not @lab'
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
examples=$(cd "$here/../.." && pwd)
work=${STORMBREAKER_LAB_DIR:-$(mktemp -d)}
mkdir -p "$work"

echo "lab: building into $work" >&2
(cd "$examples/.." && go build -o "$work/stormbreaker" ./cmd/stormbreaker)
(cd "$examples" && go build -o "$work/parcels" ./parcels && go build -o "$work/parcels-dispatcher" ./dispatchers/parcels)

pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

STORMBREAKER_ADDR=127.0.0.1:18080 STORMBREAKER_DB="$work/orchestrator.db" "$work/stormbreaker" orchestrator serve 2>"$work/orchestrator.log" &
pids+=($!)
ADDR=127.0.0.1:18081 REGION=local "$work/parcels" 2>"$work/parcels.log" &
pids+=($!)
for _ in $(seq 50); do curl -fs http://127.0.0.1:18080/healthz >/dev/null 2>&1 && break; sleep 0.1; done

STORMBREAKER_DISPATCHER=parcels STORMBREAKER_INSTANCE=local STORMBREAKER_TEAM=parcels \
  STORMBREAKER_CALLBACK_URL=http://127.0.0.1:18090/events STORMBREAKER_ADDR=127.0.0.1:18090 \
  STORMBREAKER_ORCHESTRATOR=http://127.0.0.1:18080 STORMBREAKER_GROUPS=delivery STORMBREAKER_REASSERT=2s \
  PARCELS_ADMIN_URL=http://127.0.0.1:18081 "$work/parcels-dispatcher" 2>"$work/parcels-dispatcher.log" &
pids+=($!)

echo "lab: orchestrator http://127.0.0.1:18080, parcels http://127.0.0.1:18081, its dispatcher http://127.0.0.1:18090; logs in $work" >&2
wait
