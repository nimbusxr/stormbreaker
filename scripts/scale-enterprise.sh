#!/usr/bin/env bash
# Runs the enterprise scale test (internal/scale/enterprise_test.go) the way its numbers are
# quoted: the orchestrator in a Linux container of its own, limited as a modest pod is, on a
# volume of its own, and the environment's dispatchers and suites in another container, reaching
# it over the network. It reports the orchestrator's CPU and memory as Docker saw them, and what
# one commit costs on its volume.
#
#   scripts/scale-enterprise.sh                         # the reference enterprise's peak hour
#   ORCH_CPUS=4 STORMBREAKER_SCALE_SMOKE_RATE=80 scripts/scale-enterprise.sh
#
# Every STORMBREAKER_SCALE_* variable in the environment is passed to the test (see its doc).
set -euo pipefail
cd "$(dirname "$0")/.."

ORCH_CPUS=${ORCH_CPUS:-2}
ORCH_MEMORY=${ORCH_MEMORY:-2g}
# Sized for the largest run, as an operator sizes it: the load run's 60 million identities.
ORCH_ARGS=${ORCH_ARGS:---readiness-timeout 15m}
BASE=${BASE:-alpine:3.22}
OUT=${OUT:-$(mktemp -d -t sb-enterprise)}
case "$(docker info --format '{{.Architecture}}')" in
  aarch64 | arm64) GOARCH=arm64 ;;
  *) GOARCH=amd64 ;;
esac

echo "scale-enterprise: building for linux/$GOARCH into $OUT"
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -o "$OUT/stormbreaker" ./cmd/stormbreaker
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go test -c -tags scale -o "$OUT/scale.test" ./internal/scale
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go test -c -o "$OUT/orchestrator.test" ./internal/orchestrator

STATS=""
cleanup() {
  if [ -n "$STATS" ]; then kill "$STATS" 2>/dev/null || true; wait "$STATS" 2>/dev/null || true; fi
  docker rm -f sb-orchestrator sb-harness >/dev/null 2>&1 || true
  docker volume rm sb-scale-db >/dev/null 2>&1 || true
  docker network rm sb-scale >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
docker network create sb-scale >/dev/null
docker volume create sb-scale-db >/dev/null

echo "scale-enterprise: one commit on the orchestrator's volume"
docker run --rm -v "$OUT:/sb:ro" -v sb-scale-db:/data -e TMPDIR=/data "$BASE" \
  /sb/orchestrator.test -test.run xxx -test.bench 'Commit/FULL' -test.benchtime 2000x | tee "$OUT/commit.txt" | grep -E 'Benchmark'

docker run -d --name sb-orchestrator --network sb-scale --cpus "$ORCH_CPUS" --memory "$ORCH_MEMORY" \
  --ulimit nofile=1048576:1048576 -v "$OUT:/sb:ro" -v sb-scale-db:/data "$BASE" \
  /sb/stormbreaker orchestrator serve --addr :8080 --db /data/orchestrator.db --token tok $ORCH_ARGS >/dev/null
for _ in $(seq 1 60); do
  docker exec sb-orchestrator wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break
  sleep 1
done

# The orchestrator's CPU (percent of one core) and memory, every few seconds: Docker's view, and
# the cgroup's split of it into the process's own memory (anon) and the page cache (file), which
# the kernel takes back under pressure.
(while true; do
  s=$(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' sb-orchestrator 2>/dev/null) || break
  m=$(docker exec sb-orchestrator awk '$1 == "anon" || $1 == "file" { printf "%s ", $2 }' /sys/fs/cgroup/memory.stat 2>/dev/null)
  echo "$s $m"
done) >"$OUT/stats.txt" &
STATS=$!

pass=()
while IFS= read -r v; do pass+=(-e "$v"); done < <(env | grep '^STORMBREAKER_SCALE_' || true)
echo "scale-enterprise: the orchestrator has $ORCH_CPUS CPUs and $ORCH_MEMORY; the peak runs now"
set +e
docker run --name sb-harness --network sb-scale --sysctl net.ipv4.ip_local_port_range="1024 65535" \
  --ulimit nofile=1048576:1048576 -v "$OUT:/sb:ro" \
  -e STORMBREAKER_SCALE_ORCHESTRATOR=http://sb-orchestrator:8080 -e STORMBREAKER_SCALE_LISTEN=0.0.0.0 \
  -e STORMBREAKER_SCALE_ADVERTISE=sb-harness ${pass[@]+"${pass[@]}"} "$BASE" \
  /sb/scale.test -test.run EnterprisePeak -test.v -test.timeout 180m 2>&1 | tee "$OUT/test.log"
status=${PIPESTATUS[0]}
set -e
kill "$STATS" 2>/dev/null || true
wait "$STATS" 2>/dev/null || true
STATS=""

echo "scale-enterprise: the orchestrator's container, as Docker saw it"
awk '{ cpu = $1; sub(/%/, "", cpu); if (cpu + 0 > maxcpu) maxcpu = cpu + 0; sum += cpu; n++;
       mem = $2; v = mem + 0; if (mem ~ /GiB/) v *= 1024; else if (mem ~ /KiB/) v /= 1024; if (v > maxmem) maxmem = v;
       if ($5 / 1048576 > maxanon) maxanon = $5 / 1048576; if ($6 / 1048576 > maxfile) maxfile = $6 / 1048576 }
     END { if (n) printf "  CPU    %.0f%% of a core on average, %.0f%% at most\n  memory %.0f MiB at most, as Docker counts it\n  of which the process at most %.0f MiB, and the page cache at most %.0f MiB\n", sum / n, maxcpu, maxmem, maxanon, maxfile }' "$OUT/stats.txt"
docker exec sb-orchestrator du -sh /data 2>/dev/null | awk '{ print "  volume " $1 }' || true
echo "scale-enterprise: logs in $OUT"
exit "$status"
