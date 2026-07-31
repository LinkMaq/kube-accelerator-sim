# Existing-cluster quickstart

This walkthrough starts with an already-existing Kubernetes cluster. `kasim`
does not create, upgrade, stop, or delete that cluster, and it never uses an
implicit kubeconfig, `$KUBECONFIG`, default path, or current context.

The target must be Kubernetes 1.30–1.36. Use `scheduling` throughout that
range. Use `dra-control-plane` only on 1.34–1.36.

## 1. Build or unpack the CLI

From a source checkout:

```sh
go build -trimpath -o ./dist/kasim ./cmd/kasim
./dist/kasim version -o json
```

For a published release, verify the archive before unpacking it as described
in [Release verification](release-verification.md).

## 2. Inspect and compile offline

Catalog inspection and client dry-run do not contact Kubernetes:

```sh
./dist/kasim profile list -o json
./dist/kasim profile show nvidia -o json
./dist/kasim apply -f ./examples/single-node-single-accelerator.yaml \
  --dry-run=client \
  -o json
```

The compile receipt contains the canonical Scenario digest, catalog digest,
and exact resolved profile revision/digest. Treat a profile digest change as a
reviewable input change.

## 3. Install the shared runtime

Create the namespace only if it does not already exist, then install the chart.
Both tools target the same explicit kubeconfig and named context:

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  create namespace kasim-system

helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

Check the exact release and controller rollout:

```sh
helm status kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system

kubectl --kubeconfig ./target.kubeconfig --context target \
  --namespace kasim-system \
  rollout status deployment/kasim-runtime-kasim-runtime-controller
```

Installation is intentionally separate from `kasim apply`. The CLI never
invokes Helm and never installs cluster-wide runtime resources implicitly.

## 4. Submit and inspect a Scenario

Submit the example and retain the machine-readable receipt:

```sh
./dist/kasim apply -f ./examples/single-node-single-accelerator.yaml \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json | tee apply-receipt.json

./dist/kasim status single-node-single-accelerator \
  --kubeconfig ./target.kubeconfig \
  --context target \
  --watch \
  -o json | tee status-receipt.json
```

Inspect only the Scenario's labeled Synthetic Nodes:

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  get nodes \
  -l simulation.kasim.io/scenario=single-node-single-accelerator \
  -o wide
```

The status receipt is authoritative for readiness, desired/observed
generation, instance UID, resolved profiles, fidelity surfaces, diagnostics,
and owned-object counts.

## 5. Apply typed health and scale revisions

Copy the exact `instanceUID` and current `desiredGeneration` from the latest
status receipt. Do not reuse values from another target or Scenario:

```sh
INSTANCE_UID='replace-with-exact-status-instance-uid'
GENERATION='replace-with-exact-positive-desired-generation'

./dist/kasim health single-node-single-accelerator \
  --group workers \
  --pool accelerator \
  --healthy 0 \
  --instance-uid "$INSTANCE_UID" \
  --expected-generation "$GENERATION" \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

Fetch status again and copy its new generation before the next mutation:

```sh
./dist/kasim status single-node-single-accelerator \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json

./dist/kasim scale single-node-single-accelerator \
  --group workers \
  --replicas 3 \
  --instance-uid "$INSTANCE_UID" \
  --expected-generation 'replace-with-new-positive-generation' \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

`health` and `scale` create typed Scenario revisions. They do not patch Nodes
directly and do not evict bound Pods.

## 6. Delete the exact Scenario safely

Fetch one final status receipt, then use its exact UID and generation:

```sh
./dist/kasim delete single-node-single-accelerator \
  --instance-uid 'replace-with-exact-status-instance-uid' \
  --expected-generation 'replace-with-exact-positive-generation' \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json | tee delete-receipt.json
```

Deletion closes scheduling and removes only allowlisted objects proven to be
owned by that exact instance UID. If an unowned Pod is bound to a Synthetic
Node, deletion returns `CleanupBlocked`; follow the safe process in
[Troubleshooting and security](troubleshooting-security.md). There is no force
or wildcard deletion.

Confirm the exact Scenario label no longer selects a Node:

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  get nodes \
  -l simulation.kasim.io/scenario=single-node-single-accelerator
```

## 7. Uninstall the shared runtime separately

Delete all Scenario Instances safely before uninstalling the shared runtime.
Then uninstall only the Helm release:

```sh
helm uninstall kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

The chart retains its CRDs and does not delete the namespace, Scenario
Instances, user workloads, unrelated KWOK objects, or the Kubernetes cluster.
Only remove a dedicated namespace after independently confirming that it
contains nothing else you need.
