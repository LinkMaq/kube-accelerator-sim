#!/usr/bin/env bash
# PROTOTYPE: one-command substrate experiment for GitHub issue #12.
set -euo pipefail

PROTOTYPE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${PROTOTYPE_DIR}/../.." && pwd)"
RESULTS_DIR="${PROTOTYPE_DIR}/.results"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
RUN_DIR="${RESULTS_DIR}/${RUN_ID}"
mkdir -p "${RESULTS_DIR}"
TEMP_DIR="$(cd "$(mktemp -d "${RESULTS_DIR}/tmp.${RUN_ID}.XXXXXX")" && pwd -P)"
CLUSTER_NAME="kas-proto-12-$$"
KUBECONFIG_PATH="${TEMP_DIR}/kubeconfig"
KIND_CONTEXT="kind-${CLUSTER_NAME}"
AUDIT_POLICY_DIRECTORY="${TEMP_DIR}/audit-policy"
AUDIT_POLICY="${AUDIT_POLICY_DIRECTORY}/policy.yaml"
AUDIT_DIRECTORY="${TEMP_DIR}/audit"
AUDIT_LOG="${AUDIT_DIRECTORY}/audit.log"
KIND_BIN="${TEMP_DIR}/kind"

KIND_VERSION="v0.32.0"
KIND_SHA256_DARWIN_AMD64="295ac6d0d634c9819c9907df45e3017d1f13166bd13c3404c45e79f7faa47498"
KIND_NODE_IMAGE="kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"
KWOK_VERSION="v0.8.0"
KWOK_MANIFEST_SHA256="a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405"
KWOK_STAGE_SHA256="2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24"

WORKLOAD_NAMESPACE="kas-prototype-workloads"
LABEL_PREFIX="sim.kube-accelerator.io"
MANAGED_SELECTOR="${LABEL_PREFIX}/managed-by=prototype-12"
KWOK_INSTANCE="issue-12-kwok"
NATIVE_INSTANCE="issue-12-native"

mkdir -p "${RUN_DIR}"
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

sha256() {
  shasum -a 256 "$1" | awk '{print $1}'
}

cleanup() {
  status=$?
  set +e
  if [[ "${KEEP_CLUSTER:-0}" == "1" ]]; then
    printf 'KEEP_CLUSTER=1: cluster=%s kubeconfig=%s\n' "${CLUSTER_NAME}" "${KUBECONFIG_PATH}"
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

wait_node_ready() {
  node="$1"
  deadline=$((SECONDS + 90))
  while (( SECONDS < deadline )); do
    ready="$(k get node "${node}" -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || true)"
    if [[ "${ready}" == "True" ]]; then
      return 0
    fi
    sleep 1
  done
  k get node "${node}" -o yaml || true
  fail "node did not become Ready: ${node}"
}

wait_lease() {
  node="$1"
  deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    if k get lease -n kube-node-lease "${node}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "lease was not created: ${node}"
}

wait_pod_bound() {
  pod="$1"
  deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    node="$(k get pod -n "${WORKLOAD_NAMESPACE}" "${pod}" -o jsonpath='{.spec.nodeName}' 2>/dev/null || true)"
    if [[ -n "${node}" ]]; then
      printf '%s\n' "${node}"
      return 0
    fi
    sleep 1
  done
  k describe pod -n "${WORKLOAD_NAMESPACE}" "${pod}" || true
  fail "pod was not bound: ${pod}"
}

wait_pod_phase() {
  pod="$1"
  expected="$2"
  deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    phase="$(k get pod -n "${WORKLOAD_NAMESPACE}" "${pod}" \
      -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [[ "${phase}" == "${expected}" ]]; then
      return 0
    fi
    sleep 1
  done
  fail "pod phase did not become ${expected}: ${pod} phase=${phase}"
}

assert_pod_unbound() {
  pod="$1"
  wait_seconds="${2:-8}"
  sleep "${wait_seconds}"
  node="$(k get pod -n "${WORKLOAD_NAMESPACE}" "${pod}" -o jsonpath='{.spec.nodeName}' 2>/dev/null || true)"
  [[ -z "${node}" ]] || fail "pod unexpectedly bound: ${pod} -> ${node}"
}

stable_real_snapshot() {
  output="$1"
  names_json="$(jq -Rsc 'split("\n") | map(select(length > 0))' "${TEMP_DIR}/real-node-names")"
  {
    k get nodes -o json | jq -S --argjson names "${names_json}" '{
      nodes: [
        .items[]
        | select(.metadata.name as $name | $names | index($name))
        | {
            name: .metadata.name,
            labels: .metadata.labels,
            spec: .spec,
            status: {
              addresses: .status.addresses,
              allocatable: .status.allocatable,
              capacity: .status.capacity,
              daemonEndpoints: .status.daemonEndpoints,
              nodeInfo: .status.nodeInfo
            }
          }
      ]
    }'
    k get leases -n kube-node-lease -o json | jq -S --argjson names "${names_json}" '{
      leases: [
        .items[]
        | select(.metadata.name as $name | $names | index($name))
        | {
            name: .metadata.name,
            holderIdentity: .spec.holderIdentity,
            leaseDurationSeconds: .spec.leaseDurationSeconds,
            acquireTime: .spec.acquireTime,
            leaseTransitions: .spec.leaseTransitions
          }
      ]
    }'
  } >"${output}"
}

