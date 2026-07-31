# Upgrade and rollback

Runtime installation is a separate Helm workflow against an existing,
explicitly selected target. Before changing a release, retain the current
release receipt, status receipts for every Scenario Instance, chart values,
and Helm history.

```sh
helm get values kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  -o yaml

helm history kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system
```

## Pre-upgrade checks

1. Verify the new CLI archive, controller image, and chart using the published
   checksums, signatures, attestations, and `release-receipt.json`.
2. Confirm the target is still within Kubernetes 1.30–1.36 and that any DRA
   Scenario is within 1.34–1.36.
3. Run client dry-run on every stored Scenario with the new CLI.
4. Compare profile revisions/digests and compatibility locks. A changed
   catalog digest is an input change, not an automatic migration.
5. Confirm every existing Scenario is converged and has no
   `CleanupBlocked`, `OwnershipConflict`, or retrying condition.

## Upgrade

Use the chart from the exact verified release:

```sh
helm upgrade kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --reuse-values \
  --wait
```

After the rollout, inspect Helm status and each Scenario through the new CLI:

```sh
helm status kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system

./dist/kasim status replace-with-instance-name \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

Do not submit a new Scenario revision merely to test the upgrade. First verify
that the controller observes the existing desired generation unchanged.

## Rollback

If the controller or chart rollout fails, use the exact earlier Helm revision
recorded by `helm history`:

```sh
helm rollback kasim-runtime replace-with-helm-revision \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

Helm does not roll back CRDs. The v1 chart retains CRDs, and an incompatible
pre-existing CRD fails ownership checks instead of being adopted. If a future
release documents a non-backward-compatible transport migration, follow that
release's migration procedure; do not manually rewrite Scenario Instance
storage or ownership labels.

Rollback affects the shared runtime release only. It must not remove Scenario
Instances, Synthetic Nodes, user workloads, or the cluster. Verify all
Scenario status receipts after rollback before performing another mutation.
