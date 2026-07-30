#!/usr/bin/env bash
# PROTOTYPE: one-command 1,000-Node scale experiment for GitHub issue #4.
set -euo pipefail

PROTOTYPE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_DIR="${PROTOTYPE_DIR}/.results"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
RUN_DIR="${RESULTS_DIR}/${RUN_ID}"
mkdir -p "${RUN_DIR}"
TEMP_DIR="$(cd "$(mktemp -d "${RESULTS_DIR}/tmp.${RUN_ID}.XXXXXX")" && pwd -P)"

CLUSTER_NAME="kas-scale-4-$$"
KUBECONFIG_PATH="${TEMP_DIR}/kubeconfig"
KIND_CONTEXT="kind-${CLUSTER_NAME}"
KIND_BIN="${TEMP_DIR}/kind"
DRIVER_BIN="${TEMP_DIR}/scale-driver"
PROXY_PORT=$((18000 + ($$ % 10000)))
PROXY_URL="http://127.0.0.1:${PROXY_PORT}"
PROXY_PID=""
STATS_PID=""

KIND_VERSION="v0.32.0"
KIND_SHA256_DARWIN_AMD64="295ac6d0d634c9819c9907df45e3017d1f13166bd13c3404c45e79f7faa47498"
KIND_NODE_IMAGE="kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"
KWOK_VERSION="v0.8.0"
KWOK_MANIFEST_SHA256="a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405"
KWOK_STAGE_SHA256="2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24"

NODE_COUNT="${KAS_SCALE_NODE_COUNT:-1000}"
ACCELERATORS_PER_NODE=8
ACCELERATOR_COUNT=$((NODE_COUNT * ACCELERATORS_PER_NODE))
HEALTH_SAMPLE="${KAS_SCALE_HEALTH_SAMPLE:-100}"
WORKLOAD_COUNT="${KAS_SCALE_WORKLOAD_COUNT:-100}"
CONCURRENCY="${KAS_SCALE_CONCURRENCY:-32}"

exec > >(tee "${RUN_DIR}/run.log") 2>&1

step() {
  printf '\n[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"
}

fail() {
  printf 'FAILED: %s\n' "$*" >&2
  exit 1
}

k() {
  kubectl --kubeconfig "${KUBECONFIG_PATH}" --context "${KIND_CONTEXT}" "$@"
}

driver() {
  "${DRIVER_BIN}" -base-url "${PROXY_URL}" -concurrency "${CONCURRENCY}" "$@" |
    tee -a "${RUN_DIR}/events.jsonl"
}

sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

snapshot_resources() {
  phase="$1"
  docker stats --no-stream --format '{{json .}}' \
    "${CLUSTER_NAME}-control-plane" |
    jq -c --arg phase "${phase}" --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      '. + {phase: $phase, capturedAt: $timestamp}' \
      >>"${RUN_DIR}/docker-stats.jsonl"
  docker exec "${CLUSTER_NAME}-control-plane" \
    du -k /var/lib/etcd/member/snap/db |
    awk -v phase="${phase}" '{print phase "\t" $1}' \
      >>"${RUN_DIR}/etcd-db-kib.tsv"
}

sample_resources() {
  while docker inspect "${CLUSTER_NAME}-control-plane" >/dev/null 2>&1; do
    docker stats --no-stream --format '{{json .}}' \
      "${CLUSTER_NAME}-control-plane" |
      jq -c --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '. + {capturedAt: $timestamp}' \
        >>"${RUN_DIR}/docker-stats-samples.jsonl" || true
    sleep 2
  done
}

cleanup() {
  status=$?
  set +e
  if [[ -n "${STATS_PID}" ]]; then
    kill "${STATS_PID}" >/dev/null 2>&1
    wait "${STATS_PID}" >/dev/null 2>&1
  fi
  if [[ -n "${PROXY_PID}" ]]; then
    kill "${PROXY_PID}" >/dev/null 2>&1
    wait "${PROXY_PID}" >/dev/null 2>&1
  fi
  if [[ "${KEEP_CLUSTER:-0}" == "1" ]]; then
    printf 'KEEP_CLUSTER=1: cluster=%s kubeconfig=%s results=%s\n' \
      "${CLUSTER_NAME}" "${KUBECONFIG_PATH}" "${RUN_DIR}"
  elif [[ -x "${KIND_BIN}" ]]; then
    "${KIND_BIN}" delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1
    rm -rf "${TEMP_DIR}"
  fi
  exit "${status}"
}
trap cleanup EXIT INT TERM