assert_real_snapshot_unchanged() {
  runtime="$1"
  current="${RUN_DIR}/real-${runtime}.json"
  stable_real_snapshot "${current}"
  if ! diff -u "${RUN_DIR}/real-baseline.json" "${current}" >"${RUN_DIR}/real-${runtime}.diff"; then
    cat "${RUN_DIR}/real-${runtime}.diff"
    fail "${runtime} changed stable real-Node or Lease state"
  fi
}

create_synthetic_node() {
  runtime="$1"
  instance="$2"
  node="$3"
  profile="$4"
  resource="$5"
  count="$6"

  annotation='{}'
  if [[ "${runtime}" == "kwok" ]]; then
    annotation='{"kwok.x-k8s.io/node":"fake"}'
  fi

  jq -n \
    --arg name "${node}" \
    --arg runtime "${runtime}" \
    --arg instance "${instance}" \
    --arg profile "${profile}" \
    --argjson annotations "${annotation}" \
    '{
      apiVersion: "v1",
      kind: "Node",
      metadata: {
        name: $name,
        annotations: $annotations,
        labels: {
          "kubernetes.io/hostname": $name,
          "sim.kube-accelerator.io/managed-by": "prototype-12",
          "sim.kube-accelerator.io/runtime": $runtime,
          "sim.kube-accelerator.io/instance-uid": $instance,
          "sim.kube-accelerator.io/profile": $profile
        }
      },
      spec: {}
    }' | k create -f -

  wait_node_ready "${node}"
  patch_node_resources "${node}" "${resource}" "${count}" "${count}"
  wait_lease "${node}"
  k label lease -n kube-node-lease "${node}" \
    "${LABEL_PREFIX}/managed-by=prototype-12" \
    "${LABEL_PREFIX}/runtime=${runtime}" \
    "${LABEL_PREFIX}/instance-uid=${instance}" \
    --overwrite >/dev/null
}

patch_node_resources() {
  node="$1"
  resource="$2"
  capacity="$3"
  healthy="$4"
  payload="$(jq -n \
    --arg resource "${resource}" \
    --arg capacity "${capacity}" \
    --arg healthy "${healthy}" \
    '{
      status: {
        phase: "Running",
        capacity: {
          cpu: "64",
          memory: "256Gi",
          pods: "110",
          ($resource): $capacity
        },
        allocatable: {
          cpu: "64",
          memory: "256Gi",
          pods: "110",
          ($resource): $healthy
        }
      }
    }')"
  k patch node "${node}" --subresource=status --type=merge -p "${payload}" >/dev/null
}

