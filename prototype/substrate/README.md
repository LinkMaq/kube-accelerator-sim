# PROTOTYPE — Synthetic Node substrate

This is throwaway code for issue
[#12](https://github.com/LinkMaq/kube-accelerator-sim/issues/12).
It answers one question:

> Can a pinned KWOK installation, wrapped by strict Scenario Instance
> ownership, safely expose scheduler-visible Accelerator resources in an
> existing mixed Kubernetes cluster, survive updates and restart, and clean up
> without writing to real Nodes; and is a minimal project-owned reconciler
> materially simpler?

The experiment creates a disposable two-node kind cluster with an API audit
policy. It runs the KWOK and native candidates sequentially against the same
control plane, creates owned Synthetic Nodes, schedules representative
Accelerator Pods, changes aggregate health, restarts the controller, and removes
the instance. It compares stable real-Node snapshots and rejects any mutating
audit event from either controller against a real Node or its Lease.

The product CLI does not gain cluster-lifecycle responsibility from this
prototype. kind is used only as the End-to-End Test Harness described in the
architecture decisions.

## Run

Requirements: Intel macOS, Docker, `kubectl`, `curl`, `jq`, and `shasum`.

```sh
./prototype/substrate/run.sh
```

The command downloads a checksum-pinned kind binary into a temporary directory.
All kubectl calls use the generated kubeconfig and explicit kind context. The
cluster and temporary files are removed on exit. Set `KEEP_CLUSTER=1` only when
debugging.

Runtime logs are written to `prototype/substrate/.results/`, which is ignored.
The captured successful run and verdict are in
[`EVIDENCE.md`](EVIDENCE.md). The validated architecture decision is also
recorded on the issue and in `main`; the prototype itself is never merged.
