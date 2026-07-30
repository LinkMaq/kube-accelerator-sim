# Stable DRA v1 Control-Plane Projection

Research date: 2026-07-30

Implementation ticket:
[GitHub issue #24](https://github.com/LinkMaq/kube-accelerator-sim/issues/24)

## Executive recommendation

Implement `dra-control-plane` as a narrow `resource.k8s.io/v1` projection with
these boundaries:

1. Require Kubernetes 1.34 or newer, discovery of the stable v1 resources, and
   the exact permissions below before accepting a Scenario revision. Do not
   fall back to a beta API or to scalar extended resources.
2. Let the Scenario reconciler manage only its owned `DeviceClass` and
   `ResourceSlice` objects. It may observe `ResourceClaim` and Pod objects, but
   it must never write `ResourceClaim.status`, reservations, Pod bindings, or
   Pod status.
3. Treat the Kubernetes scheduler's writes to
   `ResourceClaim.status.allocation` and `.reservedFor`, followed by Pod
   binding, as the control-plane proof. Never synthesize those fields.
4. Use a portable v1 subset: exact-count requests, CEL selectors, per-Node
   slices, ordinary device attributes/capacities, allocation results, and Pod
   claim references. Omit every independently feature-gated DRA extension from
   the initial contract.
5. Publish deterministic simulator device names and explicit
   `simulation.kasim.io` attributes. They are scheduler-visible simulated
   identities, not UUIDs, serial numbers, PCI addresses, or other vendor
   hardware identifiers.
6. Report node preparation, kubelet DRA RPCs, CDI, container device access,
   device health streaming, and accelerator computation as excluded even when
   allocation, reservation, and Pod binding succeed.

Kubernetes 1.34 documents core DRA as stable and enabled by default. The
tagged feature-gate source shows that the `DynamicResourceAllocation` gate is
GA/default-on in 1.34 but is only locked to its default in 1.35. Therefore a
1.34 server can still have the gate or API disabled, and discovery remains a
mandatory check on every target.
([v1.34 DRA documentation](https://v1-34.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
[v1.34.10 feature-gate source](https://github.com/kubernetes/kubernetes/blob/v1.34.10/pkg/features/kube_features.go#L1269-L1273))

## Evidence boundary

This report uses the immutable Kubernetes v1.34.10 source tag as the minimum
implementation contract and current official documentation only for concepts
that do not weaken that floor.

The stable API can prove inventory publication, scheduler selection,
allocation, reservation, and Pod placement. Those observations do not prove
that a node-local DRA driver exists or that a container can use a device. The
official workflow assigns device preparation to the DRA driver and kubelet
after the scheduler has allocated the claim and selected a Node.
([v1.34 DRA workflow](https://v1-34.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#how-resource-allocation-with-dra-works))

## Version and feature-gate contract

| Target | Decision |
| --- | --- |
| Kubernetes 1.30–1.33 | Reject `dra-control-plane` before revision acceptance. Ignore any discovered alpha or beta resource APIs. |
| Kubernetes 1.34 | Require server minor, discovery of `resource.k8s.io/v1`, required verbs, and successful server dry-runs. The GA gate is default-on but can still be disabled. |
| Kubernetes 1.35–1.36 | Require the same discovery, authorization, and dry-run checks. Upstream locking the core gate does not prove distribution-specific admission or permissions. |
| Newer than the release-tested ceiling | Fail closed under the project's compatibility policy until added to the release matrix. |

The stable API package being served does not make every field in it part of
the stable portable contract. Kubernetes 1.34 has separately gated DRA
features with different maturity and defaults:

| Extension | Kubernetes 1.34 state | Initial projector |
| --- | --- | --- |
| Prioritized request list | Beta, default-on | Excluded; use `exactly` only |
| Admin access | Beta, default-on | Excluded |
| ResourceClaim device status | Beta, default-on | Observe neither readiness nor health from it |
| Partitionable devices | Alpha, default-off | Excluded |
| Device taints | Alpha, default-off | Excluded |
| DRA-backed extended resources | Alpha, default-off | Excluded |
| Consumable capacity | Alpha, default-off | Excluded |
| Device binding conditions | Alpha, default-off | Excluded |

The tagged feature definitions are the source for these 1.34 states.
([v1.34.10 DRA feature definitions](https://github.com/kubernetes/kubernetes/blob/v1.34.10/pkg/features/kube_features.go#L1202-L1235))
The v1 API types themselves annotate the corresponding gated fields, including
per-device node selection, taints, binding conditions, admin access, and
consumable-capacity requests.
([v1.34.10 ResourceSlice and Device types](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L98-L209),
[v1.34.10 request types](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L768-L930))

Preflight must reject a required missing core field or operation as
`UnsupportedFidelity` or `CapabilityUnavailable`. It must not enable a feature
gate, install a DRA driver, or substitute `v1beta1`/`v1beta2`.

## Exact API contract

### `DeviceClass`

`DeviceClass` is cluster-scoped. Its spec is mutable and has two core inputs:
an atomic list of CEL selectors and an optional list of opaque driver
configuration. The initial projector should generate selectors and omit
configuration because there is no node driver to consume it.
([v1.34.10 DeviceClass types](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L1640-L1703))

The generated class must:

- have a deterministic, instance-specific DNS-subdomain name;
- carry the exact Scenario Instance UID, desired generation, managed-by label,
  and a controller owner reference to the cluster-scoped Scenario Instance;
- select the exact source-backed DRA driver name;
- select only explicitly simulated and currently allocatable devices; and
- omit `config` and the gated `extendedResourceName`.

A baseline selector can use:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: <deterministic-instance-class>
spec:
  selectors:
    - cel:
        expression: >-
          device.driver == "<source-backed-driver>" &&
          device.attributes["simulation.kasim.io"].simulated == true &&
          device.attributes["simulation.kasim.io"].allocatable == true
```

The official cluster-admin example uses `device.driver` in the same way.
([official DeviceClass example](https://kubernetes.io/docs/tasks/configure-pod-container/assign-resources/set-up-dra-cluster/#create-deviceclasses))
CEL references unknown fields as errors, so the projector must publish both
simulator attributes on every device selected by its class rather than rely on
missing-value behavior.
([v1.34.10 CEL selector contract](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L1078-L1134))

### `ResourceSlice`

`ResourceSlice` is cluster-scoped and has no status subresource. The stable
minimum shape is:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  name: <deterministic-slice-name>
spec:
  driver: <source-backed-driver>
  pool:
    name: <deterministic-pool-name>
    generation: <scenario-generation>
    resourceSliceCount: <complete-shard-count>
  nodeName: <owned-synthetic-node-name>
  devices:
    - name: <deterministic-simulated-device-name>
      attributes:
        simulation.kasim.io/simulated:
          bool: true
        simulation.kasim.io/allocatable:
          bool: true
```

The renderer must set exactly one of `nodeName`, `nodeSelector`, `allNodes`, or
the gated per-device selection mode. For node-local simulated accelerators,
use the exact owned Synthetic Node name. `driver`, pool name, and node name
are immutable. A pool generation is complete only after the advertised
`resourceSliceCount` slices for that generation are visible.
([v1.34.10 ResourceSliceSpec](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L98-L209),
[v1.34.10 ResourcePool](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L211-L241))

Each slice may contain at most 128 devices. A device name is unique within its
driver/pool and must be a DNS label. The Kubernetes-wide device identity is
the tuple `(driver, pool, device)`, independent of which slice currently
contains it.
([v1.34.10 ResourceSlice and Device contract](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L44-L68),
[v1.34.10 Device fields and limits](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L243-L320))

Attribute and capacity keys may be unqualified only when owned by the driver.
A third-party key must include its domain. Consequently the simulator flags
must be fully qualified as `simulation.kasim.io/simulated` and
`simulation.kasim.io/allocatable`; they must not be emitted as unqualified
vendor attributes.
([v1.34.10 QualifiedName rules](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L544-L558))

### `ResourceClaim`

`ResourceClaim` is namespaced and its spec is immutable. The portable request
uses one `DeviceRequest` with `exactly`, a required DeviceClass name,
`ExactCount`, and a positive count. Additional claim selectors may narrow the
class, but the maintained E2E fixture should keep the class contract itself
under test.
([v1.34.10 ResourceClaim and DeviceClaim](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L690-L760),
[v1.34.10 exact request](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L768-L879))

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  name: <fixture-claim>
  namespace: <fixture-namespace>
spec:
  devices:
    requests:
      - name: accelerator
        exactly:
          deviceClassName: <deterministic-instance-class>
          allocationMode: ExactCount
          count: 1
```

The Scenario reconciler does not create this object. Product users or the E2E
harness create claims under their own identity. The first projector does not
support `ResourceClaimTemplate`; adding templates would add a separate
namespaced lifecycle contract and is unnecessary for proving one exact
allocation path.

### Allocation and reservation

The scheduler owns the proof fields:

- `status.allocation.devices.results[]` records `request`, `driver`, `pool`,
  and `device`;
- `status.allocation.nodeSelector`, when present, constrains Nodes that can
  access the result; and
- `status.reservedFor[]` records the exact same-namespace consumer by API
  group, resource, name, and UID.

`reservedFor` is a UID-keyed map with at most 256 entries. A Pod that references
a claim but is not reserved for it must not start, and a reserved claim must
not be deallocated.
([v1.34.10 ResourceClaimStatus](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L1345-L1419),
[v1.34.10 AllocationResult](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L1422-L1510))

The v1.34 scheduler implementation calculates allocation during filtering,
adds the built-in delete-protection finalizer for a newly allocated claim, and
atomically writes allocation plus the Pod reservation during `PreBind`.
([v1.34.10 scheduler Reserve](https://github.com/kubernetes/kubernetes/blob/v1.34.10/pkg/scheduler/framework/plugins/dynamicresources/dynamicresources.go#L1120-L1217),
[v1.34.10 scheduler PreBind update](https://github.com/kubernetes/kubernetes/blob/v1.34.10/pkg/scheduler/framework/plugins/dynamicresources/dynamicresources.go#L1537-L1594))

The projector must reject as invalid evidence:

- an allocation whose driver/pool/device tuple is not in the exact owned,
  complete pool generation;
- a result count or request name that does not match the claim;
- a reservation that names the Pod but has a different Pod UID;
- a bound Pod whose Node is outside the allocation's node selector; or
- any result observed only from an alpha/beta ResourceClaim endpoint.

### Pod scheduling

The Pod and ResourceClaim must share a namespace. The Pod's
`spec.resourceClaims[]` entry names the claim, and each container that should
receive it uses `resources.claims[]` to name all or one specific device
request.
([v1.34.10 PodResourceClaim](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/core/v1/types.go#L4439-L4475),
[v1.34.10 container ResourceClaim reference](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/core/v1/types.go#L2870-L2890))

The observed control-plane sequence is:

1. the owned complete ResourceSlice pool and DeviceClass exist;
2. an independently owned ResourceClaim and Pod reference that class;
3. the scheduler selects an eligible `(driver, pool, device)` tuple;
4. the scheduler writes allocation and the exact Pod reservation;
5. the scheduler binds the Pod to an eligible owned Synthetic Node.

The official DRA workflow assigns those filtering, allocation, and placement
steps to Kubernetes.
([v1.34 DRA workflow](https://v1-34.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#workflow-for-kubernetes))

For the product runtime, DeviceClass and complete ResourceSlice inventory are
always-required readiness surfaces. Allocation, reservation, and Pod placement
become required surfaces when an exact claim/Pod observation is selected.
Release support for `dra-control-plane` still requires a real scheduler E2E
fixture on every supported minor; absence of a user workload must not be
reported as a successful per-workload allocation.

## Deterministic identity and inventory

Use the repository's existing `SimulatedDeviceID(instanceUID, group, replica,
pool, deviceIndex)` derivation unchanged. Its `kasim-device-` prefix and
content hash are stable for retry and recovery and fit the DNS-label
requirement.

Derive every related name from explicit domain-separated inputs:

| Object | Required identity inputs |
| --- | --- |
| DeviceClass | Instance UID, Accelerator Pool identity, source-backed driver |
| Resource pool | Instance UID, Node Group, replica, Accelerator Pool |
| ResourceSlice | Resource pool identity, pool generation, shard index |
| Device | Instance UID, Node Group, replica, Accelerator Pool, device index |

Rules:

- The DRA driver string comes from the resolved Vendor Profile contract. It is
  not generated from a display name.
- A source-backed vendor attribute may be emitted only with its exact key,
  type, and value semantics. Otherwise omit it or use the
  `simulation.kasim.io` domain.
- Never populate vendor UUID, serial, PCI BDF, firmware, topology, or hardware
  health keys with simulator hashes.
- Include `simulation.kasim.io/simulated=true` on every device and expose the
  same fact in the fidelity receipt.
- Publish exactly `count` stable device indices. Map aggregate `healthy`
  deterministically to `simulation.kasim.io/allocatable=true` for indices
  `[0, healthy)` and false for the remainder. The generated DeviceClass
  requires `allocatable == true`, so new allocations exclude unavailable
  simulated devices while existing claims are neither evicted nor rewritten.
- Describe that field as simulated control-plane eligibility, not node-local
  device health. The core stable API has no ungated node-driver health stream.

Shard devices by stable index into groups of at most 128. Use one empty slice
when `count == 0` so that a complete zero-device pool has an observable
generation and `resourceSliceCount` remains greater than zero. A Scenario
generation change that changes devices or attributes becomes a new pool
generation; retries of the same Scenario generation reuse identical names,
shards, attributes, and device IDs.

Create or patch all desired shards, observe the complete highest generation,
then remove stale owned shards. Never report the inventory surface achieved
while the highest pool generation is incomplete. Kubernetes explicitly tells
consumers to ignore lower generations and use `resourceSliceCount` to detect
an incomplete update.
([ResourceSlice API overview](https://kubernetes.io/docs/reference/kubernetes-api/resource/))

## RBAC and preflight

The optional DRA projector role should contain no wildcards and no
`resourceclaims/status` permission:

| API resource | Scope | Verbs | Reason |
| --- | --- | --- | --- |
| `resource.k8s.io/deviceclasses` | Cluster | `get`, `list`, `watch`, `create`, `patch`, `delete` | Observe, server-side apply, conflict detection, and exact cleanup of owned classes |
| `resource.k8s.io/resourceslices` | Cluster | `get`, `list`, `watch`, `create`, `patch`, `delete` | Observe, server-side apply, complete-pool reconciliation, and exact cleanup |
| `resource.k8s.io/resourceclaims` | All namespaces, read-only | `get`, `list`, `watch` | Observe allocation/reservation and detect references before destructive cleanup |
| `core/pods` | All namespaces, read-only | `get`, `list`, `watch` | Observe exact Pod UID, claim use, and scheduler placement |

The preflight identity also needs `create` on
`authorization.k8s.io/selfsubjectaccessreviews`; that is a base target-check
permission rather than a DRA object mutation. API discovery endpoints must be
readable, either through the cluster's standard discovery binding or an exact
non-resource-URL rule. Discovery success is still checked at runtime rather
than inferred from either grant.

The base Scenario reconciler already owns its Node/Lease permissions; do not
duplicate them into the DRA role. If implementation uses `update` instead of
server-side apply, that is a deliberate RBAC expansion and must be reflected in
preflight and tests. The preferred implementation uses `patch` without
`force=true`.

The separate E2E harness identity may create, get, list, watch, patch, and
delete only its namespaced ResourceClaims and Pods. It also receives no
`resourceclaims/status` permission. Kubernetes's scheduler and resourceclaim
controller already have their own control-plane identities; the product must
not grant or impersonate them.

Cluster-wide read access to claims and Pods is needed for safe cleanup of a
cluster-scoped class and pool. Kubernetes RBAC cannot restrict those reads or
cluster-scoped writes by the Scenario's ownership label, so exact
UID/owner-reference checks and bounded observations remain application
invariants. Official guidance likewise separates admin/driver access to
DeviceClasses and ResourceSlices from namespaced workload access to claims.
([DRA permission guidance](https://kubernetes.io/docs/concepts/cluster-administration/dra/#separate-permissions-to-dra-related-apis),
[RBAC least-privilege guidance](https://kubernetes.io/docs/concepts/security/rbac-good-practices/#general-good-practice))

Preflight must verify:

1. the supported Kubernetes minor and target fingerprint;
2. discovery of exactly `resource.k8s.io/v1` `deviceclasses`,
   `resourceslices`, and `resourceclaims`, including scope and required verbs;
3. exact SelfSubjectAccessReviews for each selected operation;
4. absence of foreign objects at every desired class/slice name;
5. absence of a foreign pool using the same driver/pool identity;
6. server dry-run of each representative class/slice create or patch and
   delete; and
7. strict field validation with the production field manager.

Missing list/watch is a capability failure, not a reason to perform
name-prefix cleanup. Missing alpha/beta extensions are irrelevant because the
portable renderer never requests them.

## Ownership and deletion dependencies

`DeviceClass`, `ResourceSlice`, and Scenario Instance are all cluster-scoped,
so each projector-owned object can legally carry the Scenario Instance's exact
controller owner reference. A namespaced dependent may also have a
cluster-scoped owner, but the product deliberately does not own workload
claims or Pods. Kubernetes prohibits cross-namespace namespaced ownership and
requires cluster-scoped dependents to have cluster-scoped owners.
([Kubernetes garbage-collection scope rules](https://kubernetes.io/docs/concepts/architecture/garbage-collection/#owners-and-dependents))

Use both owner reference and exact ownership labels; neither a name prefix nor
the DRA driver string proves ownership. An existing name, pool tuple, or
managed field with a different/missing Instance UID is an
`OwnershipConflict`, never an adoption opportunity.

Cleanup ordering is:

1. close scheduling on affected Synthetic Nodes;
2. observe all claims that reference an owned DeviceClass or whose allocation
   contains an owned `(driver, pool, device)` tuple;
3. observe exact reservations and consuming Pods;
4. if an unowned claim still references the class, or an allocation/reservation
   still uses the pool, report `CleanupBlocked` and retain the Scenario
   Instance;
5. after references are gone, delete stale/desired ResourceSlices with exact
   UID/resourceVersion preconditions;
6. delete the owned DeviceClasses with exact preconditions; and
7. only then allow the ordinary Synthetic Node/Lease cleanup to finish.

The scheduler adds `resource.kubernetes.io/delete-protection` to claims
allocated by its built-in allocator. The resourceclaim controller clears
allocation and removes that finalizer after reservations are gone.
([v1.34.10 finalizer definition](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L26-L35),
[v1.34.10 resourceclaim cleanup](https://github.com/kubernetes/kubernetes/blob/v1.34.10/pkg/controller/resourceclaim/controller.go#L814-L880))
The projector must never remove this finalizer, clear allocation, remove a
reservation, delete a consuming Pod, or force cleanup. A retry resumes from
the last observed dependency state.

Owner-reference garbage collection is a safety net, not the primary deletion
protocol. Explicit ordered deletion and a zero-owned-object observation are
required before removing the Scenario finalizer.

## Server dry-run feasibility and limits

Server dry-run is safe and useful for each independently addressable mutation.
With `dryRun=All`, Kubernetes runs authorization, defaulting, schema
validation, admission, and patch conflict processing, but does not persist the
object or allow external side effects. Authorization is identical to the real
request.
([Kubernetes dry-run contract](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run))

Use server dry-run for:

- DeviceClass and ResourceSlice create/apply/update requests with the same
  body, field manager, strict validation, and no force ownership;
- exact delete requests with UID/resourceVersion preconditions; and
- E2E-owned claim/Pod requests when their referenced dependencies already
  exist persistently.

Server dry-run cannot:

- reserve a name, UID, resourceVersion, or pool capacity;
- make a dry-run DeviceClass or ResourceSlice visible to a later request;
- execute the scheduler asynchronously;
- produce a durable allocation, reservation, or Pod binding;
- validate node preparation, CDI, or container access; or
- act as a transaction across the class, slices, claim, and Pod.

Therefore server dry-run is a pre-acceptance schema/admission/conflict check,
not the DRA fidelity assertion. Persistent writes must repeat ownership and
resourceVersion checks. Allocation/reservation/binding require a post-write
watch and the supported-minor E2E fixture.

## Readiness assertions and explicit exclusions

An instance-level DRA inventory surface is achieved only when:

- every desired DeviceClass exists with exact ownership and selectors;
- every desired pool's highest generation is complete;
- all and only the expected deterministic devices, attributes, capacities,
  Node names, and shard counts are present;
- no stale owned slice can alter the effective highest generation; and
- all required observations are for the accepted Scenario generation.

When an allocation probe or user workload is selected, its surface is achieved
only when the exact claim allocation, exact Pod-UID reservation, allocation
node selector, and Pod binding all agree with owned inventory. Watches resume
from resourceVersion and recover from expiration by bounded re-list.

Always report these exclusions:

- no DRA driver installation or vendor controller;
- no kubelet `NodePrepareResources`/`NodeUnprepareResources`;
- no CDI specification generation or injection;
- no driver configuration consumption;
- no device file, mount, environment, or container access;
- no node-local health stream or hardware fault detection;
- no topology, NUMA, partitioning, sharing, or admin-access fidelity beyond
  fields explicitly selected by a later contract; and
- no accelerator computation, performance, firmware, or vendor runtime.

A KWOK stage marking a bound Pod `Running` is simulated lifecycle evidence,
not proof that the container received an accelerator.

## Required implementation and E2E tests

The implementation should add red tests for:

- Kubernetes 1.33 and missing-v1 discovery rejection before acceptance;
- absence of every required create/patch/delete/read/watch permission;
- refusal to use `v1beta1`, `v1beta2`, or an optional gated field;
- deterministic class, pool, slice, and device identity across retry;
- 128-device sharding, an empty pool, and incomplete highest generation;
- exact owner-reference and pool-identity conflicts;
- source-backed versus simulator-owned attributes;
- aggregate availability changes without changing device identity or rewriting
  existing claims;
- no `resourceclaims/status` or Pod mutation in the operation log;
- allocation/result tuple, reservation UID, node-selector, and Pod-binding
  assessment;
- server dry-run producing zero persistent objects;
- deletion blocked by class references, allocation tuples, and reservations;
- cleanup retry after the external claim/Pod lifecycle completes; and
- zero owned DRA objects after successful deletion.

Run the same projection contract suite against recording and Kubernetes
adapters. On real Kubernetes 1.34, 1.35, and 1.36 fixtures, create one
namespaced claim and Pod under the harness identity and observe the scheduler
allocation, exact reservation, and binding to a Synthetic Node. Verify
deallocation and device reuse after deleting the Pod/claim. Separately assert
that no node preparation, CDI, device access, or compute claim appears in the
receipt.