patch_node_healthy() {
  node="$1"
  resource="$2"
  healthy="$3"
  payload="$(jq -n --arg resource "${resource}" --arg healthy "${healthy}" \
    '{status: {allocatable: {($resource): $healthy}}}')"
  k patch node "${node}" --subresource=status --type=merge -p "${payload}" >/dev/null
}

create_workload_namespace() {
  k create namespace "${WORKLOAD_NAMESPACE}" >/dev/null
}

delete_workloads() {
  k delete pod --all -n "${WORKLOAD_NAMESPACE}" \
    --force --grace-period=0 --wait=true --ignore-not-found >/dev/null
}

create_accelerator_pod() {
  name="$1"
  runtime="$2"
  instance="$3"
  profile="$4"
  resource="$5"
  count="$6"
  target_node="${7:-}"
  case_label="${8:-${name}}"
  anti_affinity="${9:-false}"

  jq -n \
    --arg name "${name}" \
    --arg runtime "${runtime}" \
    --arg instance "${instance}" \
    --arg profile "${profile}" \
    --arg resource "${resource}" \
    --arg count "${count}" \
    --arg targetNode "${target_node}" \
    --arg caseLabel "${case_label}" \
    --argjson antiAffinity "${anti_affinity}" \
    '{
      apiVersion: "v1",
      kind: "Pod",
      metadata: {
        name: $name,
        namespace: "kas-prototype-workloads",
        labels: {"prototype-case": $caseLabel}
      },
      spec: {
        restartPolicy: "Never",
        nodeSelector: ({
          "sim.kube-accelerator.io/managed-by": "prototype-12",
          "sim.kube-accelerator.io/runtime": $runtime,
          "sim.kube-accelerator.io/instance-uid": $instance,
          "sim.kube-accelerator.io/profile": $profile
        } + (if $targetNode == "" then {} else {"kubernetes.io/hostname": $targetNode} end)),
        affinity: (
          if $antiAffinity then {
            podAntiAffinity: {
              requiredDuringSchedulingIgnoredDuringExecution: [{
                labelSelector: {
                  matchExpressions: [{
                    key: "prototype-case",
                    operator: "In",
                    values: [$caseLabel]
                  }]
                },
                topologyKey: "kubernetes.io/hostname"
              }]
            }
          } else null end
        ),
        containers: [{
          name: "pause",
          image: "registry.k8s.io/pause:3.10",
          resources: {
            requests: {($resource): $count},
            limits: {($resource): $count}
          }
        }]
      }
    }
    | if .spec.affinity == null then del(.spec.affinity) else . end' | k create -f - >/dev/null
}

