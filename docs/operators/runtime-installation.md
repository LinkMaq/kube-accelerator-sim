# Install the runtime into an existing cluster

`kasim` never creates a cluster and never invokes Helm. An installer selects
the kubeconfig/context explicitly, creates a namespace if needed, and installs
the shared runtime as a separate operation:

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  create namespace kasim-system

helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

The supported Kubernetes range is 1.30–1.36. The `scheduling` mode spans the
full range; stable `resource.k8s.io/v1` DRA control-plane projection requires
1.34–1.36. Installation into a server outside the frozen chart range fails.

## Permission personas

The chart defines separate deny-by-default identities:

| Persona | Intended use | Can mutate Nodes, Leases, or DRA inventory |
| --- | --- | --- |
| observer | Read and watch Scenario Instances | No |
| operator | Submit, update, observe, and delete exact Scenario Instances; read the `kube-system` UID and simulator-owned Node/Lease/Pod inventory; create self-access reviews | No |
| controller | Reconcile exact simulator-owned resources | Yes, only through application ownership checks |
| telemetry | Read Scenario Instances and exactly owned Synthetic Nodes; expose immutable simulated Prometheus samples | No |
| KWOK controller | Maintain the pinned simulated Node/Pod surfaces | Only its exact runtime surface |
| Stage installer | Helm hooks that apply/delete five exact pinned Stage names | Only those five Stage objects |

The generated ClusterRole names are
`<release>-kasim-runtime-observer` and
`<release>-kasim-runtime-operator`. Bind a human or automation identity to
only the role it needs. The CLI submitter must not receive the controller,
KWOK, CRD, RBAC, Namespace mutation, Secret, impersonation, Pod eviction, or
service-account-token permissions.

Telemetry is enabled by default as a separate single-replica, read-only
Deployment and ClusterIP Service. Its ServiceAccount can only `get`, `list`,
and `watch` Scenario Instances and Nodes. See
[Simulated vendor Prometheus telemetry](simulated-vendor-telemetry.md) for
scrape discovery, evidence classes, and staleness behavior.

## Ownership and uninstall

The product and KWOK Stage CRDs carry the ownership root
`kasim-runtime/v1alpha1`. A pre-existing CRD without that exact compatible root
causes installation to fail; the chart never silently adopts it. Helm also
rejects same-name runtime objects owned by another release.

Helm retains CRDs on uninstall. The release deletes only its exact service
accounts, roles, bindings, Deployments, ConfigMap, and five pinned Stage
objects. Scenario Instances, Synthetic Nodes, user workloads, unrelated KWOK
resources, and the cluster itself are outside the uninstall ownership set.

## Supply-chain inputs

The controller uses pinned multi-architecture build/runtime base image indexes.
KWOK v0.8.0 assets and image are content-addressed. The complete input lock for
SBOM and provenance generation is in `release/inputs.json`.

The exact Kubernetes patch/image validation lock and the CI evidence cadence
are documented in [Kubernetes compatibility](kubernetes-compatibility.md).
Checksums, attestations, signatures, OCI publication, and consumer verification
are documented in [Release verification](release-verification.md).
