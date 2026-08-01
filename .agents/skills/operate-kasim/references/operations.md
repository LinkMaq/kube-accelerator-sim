# Native operation templates

Replace every uppercase placeholder. Use the same explicit target in all
connected commands. Prefer a released CLI that matches the runtime Chart.

## Prepare and inspect

```sh
KASIM_BIN=./dist/kasim
KUBECONFIG_PATH=./target.kubeconfig
KUBE_CONTEXT=target
RECEIPT_DIR=dist/receipts/SCENARIO_NAME

"$KASIM_BIN" version -o json
"$KASIM_BIN" profile list -o json
"$KASIM_BIN" profile show PROFILE_ID -o json
mkdir -p "$RECEIPT_DIR"
```

These are task-specific variables, not ambient target selection. Always pass
the kubeconfig and context flags explicitly.

## Install the shared runtime

```sh
kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  get namespace kasim-system >/dev/null 2>&1 || \
kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  create namespace kasim-system

helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig "$KUBECONFIG_PATH" \
  --kube-context "$KUBE_CONTEXT" \
  --namespace kasim-system \
  --wait --timeout 10m

kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --namespace kasim-system rollout status \
  deployment/kasim-runtime-kasim-runtime-controller --timeout=5m
kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --namespace kasim-system rollout status \
  deployment/kasim-runtime-kasim-runtime-kwok-controller --timeout=5m
```

Use the pinned OCI Chart and `--version` instead of the local Chart when the
user requests a published release deployment.

## Start a homogeneous Scenario

Compile offline first:

```sh
"$KASIM_BIN" apply demo \
  --profile PROFILE_ID --model MODEL_ID \
  --contract device-plugin --resource RESOURCE_ALIAS \
  --nodes NODE_COUNT \
  --accelerators-per-node UNITS_PER_NODE \
  --healthy-per-node HEALTHY_PER_NODE \
  --fidelity scheduling \
  --dry-run=client -o json \
  | tee "$RECEIPT_DIR/compile.json"
```

Then run target preflight and persist:

```sh
"$KASIM_BIN" apply demo \
  --profile PROFILE_ID --model MODEL_ID \
  --contract device-plugin --resource RESOURCE_ALIAS \
  --nodes NODE_COUNT \
  --accelerators-per-node UNITS_PER_NODE \
  --healthy-per-node HEALTHY_PER_NODE \
  --fidelity scheduling \
  --dry-run=server \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  -o json | tee "$RECEIPT_DIR/server-dry-run.json"

"$KASIM_BIN" apply demo \
  --profile PROFILE_ID --model MODEL_ID \
  --contract device-plugin --resource RESOURCE_ALIAS \
  --nodes NODE_COUNT \
  --accelerators-per-node UNITS_PER_NODE \
  --healthy-per-node HEALTHY_PER_NODE \
  --fidelity scheduling \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --timeout 10m -o json | tee "$RECEIPT_DIR/apply.json"
```

The shortcut creates the Scenario name `demo`. Use a Scenario YAML when a
stable custom name or more than one pool is required.

## Apply a Scenario document

```sh
"$KASIM_BIN" apply -f SCENARIO_FILE --dry-run=client -o json \
  | tee "$RECEIPT_DIR/compile.json"
"$KASIM_BIN" apply -f SCENARIO_FILE --dry-run=server \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" -o json \
  | tee "$RECEIPT_DIR/server-dry-run.json"
"$KASIM_BIN" apply -f SCENARIO_FILE \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --timeout 10m -o json | tee "$RECEIPT_DIR/apply.json"
```

## Observe, revise, and delete

```sh
"$KASIM_BIN" status SCENARIO_NAME \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --watch --timeout 10m -o json | tee "$RECEIPT_DIR/status.json"

kubectl --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  get nodes -l simulation.kasim.io/scenario=SCENARIO_NAME -o wide
```

Immediately before each mutation, refresh status and extract the exact
`instanceUID` and `desiredGeneration`. Then use one native typed revision:

```sh
"$KASIM_BIN" health SCENARIO_NAME --group GROUP --pool POOL \
  --healthy HEALTHY_PER_NODE \
  --instance-uid INSTANCE_UID --expected-generation GENERATION \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --timeout 10m -o json | tee "$RECEIPT_DIR/health.json"

"$KASIM_BIN" scale SCENARIO_NAME --group GROUP --replicas NODE_COUNT \
  --instance-uid INSTANCE_UID --expected-generation GENERATION \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --timeout 10m -o json | tee "$RECEIPT_DIR/scale.json"

"$KASIM_BIN" delete SCENARIO_NAME \
  --instance-uid INSTANCE_UID --expected-generation GENERATION \
  --kubeconfig "$KUBECONFIG_PATH" --context "$KUBE_CONTEXT" \
  --timeout 10m -o json | tee "$RECEIPT_DIR/delete.json"
```

Never parse UID or generation from an older receipt. After `health` or
`scale`, fetch status again before another mutation.