require_tool() {
  command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"
}

step "Preflight and capture reference environment"
for tool in docker kubectl curl jq shasum awk; do
  require_tool "${tool}"
done
docker info >/dev/null
[[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "x86_64" ]] ||
  fail "captured prototype is pinned for Intel macOS"
docker info --format '{"dockerCPUs":{{.NCPU}},"dockerMemoryBytes":{{.MemTotal}},"driver":"{{.Driver}}","os":"{{.OperatingSystem}}"}' |
  jq --arg hostCPUs "$(sysctl -n hw.logicalcpu)" \
    --arg hostMemoryBytes "$(sysctl -n hw.memsize)" \
    '. + {hostCPUs: ($hostCPUs | tonumber), hostMemoryBytes: ($hostMemoryBytes | tonumber)}' \
    >"${RUN_DIR}/environment.json"
cat "${RUN_DIR}/environment.json"

step "Download and verify kind ${KIND_VERSION}"
curl -fsSL -o "${KIND_BIN}" \
  "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-darwin-amd64"
[[ "$(sha256 "${KIND_BIN}")" == "${KIND_SHA256_DARWIN_AMD64}" ]] ||
  fail "kind checksum mismatch"
chmod +x "${KIND_BIN}"

step "Build disposable scale driver"
docker run --rm \
  -v "${PROTOTYPE_DIR}/driver:/src:ro" \
  -v "${TEMP_DIR}:/out" \
  -w /src \
  golang:1.25-alpine \
  sh -c 'CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/scale-driver main.go'

step "Create isolated single-control-plane kind cluster"
cat >"${TEMP_DIR}/kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
EOF
"${KIND_BIN}" create cluster \
  --name "${CLUSTER_NAME}" \
  --config "${TEMP_DIR}/kind.yaml" \
  --image "${KIND_NODE_IMAGE}" \
  --kubeconfig "${KUBECONFIG_PATH}" \
  --retain \
  --wait 180s
k config current-context | grep -Fx "${KIND_CONTEXT}" >/dev/null ||
  fail "unexpected kind context"
real_node="$(k get nodes -o jsonpath='{.items[0].metadata.name}')"
k patch node "${real_node}" --type=merge -p '{"spec":{"taints":[]}}' >/dev/null
k wait --for=condition=Ready nodes --all --timeout=120s
snapshot_resources baseline
sample_resources &
STATS_PID=$!

step "Download, verify, and install KWOK ${KWOK_VERSION}"
curl -fsSL -o "${TEMP_DIR}/kwok.yaml" \
  "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/kwok.yaml"
curl -fsSL -o "${TEMP_DIR}/stage-fast.yaml" \
  "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/stage-fast.yaml"
[[ "$(sha256 "${TEMP_DIR}/kwok.yaml")" == "${KWOK_MANIFEST_SHA256}" ]] ||
  fail "KWOK manifest checksum mismatch"
[[ "$(sha256 "${TEMP_DIR}/stage-fast.yaml")" == "${KWOK_STAGE_SHA256}" ]] ||
  fail "KWOK stage checksum mismatch"
k apply -f "${TEMP_DIR}/kwok.yaml" >/dev/null
k apply -f "${TEMP_DIR}/stage-fast.yaml" >/dev/null
affinity="$(jq -n '{
  spec: {
    template: {
      spec: {
        affinity: {
          nodeAffinity: {
            requiredDuringSchedulingIgnoredDuringExecution: {
              nodeSelectorTerms: [{
                matchExpressions: [{
                  key: "sim.kube-accelerator.io/managed-by",
                  operator: "DoesNotExist"
                }]
              }]
            }
          }
        }
      }
    }
  }
}')"
k patch deployment kwok-controller -n kube-system --type=merge -p "${affinity}" >/dev/null
k rollout status deployment/kwok-controller -n kube-system --timeout=180s
snapshot_resources kwok-installed

step "Start authenticated API proxy"
k proxy --address=127.0.0.1 --port="${PROXY_PORT}" \
  >"${RUN_DIR}/kubectl-proxy.log" 2>&1 &
