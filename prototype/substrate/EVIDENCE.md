# Prototype evidence

Run: `20260730T014445Z-46261`

Environment:

- kind `v0.32.0`
- Kubernetes `v1.36.1`
- KWOK `v0.8.0`
- Intel macOS with Docker

Both candidates passed scheduler binding for single-card, multi-card,
heterogeneous, multi-node spread, health degradation/recovery, and exhaustion
cases. Both recovered Node readiness after a controller restart, removed only
objects carrying the exact Scenario Instance UID, left the stable state of the
two real kind Nodes and Leases unchanged, and produced zero audited mutating
writes to those real objects.

The stable-state SHA-256 was
`2aea57d36346dd1450707a3a1530bc33ead85716f6a8df1eeb56cd8f663f08da`
before either candidate, after KWOK, and after the native candidate.

The material difference was Pod lifecycle:

| Candidate | Scheduled Pods |
| --- | --- |
| KWOK | `Running` |
| Minimal native reconciler | `Pending` after binding |

The native image was 2,554,391 bytes and used three RBAC rule groups, but it
only maintained Node status and Leases. Reaching KWOK-equivalent behavior would
also require implementing Pod status and lifecycle behavior. The prototype
also exposed a shared safety constraint: every runtime controller must require
the `sim.kube-accelerator.io/managed-by` Node label to be absent, otherwise a
restarted controller can be scheduled onto its own Synthetic Node and deadlock.

Verdict: use pinned KWOK as the default Synthetic Node runtime behind a
project-owned adapter. Keep a narrow runtime interface so a different backend
can be added later, but do not build a custom reconciler for the first release.
