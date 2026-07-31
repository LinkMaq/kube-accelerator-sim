# Troubleshooting and security

## Common failures

| Diagnostic | Meaning | Safe response |
| --- | --- | --- |
| `InvocationInvalid` requesting target flags | A connected command omitted `--kubeconfig` or `--context` | Supply both; the CLI never falls back to current context or environment |
| `TargetInvalid` or target fingerprint change | The named context resolves differently or `kube-system` identity changed | Stop and verify the exact API server, CA digest, and cluster identity |
| Kubernetes version rejection | The server is outside 1.30–1.36 | Use a supported target; do not bypass preflight |
| DRA unavailable | Stable DRA is unavailable or the server is outside 1.34–1.36 | Use `scheduling` if that matches the intended test, otherwise change target |
| Provisional profile rejection | The Scenario did not explicitly accept provisional evidence | Review the source evidence, then set acceptance intentionally or use a verified profile |
| `OwnershipConflict` | A desired object name is owned by another UID or lacks exact ownership | Investigate the object; do not relabel or adopt it casually |
| `CleanupBlocked` | An unowned Pod remains bound to a Synthetic Node | Follow the bounded cleanup process below |
| `Overcommitted` | New allocatable capacity is below existing requests | Keep workloads in place and decide whether to restore capacity |

Always preserve the JSON diagnostic and the latest status receipt. They carry
the retryability and revision-acceptance boundary; a network error after
acceptance is not equivalent to rejection.

## Safe blocked deletion

`kasim delete` never deletes or evicts an unowned Pod. When it returns
`CleanupBlocked`, copy the bounded blocker Node/Pod references from the status
receipt and inspect that exact Node:

The release compatibility harness contains the reproducible real-cluster
[foreign-Pod setup and blocked-delete assertion](../../test/e2e/compatibility_test.go).
The operator workflow below begins after that safety condition is observed; it
does not create or remove a user workload on the operator's behalf.

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  get pods --all-namespaces \
  --field-selector spec.nodeName=replace-with-blocked-node \
  -o wide
```

Determine who owns each workload and coordinate its migration or deletion
through the workload's normal operator. That action is outside `kasim` and may
have application consequences. Do not change simulator ownership labels,
remove finalizers, patch the Node, or look for a force flag.

After the foreign Pod is gone, fetch the Scenario status again and retry
deletion with the same exact instance UID and its current desired generation:

```sh
./dist/kasim status replace-with-instance-name \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json

./dist/kasim delete replace-with-instance-name \
  --instance-uid 'replace-with-exact-status-instance-uid' \
  --expected-generation 'replace-with-exact-positive-generation' \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

Successful deletion includes an ownership proof that no allowlisted object
with the exact instance UID remains.

## Permission model

Bind users and automation to the chart's observer or lifecycle-operator roles.
Do not grant the controller, KWOK controller, Stage installer, CRD mutation,
RBAC mutation, Secret access, impersonation, Pod eviction, or service-account
token permissions to CLI submitters.

The controller mutates only exact simulator-owned Nodes, Leases, DRA inventory,
and helper objects after ownership preconditions pass. Runtime Pods use a
non-root user, read-only root filesystem, dropped Linux capabilities, and hard
affinity that excludes Synthetic Nodes.

Use a dedicated administrative identity for Helm installation and upgrades.
The chart refuses to adopt same-name CRDs or runtime resources with an
incompatible ownership root.

## Fidelity and data boundary

Scenario files contain portable desired state, not kubeconfigs, credentials,
raw Kubernetes objects, generated device IDs, or arbitrary patches. Receipts
redact credentials and report the canonical API endpoint, target fingerprint,
and CA digest separately.

The simulator does not provide device access, does not execute accelerator
compute, does not install vendor drivers, does not provide vendor telemetry,
does not simulate NUMA topology, and does not inject CDI devices. Treat all
generated device identities as deterministic simulator identities, never
vendor hardware serial numbers.
