#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The lab on a kind cluster: the orchestrator, the parcels service in two regions with a dispatcher
# each, the tracking service with Postgres and its dispatcher, Kafka, a Prometheus per region, and
# the courier mocked.
# Every kubectl call pins the kind-stormbreaker context.
#   examples/lab/scripts/up.sh
#   cd examples/acceptance && axx run
#   examples/lab/scripts/down.sh
set -euo pipefail
lab=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "$lab/../.." && pwd)
ctx=kind-stormbreaker
for tool in kind kubectl docker go; do command -v "$tool" >/dev/null || { echo "lab: $tool is required" >&2; exit 1; }; done

kind get clusters | grep -qx stormbreaker || kind create cluster --name stormbreaker --config "$lab/kind.yaml"
arch=$(docker info --format '{{.Architecture}}')
case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
echo "lab: building for linux/$arch" >&2
mkdir -p "$work/orchestrator/linux/$arch" "$work/lab/bin"
(cd "$root" && GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -trimpath -o "$work/orchestrator/linux/$arch/stormbreaker" ./cmd/stormbreaker)
for cmd in parcels tracking dispatchers/parcels dispatchers/tracking; do
  name=$(echo "$cmd" | sed 's#dispatchers/\(.*\)#\1-dispatcher#')
  (cd "$root/examples" && GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -trimpath -o "$work/lab/bin/$name" "./$cmd")
done
docker build -q --platform "linux/$arch" -f "$root/build/orchestrator.Dockerfile" -t ghcr.io/nimbusxr/stormbreaker-orchestrator:dev "$work/orchestrator"
docker build -q --platform "linux/$arch" -f "$lab/Dockerfile" -t stormbreaker-lab:dev "$work/lab"
docker build -q -t stormbreaker-lab-courier-mock:dev "$lab/mocks/courier"
for image in ghcr.io/nimbusxr/stormbreaker-orchestrator:dev stormbreaker-lab:dev stormbreaker-lab-courier-mock:dev; do
  kind load docker-image --name stormbreaker "$image"
done

kubectl --context "$ctx" apply -f "$lab/k8s/"
# The lab's own images keep their :dev tag: what runs them restarts, to run what was just built.
kubectl --context "$ctx" -n stormbreaker-lab rollout restart deploy/orchestrator deploy/courier deploy/parcels-east deploy/parcels-west \
  deploy/tracking deploy/parcels-dispatcher-east deploy/parcels-dispatcher-west deploy/tracking-dispatcher >/dev/null
for d in orchestrator kafka schema-registry postgres courier parcels-east parcels-west tracking \
  parcels-dispatcher-east parcels-dispatcher-west tracking-dispatcher prometheus-east prometheus-west; do
  kubectl --context "$ctx" -n stormbreaker-lab rollout status "deploy/$d" --timeout=180s
done
echo "lab: up. orchestrator http://localhost:18080, parcels http://localhost:18081" >&2