PROXY_PID=$!
for _ in $(seq 1 30); do
  if curl -fsS "${PROXY_URL}/version" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "${PROXY_URL}/version" >/dev/null ||
  fail "kubectl proxy did not start"

step "Apply ${NODE_COUNT} Synthetic Nodes and ${ACCELERATOR_COUNT} Accelerators"
driver -command apply-nodes -count "${NODE_COUNT}" -timeout 5m
driver -command wait-ready -count "${NODE_COUNT}" -timeout 5m
driver -command label-leases -count "${NODE_COUNT}" -timeout 5m
snapshot_resources nodes-ready

step "Measure repeated full-instance observation"
driver -command observe -count "${NODE_COUNT}" -iterations 10 -timeout 2m

step "Apply and recover 10 percent aggregate health loss"
driver -command patch-health -sample "${HEALTH_SAMPLE}" -healthy 0 -timeout 2m
driver -command patch-health -sample "${HEALTH_SAMPLE}" -healthy 8 -timeout 2m
snapshot_resources health-recovered

step "Schedule ${WORKLOAD_COUNT} representative full-node Accelerator Pods"
k create namespace kas-scale-workloads >/dev/null
driver -command apply-pods -count "${WORKLOAD_COUNT}" -timeout 3m
driver -command wait-pods -count "${WORKLOAD_COUNT}" -timeout 3m
snapshot_resources workloads-running
driver -command delete-pods -count "${WORKLOAD_COUNT}" -timeout 3m

step "Measure controller outage and recovery"
k scale deployment kwok-controller -n kube-system --replicas=0 >/dev/null
deadline=$((SECONDS + 90))
while (( SECONDS < deadline )); do
  replicas="$(k get deployment kwok-controller -n kube-system -o jsonpath='{.status.replicas}' 2>/dev/null || true)"
  if [[ -z "${replicas}" || "${replicas}" == "0" ]]; then
    break
  fi
  sleep 1
done
[[ -z "${replicas}" || "${replicas}" == "0" ]] ||
  fail "KWOK controller did not scale down"
driver -command patch-ready-false -sample "${HEALTH_SAMPLE}" -timeout 2m
restart_started="$(date +%s)"
k scale deployment kwok-controller -n kube-system --replicas=1 >/dev/null
k wait deployment kwok-controller -n kube-system \
  --for=condition=Available --timeout=180s >/dev/null
driver -command wait-ready -count "${NODE_COUNT}" -timeout 3m
restart_seconds=$(($(date +%s) - restart_started))
jq -n \
  --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson durationMillis "$((restart_seconds * 1000))" \
  '{timestamp: $timestamp, operation: "controller-restart-recovery", durationMillis: $durationMillis}' |
  tee -a "${RUN_DIR}/events.jsonl"
snapshot_resources restart-recovered

step "Delete only exact Scenario Instance objects"
driver -command delete-nodes -count "${NODE_COUNT}" -timeout 5m
driver -command delete-leases -count "${NODE_COUNT}" -timeout 5m
k delete namespace kas-scale-workloads --wait=true >/dev/null
remaining_nodes="$(k get nodes -l 'sim.kube-accelerator.io/instance-uid=issue-4-scale' -o name)"
remaining_leases="$(k get leases -n kube-node-lease -l 'sim.kube-accelerator.io/instance-uid=issue-4-scale' -o name)"
[[ -z "${remaining_nodes}" && -z "${remaining_leases}" ]] ||
  fail "exact-UID cleanup leaked Nodes or Leases"
snapshot_resources cleaned

step "Capture machine-readable summary"
jq -s \
  --arg runID "${RUN_ID}" \
  --argjson nodes "${NODE_COUNT}" \
  --argjson accelerators "${ACCELERATOR_COUNT}" \
  --argjson healthUpdateNodes "${HEALTH_SAMPLE}" \
  --argjson representativePods "${WORKLOAD_COUNT}" \
  --slurpfile environment "${RUN_DIR}/environment.json" \
  '{
    runID: $runID,
    environment: $environment[0],
    scenario: {
      syntheticNodes: $nodes,
      accelerators: $accelerators,
      acceleratorsPerNode: 8,
      healthUpdateNodes: $healthUpdateNodes,
      representativePods: $representativePods
    },
    operations: .
  }' "${RUN_DIR}/events.jsonl" |
  tee "${RUN_DIR}/summary.json"

step "PASS — results ${RUN_DIR}"