run_scheduler_suite() {
  runtime="$1"
  instance="$2"
  nvidia_a="$3"
  nvidia_b="$4"

  step "${runtime}: single-card, multi-card, and heterogeneous scheduling"
  create_accelerator_pod single-card "${runtime}" "${instance}" nvidia nvidia.com/gpu 1
  single_node="$(wait_pod_bound single-card)"
  create_accelerator_pod multi-card "${runtime}" "${instance}" nvidia nvidia.com/gpu 4
  multi_node="$(wait_pod_bound multi-card)"
  create_accelerator_pod heterogeneous-amd "${runtime}" "${instance}" amd amd.com/gpu 1
  amd_node="$(wait_pod_bound heterogeneous-amd)"
  [[ "${amd_node}" == *"-amd" ]] || fail "AMD workload landed on unexpected node: ${amd_node}"
  if [[ "${runtime}" == "kwok" ]]; then
    wait_pod_phase single-card Running
    wait_pod_phase multi-card Running
    wait_pod_phase heterogeneous-amd Running
  else
    wait_pod_phase single-card Pending
    wait_pod_phase multi-card Pending
    wait_pod_phase heterogeneous-amd Pending
  fi
  printf 'single-card=%s multi-card=%s heterogeneous-amd=%s\n' "${single_node}" "${multi_node}" "${amd_node}"
  k get pods -n "${WORKLOAD_NAMESPACE}" -o wide
  k get pods -n "${WORKLOAD_NAMESPACE}" \
    -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,NODE:.spec.nodeName \
    >"${RUN_DIR}/pod-lifecycle-${runtime}.txt"

  delete_workloads

  step "${runtime}: multi-node multi-card scheduling"
  create_accelerator_pod spread-0 "${runtime}" "${instance}" nvidia nvidia.com/gpu 4 "" spread true
  spread_0_node="$(wait_pod_bound spread-0)"
  create_accelerator_pod spread-1 "${runtime}" "${instance}" nvidia nvidia.com/gpu 4 "" spread true
  spread_1_node="$(wait_pod_bound spread-1)"
  [[ "${spread_0_node}" != "${spread_1_node}" ]] || fail "anti-affinity did not spread multi-card workloads"
  printf 'spread-0=%s spread-1=%s\n' "${spread_0_node}" "${spread_1_node}"
  delete_workloads

  step "${runtime}: aggregate health transition and recovery"
  patch_node_healthy "${nvidia_b}" nvidia.com/gpu 1
  capacity="$(k get node "${nvidia_b}" -o jsonpath='{.status.capacity.nvidia\.com/gpu}')"
  allocatable="$(k get node "${nvidia_b}" -o jsonpath='{.status.allocatable.nvidia\.com/gpu}')"
  [[ "${capacity}" == "8" && "${allocatable}" == "1" ]] || fail "health update changed the wrong fields"
  create_accelerator_pod health-gated "${runtime}" "${instance}" nvidia nvidia.com/gpu 2 "${nvidia_b}"
  assert_pod_unbound health-gated
  patch_node_healthy "${nvidia_b}" nvidia.com/gpu 8
  recovered_node="$(wait_pod_bound health-gated)"
  [[ "${recovered_node}" == "${nvidia_b}" ]] || fail "health recovery bound to unexpected node"
  delete_workloads

  step "${runtime}: resource exhaustion remains unschedulable"
  create_accelerator_pod exhausted "${runtime}" "${instance}" nvidia nvidia.com/gpu 9 "${nvidia_a}"
  assert_pod_unbound exhausted
  k get events -n "${WORKLOAD_NAMESPACE}" \
    --field-selector involvedObject.kind=Pod,involvedObject.name=exhausted \
    -o json | jq -e '.items | any(.reason == "FailedScheduling")' >/dev/null ||
    fail "expected FailedScheduling event was not observed"
  delete_workloads
}

restart_controller_and_verify() {
  runtime="$1"
  deployment_namespace="$2"
  deployment_name="$3"
  node="$4"

  step "${runtime}: controller restart and recovery"
  k scale deployment -n "${deployment_namespace}" "${deployment_name}" --replicas=0 >/dev/null
  deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    replicas="$(k get deployment -n "${deployment_namespace}" "${deployment_name}" \
      -o jsonpath='{.status.replicas}' 2>/dev/null || true)"
    if [[ -z "${replicas}" || "${replicas}" == "0" ]]; then
      break
    fi
    sleep 1
  done
  [[ -z "${replicas}" || "${replicas}" == "0" ]] ||
    fail "${runtime} controller did not scale down"
  timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  payload="$(jq -n --arg now "${timestamp}" '{
    status: {
      conditions: [{
        type: "Ready",
        status: "False",
        reason: "PrototypeRestartProbe",
        message: "Controller restart recovery probe",
        lastHeartbeatTime: $now,
        lastTransitionTime: $now
      }]
    }
  }')"
  k patch node "${node}" --subresource=status --type=merge -p "${payload}" >/dev/null
  k scale deployment -n "${deployment_namespace}" "${deployment_name}" --replicas=1 >/dev/null
  if ! k wait deployment -n "${deployment_namespace}" "${deployment_name}" \
    --for=condition=Available --timeout=90s >/dev/null; then
    k get deployment -n "${deployment_namespace}" "${deployment_name}" -o yaml || true
    k get pods -n "${deployment_namespace}" -o wide || true
    k logs -n "${deployment_namespace}" deployment/"${deployment_name}" --all-containers || true
    fail "${runtime} controller did not become Available after restart"
  fi
  wait_node_ready "${node}"
}

