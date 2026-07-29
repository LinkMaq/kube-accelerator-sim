# ADR 0003: Model simulations as revisioned Scenario Instances

Status: Accepted

## Context

The simulator must express single-node, multi-node, multi-Accelerator, and heterogeneous scenarios while supporting safe health, capacity, and scale changes. A command-oriented model would make retries ambiguous, while an embedded timeline language would mix orchestration with desired state and complicate crash recovery. Kubernetes also has no transaction spanning Nodes, Leases, status, and DRA objects, so identity and ownership must survive partial convergence.

## Decision

A portable **Scenario** is target-independent desired state. Every file and CLI shortcut compiles to the same canonical Scenario before any cluster write. It contains no kubeconfig, context, backend choice, KWOK field, Kubernetes object manifest, generated device ID, or runtime workflow.

Applying a Scenario creates or updates one target-scoped, cluster-level **Scenario Instance**. Its name is unique within the Simulation Target; its server-assigned UID, target fingerprint, creation identity, and Fidelity Mode are immutable. The instance records the canonical spec digest, resolved Vendor Profile revisions and digests, desired generation, observed generation, and an inventory of owned objects.

Each accepted change creates an immutable **Scenario Revision**. Reapplying the same canonical digest is a no-op. A different digest requires an expected generation and increments the generation, preventing concurrent CLI operations from silently overwriting one another.

Dynamic health, capacity, placement, and scale changes are declarative revisions of the same Scenario Instance. The first version does not embed timers, event graphs, or a workflow DSL. A CLI health or scale shortcut compiles the requested change into a new revision; the End-to-End Test Harness can reproduce a sequence by applying revisions and recording their receipts.

### Canonical Scenario shape

The eventual API encoding may add compatibility metadata, but the canonical model preserves this structure:

```yaml
metadata:
  name: mixed-training-lab
spec:
  fidelity: scheduling
  acceptance:
    provisionalProfiles: false
  nodeGroups:
    - name: nvidia-workers
      replicas: 4
      node:
        capacity:
          cpu: "64"
          memory: 256Gi
          pods: "110"
        placement:
          zone: lab-a
        labels: {}
        taints: []
      acceleratorPools:
        - name: training
          profile:
            id: nvidia
            revision: 2026-07-29
            digest: sha256:...
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          variant: {}
          count: 8
          healthy: 8
```

A **Node Group** is homogeneous: every replica has the same base capacity, placement, labels, taints, and Accelerator Pools. Replica indices are stable; scale-down selects the highest indices first. Different per-node composition is modeled by another Node Group rather than overrides.

An **Accelerator Pool** is homogeneous on every Node Group replica. It has a stable pool name, pinned profile, model, contract, resource, source-backed variant, total count, and healthy count. `replicas`, `count`, and `healthy` may be zero to represent a fully scaled-down or unavailable desired state; `healthy` must be an integer between zero and `count`.

All references must resolve before writes. Provisional profiles require explicit acceptance. Custom profiles are submitted and pinned by digest. The compiler rejects duplicate group or pool names, unresolved models, invalid quantities, reserved labels, conflicting taints, unsupported profile capabilities, and output conflicts.

### Representation rules

- `scheduling` projects each Accelerator Pool to source-backed Node labels and scalar capacity or allocatable quantities. Capacity is `count`; allocatable is `healthy`. A health-only change never alters capacity and never evicts or reassigns an already bound Pod.
- A capacity reduction may leave current Pod requests above allocatable. Existing Pods remain untouched and the instance reports `Overcommitted`; only new scheduling is affected.
- `dra-control-plane` deterministically derives simulator-owned device identities from instance UID, group, replica, pool, and device index. Those identities are stable across retries but are never presented as vendor hardware IDs.
- One Scenario Instance uses one Fidelity Mode. Every selected Resource Contract must support it; there is no per-group fallback.
- Multiple Accelerator Pools may coexist on one Node Group only when their rendered resources and identity signals are unambiguous.
- In `scheduling`, two different models that collapse to the same scalar resource name on one Node Group are rejected. They must use separate Node Groups or a source-backed distinct resource. DRA may represent them together when its device attributes distinguish them.
- Conflicting values for the same vendor label or DRA attribute are rejected. Simulator-owned normalized identity remains separate from vendor identity.
- A change to target, instance identity, Fidelity Mode, or ownership root requires replacement. A change that replaces a resource identity is reconciled as removal plus addition and cannot remove a node or resource that still hosts unowned Pods.

