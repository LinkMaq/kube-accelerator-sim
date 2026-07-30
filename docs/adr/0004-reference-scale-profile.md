# ADR 0004: Maintain a 1,000-Node reference scale profile

Status: Accepted

## Context

The default `scheduling` Fidelity Mode must remain useful for platform
scheduling tests that are materially larger than a laptop-sized smoke test.
The provisional target was 1,000 Synthetic Nodes with roughly 8,000
Accelerators, but it was not a supportable capability statement until the
selected KWOK substrate was measured for convergence, observation, health
updates, workload scheduling, controller failure, cleanup, memory, and storage
behavior.

Two consecutive prototype trials used a disposable single-control-plane kind
cluster with four Docker CPUs and 7.753 GiB Docker memory. Each trial created
1,000 Synthetic Nodes, eight `nvidia.com/gpu` per Node, exact ownership on
1,000 Leases, 100 health changes, and 100 Pods requesting eight Accelerators
each. Both trials completed without reducing the requested object count or
leaking a Node or Lease.

The slower or higher observations were:

- 124.108 seconds from Node submission through all Nodes Ready and all Leases
  ownership-labelled;
- 600 milliseconds p95 for ten sequential full-instance observations;
- 4.114 seconds to change aggregate health on 100 Nodes and 2.617 seconds to
  restore it;
- 17.360 seconds to create and run 100 representative full-node Accelerator
  Pods;
- 69 seconds for controller restart and all-Node Ready recovery;
- less than 61 seconds for end-to-end exact-UID cleanup verification;
- 1.323 GiB peak kind control-plane container memory.

The etcd database file grew from roughly 1 MiB to 71–78.5 MiB after successful
cleanup. Deleting live objects does not immediately compact or defragment etcd
history and tombstones.

## Decision

The project maintains a `scheduling` reference scale profile with:

- exactly 1,000 Ready Synthetic Nodes;
- exactly 8,000 capacity and allocatable Accelerators, represented as eight per
  Node;
- exact Scenario Instance ownership on every Synthetic Node and Lease;
- a 100-Node aggregate health-loss and recovery revision;
- 100 representative Pods requesting one full Node of Accelerator capacity;
- a runtime-controller outage and recovery probe;
- exact-UID cleanup with zero live owned Nodes or Leases.

The scale profile runs as a dedicated release-validation job, not as a normal
unit or pull-request test. Its reference End-to-End Test Harness provides at
least four Docker CPUs and 8 GiB Docker memory. A release candidate passes only
when two consecutive trials each meet every gate:

- apply acceptance through Ready and exact Lease ownership: at most 180
  seconds;
- full Scenario Instance observation p95 over ten sequential samples: at most
  2 seconds;
- 100-Node health loss or recovery: at most 15 seconds per revision;
- all 100 representative Pods Running: at most 60 seconds;
- controller restart and full Ready recovery: at most 120 seconds;
- exact-UID Node and Lease cleanup: at most 120 seconds;
- peak kind control-plane container memory: at most 2 GiB;
- zero API errors, controller crashes, ownership leaks, or silent count
  reductions.

The maintained guarantee is scoped to this reference profile and environment.
Smaller environments may run the product but are not evidence for the
1,000/8,000 guarantee. Larger environments and scenarios remain unclaimed
until separately measured.

A successful Scenario Instance deletion proves that no owned live API object
remains. It does not claim etcd file shrinkage. The product never invokes etcd
compaction or defragmentation in a user-owned Simulation Target; those remain
cluster-operator maintenance.

## Consequences

- The initial scale target becomes a repeatable release gate rather than an
  aspirational number.
- Performance regressions have explicit latency, recovery, cleanup, and memory
  failure boundaries.
- The default pull-request path remains fast; scale confidence comes from a
  separately provisioned job.
- Completion is judged by exact requested and observed object counts, not by
  command exit alone.
- Cleanup status does not overstate control over the Simulation Target's etcd
  storage lifecycle.
- The reference profile does not imply real Device Plugin, kubelet allocation,
  container device access, or accelerator computation fidelity.

## Evidence

- [Scale prototype decision](https://github.com/LinkMaq/kube-accelerator-sim/issues/4)
- [Captured prototype and trial summary](https://github.com/LinkMaq/kube-accelerator-sim/tree/prototype/scale-issue-4/prototype/scale)