pin_controller_to_real_nodes() {
  namespace="$1"
  deployment="$2"
  payload="$(jq -n --arg key "${LABEL_PREFIX}/managed-by" '{
    spec: {
      template: {
        spec: {
          affinity: {
            nodeAffinity: {
              requiredDuringSchedulingIgnoredDuringExecution: {
                nodeSelectorTerms: [{
                  matchExpressions: [{
                    key: $key,
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
  k patch deployment -n "${namespace}" "${deployment}" --type=merge -p "${payload}" >/dev/null
}

cleanup_instance() {
  runtime="$1"
  instance="$2"

  step "${runtime}: exact-ownership cleanup"
  k delete namespace "${WORKLOAD_NAMESPACE}" --wait=true --ignore-not-found >/dev/null
  k delete nodes -l "${LABEL_PREFIX}/instance-uid=${instance}" --wait=true --ignore-not-found >/dev/null
  k delete leases -n kube-node-lease -l "${LABEL_PREFIX}/instance-uid=${instance}" \
    --wait=true --ignore-not-found >/dev/null

  remaining_nodes="$(k get nodes -l "${LABEL_PREFIX}/instance-uid=${instance}" -o name)"
  remaining_leases="$(k get leases -n kube-node-lease -l "${LABEL_PREFIX}/instance-uid=${instance}" -o name)"
  [[ -z "${remaining_nodes}" && -z "${remaining_leases}" ]] ||
    fail "${runtime} leaked owned Nodes or Leases"
}

audit_controller_writes_to_real_nodes() {
  runtime="$1"
  username="$2"
  output="${RUN_DIR}/audit-${runtime}-real-node-writes.json"
  names_json="$(jq -Rsc 'split("\n") | map(select(length > 0))' "${TEMP_DIR}/real-node-names")"

  jq -s \
    --arg username "${username}" \
    --argjson names "${names_json}" \
    '[
      .[]
      | select(.user.username == $username)
      | select(.verb == "create" or .verb == "update" or .verb == "patch" or .verb == "delete" or .verb == "deletecollection")
      | select(
          (.objectRef.resource == "nodes" and (.objectRef.name as $name | $names | index($name)))
          or
          (.objectRef.resource == "leases"
            and .objectRef.namespace == "kube-node-lease"
            and (.objectRef.name as $name | $names | index($name)))
        )
    ]' "${AUDIT_LOG}" >"${output}"
  jq -e 'length == 0' "${output}" >/dev/null ||
    fail "${runtime} attempted to mutate a real Node or Lease"
}

step "Preflight"
for tool in docker kubectl curl jq shasum awk diff; do
  require_tool "${tool}"
done
docker info >/dev/null
[[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "x86_64" ]] ||
  fail "this captured prototype is pinned for Intel macOS"

step "Download and verify kind ${KIND_VERSION}"
curl -fsSL -o "${KIND_BIN}" \
  "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-darwin-amd64"
[[ "$(sha256 "${KIND_BIN}")" == "${KIND_SHA256_DARWIN_AMD64}" ]] ||
  fail "kind checksum mismatch"
chmod +x "${KIND_BIN}"
"${KIND_BIN}" version

step "Prepare API audit policy"
mkdir -p "${AUDIT_POLICY_DIRECTORY}"
mkdir -p "${AUDIT_DIRECTORY}"
chmod 0777 "${AUDIT_DIRECTORY}"
cat >"${AUDIT_POLICY}" <<'EOF'
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  - level: Metadata
    resources:
      - group: ""
        resources: ["nodes", "nodes/status"]
      - group: "coordination.k8s.io"
        resources: ["leases"]
  - level: None
EOF

cat >"${TEMP_DIR}/kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
kubeadmConfigPatches:
  - |
    apiVersion: kubeadm.k8s.io/v1beta4
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        - name: audit-policy-file
          value: /etc/kubernetes/audit/policy.yaml
        - name: audit-log-path
          value: /var/log/kubernetes/audit.log
        - name: audit-log-maxbackup
          value: "1"
        - name: audit-log-maxsize
          value: "100"
      extraVolumes:
        - name: audit-policy
          hostPath: /etc/kubernetes/audit/policy.yaml
          mountPath: /etc/kubernetes/audit/policy.yaml
          readOnly: true
          pathType: File
        - name: audit-logs
          hostPath: /var/log/kubernetes
          mountPath: /var/log/kubernetes
          readOnly: false
          pathType: DirectoryOrCreate
nodes:
  - role: control-plane
    extraMounts:
      - hostPath: ${AUDIT_POLICY_DIRECTORY}
        containerPath: /etc/kubernetes/audit
        readOnly: true
      - hostPath: ${AUDIT_DIRECTORY}
        containerPath: /var/log/kubernetes
  - role: worker
EOF

step "Create isolated kind cluster"
"${KIND_BIN}" create cluster \
  --name "${CLUSTER_NAME}" \
  --config "${TEMP_DIR}/kind.yaml" \
  --image "${KIND_NODE_IMAGE}" \
  --kubeconfig "${KUBECONFIG_PATH}" \
  --retain \
  --wait 180s
k config current-context | grep -Fx "${KIND_CONTEXT}" >/dev/null ||
  fail "unexpected kind context"
k wait --for=condition=Ready nodes --all --timeout=120s
k get nodes -o wide
k get nodes -o json | jq -r '.items[].metadata.name' >"${TEMP_DIR}/real-node-names"
stable_real_snapshot "${RUN_DIR}/real-baseline.json"

step "Download and verify KWOK ${KWOK_VERSION}"
curl -fsSL -o "${TEMP_DIR}/kwok.yaml" \
  "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/kwok.yaml"
curl -fsSL -o "${TEMP_DIR}/stage-fast.yaml" \
  "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/stage-fast.yaml"
[[ "$(sha256 "${TEMP_DIR}/kwok.yaml")" == "${KWOK_MANIFEST_SHA256}" ]] ||
  fail "KWOK manifest checksum mismatch"
[[ "$(sha256 "${TEMP_DIR}/stage-fast.yaml")" == "${KWOK_STAGE_SHA256}" ]] ||
  fail "KWOK stage manifest checksum mismatch"

step "KWOK: install pinned runtime"
k apply -f "${TEMP_DIR}/kwok.yaml" >/dev/null
k apply -f "${TEMP_DIR}/stage-fast.yaml" >/dev/null
pin_controller_to_real_nodes kube-system kwok-controller
k rollout status deployment/kwok-controller -n kube-system --timeout=120s
k get clusterrole kwok-controller -o json | jq -S '.rules' >"${RUN_DIR}/rbac-kwok.json"

step "KWOK: apply owned Scenario Instance"
create_synthetic_node kwok "${KWOK_INSTANCE}" kas-kwok-nvidia-a nvidia nvidia.com/gpu 8
create_synthetic_node kwok "${KWOK_INSTANCE}" kas-kwok-nvidia-b nvidia nvidia.com/gpu 8
create_synthetic_node kwok "${KWOK_INSTANCE}" kas-kwok-amd amd amd.com/gpu 2
k get nodes -l "${LABEL_PREFIX}/instance-uid=${KWOK_INSTANCE}" \
  -o custom-columns=NAME:.metadata.name,READY:.status.conditions[-1].status,NVIDIA:.status.allocatable.nvidia\\.com/gpu,AMD:.status.allocatable.amd\\.com/gpu
create_workload_namespace
run_scheduler_suite kwok "${KWOK_INSTANCE}" kas-kwok-nvidia-a kas-kwok-nvidia-b
restart_controller_and_verify kwok kube-system kwok-controller kas-kwok-nvidia-a
cleanup_instance kwok "${KWOK_INSTANCE}"
k delete -f "${TEMP_DIR}/stage-fast.yaml" --ignore-not-found >/dev/null
k delete -f "${TEMP_DIR}/kwok.yaml" --ignore-not-found >/dev/null
assert_real_snapshot_unchanged kwok
audit_controller_writes_to_real_nodes kwok system:serviceaccount:kube-system:kwok-controller

step "Native: build and load throwaway reconciler"
docker build -t kas-native-prototype:issue-12 \
  "${PROTOTYPE_DIR}/native-reconciler"
"${KIND_BIN}" load docker-image kas-native-prototype:issue-12 --name "${CLUSTER_NAME}"
k apply -f "${PROTOTYPE_DIR}/native-reconciler/manifest.yaml" >/dev/null
pin_controller_to_real_nodes kas-native-prototype native-reconciler
k rollout status deployment/native-reconciler -n kas-native-prototype --timeout=120s
k get clusterrole kas-native-prototype -o json | jq -S '.rules' >"${RUN_DIR}/rbac-native.json"
docker image inspect kas-native-prototype:issue-12 \
  --format '{{.Size}}' >"${RUN_DIR}/native-image-bytes.txt"

step "Native: apply owned Scenario Instance"
create_synthetic_node native "${NATIVE_INSTANCE}" kas-native-nvidia-a nvidia nvidia.com/gpu 8
create_synthetic_node native "${NATIVE_INSTANCE}" kas-native-nvidia-b nvidia nvidia.com/gpu 8
create_synthetic_node native "${NATIVE_INSTANCE}" kas-native-amd amd amd.com/gpu 2
k get nodes -l "${LABEL_PREFIX}/instance-uid=${NATIVE_INSTANCE}" \
  -o custom-columns=NAME:.metadata.name,READY:.status.conditions[-1].status,NVIDIA:.status.allocatable.nvidia\\.com/gpu,AMD:.status.allocatable.amd\\.com/gpu
create_workload_namespace
run_scheduler_suite native "${NATIVE_INSTANCE}" kas-native-nvidia-a kas-native-nvidia-b
restart_controller_and_verify native kas-native-prototype native-reconciler kas-native-nvidia-a
cleanup_instance native "${NATIVE_INSTANCE}"
k delete -f "${PROTOTYPE_DIR}/native-reconciler/manifest.yaml" --ignore-not-found >/dev/null
assert_real_snapshot_unchanged native
audit_controller_writes_to_real_nodes native system:serviceaccount:kas-native-prototype:native-reconciler

step "Capture comparison"
jq -n \
  --arg runID "${RUN_ID}" \
  --arg kind "${KIND_VERSION}" \
  --arg kubernetes "v1.36.1" \
  --arg kwok "${KWOK_VERSION}" \
  --arg baselineHash "$(sha256 "${RUN_DIR}/real-baseline.json")" \
  --arg kwokHash "$(sha256 "${RUN_DIR}/real-kwok.json")" \
  --arg nativeHash "$(sha256 "${RUN_DIR}/real-native.json")" \
  --arg nativeImageBytes "$(cat "${RUN_DIR}/native-image-bytes.txt")" \
  --argjson kwokRBACRules "$(jq 'length' "${RUN_DIR}/rbac-kwok.json")" \
  --argjson nativeRBACRules "$(jq 'length' "${RUN_DIR}/rbac-native.json")" \
  '{
    runID: $runID,
    versions: {kind: $kind, kubernetes: $kubernetes, kwok: $kwok},
    stableRealStateHashes: {
      baseline: $baselineHash,
      afterKWOK: $kwokHash,
      afterNative: $nativeHash
    },
    realNodeMutatingAuditEvents: {kwok: 0, native: 0},
    rbacRuleCounts: {kwok: $kwokRBACRules, native: $nativeRBACRules},
    nativeImageBytes: ($nativeImageBytes | tonumber),
    verdictInput: {
      kwokSchedulingSuite: "passed",
      nativeSchedulingSuite: "passed",
      podLifecycle: {
        kwok: "Running",
        native: "Pending (no Pod lifecycle emulation)"
      },
      kwokRestartRecovery: "passed",
      nativeRestartRecovery: "passed",
      exactOwnershipCleanup: "passed"
    }
  }' | tee "${RUN_DIR}/summary.json"

step "PASS — results ${RUN_DIR}"