### Reconciliation contract

`Apply` performs canonical validation, profile resolution, capability discovery, RBAC preflight, target fingerprint validation, and an ownership-conflict check before creating a revision. A preflight rejection has zero writes.

After acceptance, convergence is asynchronous and idempotent:

1. Persist the Scenario Revision and its ownership root.
2. Create new Synthetic Nodes with scheduling closed.
3. Reconcile identity, base resources, Accelerator resources or DRA inventory, Ready state, and Lease state.
4. Open scheduling only after all required surfaces for that node are observed.
5. Apply in-place health and capacity updates without touching bound Pods.
6. For replacement or scale-down, close scheduling first, then remove only objects proven to belong to the instance.

Every object carries the instance UID, desired generation, managed-by identity, and the strongest legal owner reference. Cleanup and stale-object detection use the exact UID plus an allowlist of object kinds; a name prefix or generic KWOK label is never sufficient.

An object with the desired name but a different or missing ownership UID produces `OwnershipConflict`. The simulator never adopts it, even if it otherwise looks synthetic. It never patches, labels, taints, changes status on, or deletes a pre-existing real Node.

Crashes and transport failures may leave a partial generation, but the durable instance records applied, pending, failed, and stale inventory. Retrying the same revision resumes convergence without allocating different node or device identities.

### Status contract

The bounded status snapshot contains:

- instance UID and target fingerprint;
- desired and observed generation plus revision digest;
- resolved profile IDs, revisions, digests, classes, and evidence receipts;
- requested Fidelity Mode and per-surface achieved, excluded, unavailable, or out-of-scope results;
- aggregate Node Group and Accelerator Pool desired and observed counts;
- owned-object counts and capped diagnostics;
- typed conditions and the last transition time.

The lifecycle phase is one of `Pending`, `Reconciling`, `Ready`, `Failed`, or `Deleting`. `Ready` means the observed generation equals the desired generation and every required fidelity assertion passed. A deliberately requested `healthy < count` state can therefore be Ready; simulated accelerator failure is desired data, not controller degradation.

Conditions distinguish `Progressing`, `Retrying`, `Overcommitted`, `OwnershipConflict`, `CleanupBlocked`, and `FidelitySatisfied`. A non-retryable validation or capability error fails before revision acceptance; a post-acceptance failure retains the instance and its inventory for recovery.

### Cleanup guarantees

Deletion and scale-down first make the affected Synthetic Nodes unschedulable. Instance-owned test artifacts may be removed, but the product never deletes or evicts a Pod it does not own.

If unowned Pods remain bound to a Synthetic Node, node removal pauses with `CleanupBlocked` and reports the blocking references. Cleanup resumes after those workloads are removed. Destructive force behavior, if added later, must be a separate explicit safety decision; it is not the default contract.

Once no foreign workload blocks cleanup, the reconciler removes only allowlisted objects with the exact instance UID, in dependency order, and then removes the ownership root. A successful delete receipt proves that no owned Node, Lease, DRA inventory, or helper object remains. It makes no claim about unrelated user objects.

## Scenario coverage

The model represents the required shapes without vendor-specific branches:

- single-node single-Accelerator: one Node Group replica and one Pool with count one;
- single-node multi-Accelerator: one replica and a Pool count greater than one;
- multi-node multi-Accelerator: multiple replicas of one homogeneous Node Group;
- heterogeneous nodes: multiple Node Groups with different Pools;
- multiple compatible Accelerator types on one node: multiple non-conflicting Pools;
- full or partial health loss: a new revision with lower `healthy`;
- inventory change: a new revision with a different `count`;
- node scale change: a new revision with a different `replicas`.

## Consequences

- All user workflows share one idempotent desired-state path.
- Updates are auditable and recoverable without a temporal scripting engine.
- Ownership remains exact even after interrupted apply or delete operations.
- Scalar scheduling fidelity refuses per-device distinctions the Kubernetes scheduler cannot observe.
- Workload lifecycle stays outside the Scenario core; the End-to-End Test Harness may own representative test workloads without turning the product into a general workload deployer.
