# kasim-runtime chart

This chart installs the shared kube-accelerator-sim runtime into an explicitly
selected, already-existing Kubernetes cluster and namespace. It never manages
the cluster lifecycle. Create the namespace separately, then install:

```sh
helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system
```

For the published `v0.2.0` package, install the same immutable chart directly
from GitHub Container Registry:

```sh
helm upgrade --install kasim-runtime \
  oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.2.0 \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system
```

The chart selects
`ghcr.io/linkmaq/kube-accelerator-sim-controller:0.2.0` by its matching
`appVersion`. A different controller tag is rejected; use the chart and image
from the same release.

The default controller and KWOK Pods are non-root, use a read-only root
filesystem, drop all Linux capabilities, and have hard node affinity excluding
simulator-owned Synthetic Nodes.

The chart creates separate observer, lifecycle operator, product controller,
KWOK controller, and Stage installer service accounts and exact RBAC roles.
The Stage installer is used only by bounded Helm hooks. Bind human or
automation identities to the observer/operator ClusterRoles according to their
job; do not grant the product controller role to CLI submitters.

Product and Stage CRDs are retained on uninstall. Existing CRDs without the
expected `simulation.kasim.io/ownership-root` annotation cause installation to
fail instead of being silently adopted. Other KWOK installations and arbitrary
user objects are outside the release ownership set and are never selected for
deletion.
