# ADR 0001: Expose fidelity modes, not simulation backends

Status: Accepted

## Context

Kubernetes exposes accelerator behavior through several contracts with different runtime requirements. Synthetic Node status is sufficient to exercise scheduler behavior, Dynamic Resource Allocation (DRA) can exercise additional control-plane behavior, while Device Plugin registration, kubelet allocation, NUMA admission, CDI injection, and DRA node preparation require a real kubelet. Treating all of these as one generic "GPU mock" would overstate what a successful Scenario Instance proved.

KWOK is the strongest current substrate for high-scale Synthetic Node lifecycle,
but it is an implementation dependency rather than a user intent. The substrate
prototype compared pinned KWOK with a minimal native Node and Lease reconciler
in the same mixed cluster. Both exposed scheduler-visible capacity safely, but
only KWOK advanced bound Pods to `Running`; the native candidate left them
`Pending` because it did not implement Pod lifecycle behavior. The prototype
also showed that a placement-unconstrained runtime controller can be scheduled
onto one of its own Synthetic Nodes after restart and deadlock there.

## Decision

The product exposes exactly two Fidelity Modes:

- `scheduling` is the default. It creates owned Synthetic Nodes and reports Kubernetes-visible labels, scalar extended-resource capacity and allocatable values, Ready and Lease state, scheduler placement, resource exhaustion, and aggregate health changes. It does not claim Device Plugin registration, device-ID allocation, kubelet `Allocate`, NUMA admission, CDI injection, container device access, or accelerator computation.
- `dra-control-plane` is explicit and version-gated. It exercises supported DRA APIs, including DeviceClass selection, ResourceSlice inventory, ResourceClaim allocation or reservation, and Pod scheduling. Missing APIs, permissions, or capabilities fail closed; the product never silently degrades to `scheduling`. It does not claim node preparation, CDI, node-local health streaming, or container device access.

Node-runtime protocol fidelity is not a product Fidelity Mode. A separate End-to-End Test Harness may create a disposable kind cluster and run fake Device Plugin or DRA node agents against real kubelets. The product CLI continues to operate only on an existing, explicitly selected Simulation Target.

The public Module is the Scenario Instance lifecycle:

```go
type ScenarioRuntime interface {
    Apply(context.Context, ApplyRequest) (Snapshot, error)
    Observe(context.Context, InstanceKey) (Snapshot, error)
    Delete(context.Context, InstanceRef) (Snapshot, error)
}
```

The interface requires an explicit kubeconfig and context, records a target-cluster fingerprint, and returns machine-readable requested, achieved, guaranteed, excluded, and unavailable fidelity surfaces. `Apply` and `Delete` are idempotent, ownership-bounded operations. They never adopt or mutate a pre-existing real Node.

All CLI shortcuts and files compile to one vendor-neutral Scenario and then to one internal desired graph. Two internal resource-projection Adapters render that graph:

- an extended-resource projection for `scheduling`;
- a DRA projection for `dra-control-plane`.

The projections cannot both publish the same resource identity on the same Synthetic Node. Kubernetes API access is isolated behind a cluster port with a real client-go Adapter and deterministic test Adapters.

A project-pinned KWOK release is the only maintained Synthetic Node
implementation in the first release. The project pins and verifies its release
artifacts; KWOK annotations, Stages, object choreography, and version details do
not enter the Scenario, Vendor Profile, CLI, or public Module interface. The
minimal native challenger is rejected because matching the required behavior
would mean taking ownership of Pod lifecycle semantics in addition to Node and
Lease reconciliation. No `SyntheticRuntime` Seam is introduced until a second
maintained implementation exists.

Every in-cluster runtime controller has hard required Node affinity excluding
Nodes carrying the simulator managed-by label. This prevents a controller from
bootstrapping onto its own Synthetic Node. Runtime API reads and writes remain
restricted to exact Scenario Instance ownership selectors, and the release
validation audit must show no mutating controller request against a
pre-existing real Node or its Lease.

kind is an End-to-End Test Harness, not a product backend.

The implementation may build an internal immutable plan for zero-write preflight, auditing, ordering, and stale-target checks. `Plan` is not a public operation until a second real caller demonstrates that need.

## Consequences

- Users choose the Kubernetes behavior they need to test, not a backend.
- A successful result cannot be mistaken for real Device Plugin, kubelet, CDI, or accelerator-compute behavior.
- Vendor additions remain data in Vendor Profiles instead of backend branches.
- Replacing KWOK is local to the Synthetic Node implementation.
- The project does not maintain a partial kubelet or Pod lifecycle emulator.
- Runtime control Pods cannot be scheduled onto simulator-managed Nodes.
- DRA and scalar extended resources share Scenario semantics while retaining separate Kubernetes-specific Adapters.
- The product fails closed on target identity, capability, permission, evidence, ownership, and projection conflicts.
- The provisional 1,000-Node and 8,000-Accelerator target remains a prototype acceptance target, not a capability claim.

## Evidence

- [Kubernetes accelerator resource contract](https://github.com/LinkMaq/kube-accelerator-sim/issues/8)
- [Simulation backend landscape](https://github.com/LinkMaq/kube-accelerator-sim/issues/6)
- [Vendor and model evidence catalog](https://github.com/LinkMaq/kube-accelerator-sim/issues/10)
- [Synthetic Node substrate prototype](https://github.com/LinkMaq/kube-accelerator-sim/issues/12)
