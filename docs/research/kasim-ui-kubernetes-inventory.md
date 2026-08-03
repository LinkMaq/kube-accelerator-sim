# Kasim UI: Kubernetes 1.30+ device inventory and scheduling signals

Research date: 2026-08-03

Decision ticket:
[Research: Kubernetes 1.30+ 设备清单与调度使用信号](https://github.com/LinkMaq/kube-accelerator-sim/issues/35)

## Executive recommendation

Build the Cluster Simulation Inventory from independent, read-only Kubernetes
sources and preserve the evidence boundary of each field:

1. Use `core/v1` Nodes and non-terminated, assigned Pods as the Kubernetes 1.30+
   floor. Node `capacity` and `allocatable` are observable scalar facts; summed
   Pod requests and `allocatable - requested` are derived scheduling estimates,
   not physical utilization. Kubernetes schedules from requests and extended
   resources cannot be overcommitted.
   ([Node capacity tracking](https://kubernetes.io/docs/concepts/architecture/nodes/#resource-capacity-tracking),
   [extended-resource accounting](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources))
2. Discover DRA resources on every Simulation Target and select only a schema
   adapter that the binary knows. Kubernetes 1.30's `resource.k8s.io/v1alpha2`
   model is not the same model as the structured device APIs introduced in
   1.31; stable `resource.k8s.io/v1` starts in Kubernetes 1.34. Do not infer DRA
   support from the server minor alone.
   ([v1.30 API types](https://github.com/kubernetes/kubernetes/blob/v1.30.14/staging/src/k8s.io/api/resource/v1alpha2/types.go#L531-L566),
   [v1.31 API types](https://github.com/kubernetes/kubernetes/blob/v1.31.14/staging/src/k8s.io/api/resource/v1alpha3/types.go#L55-L230),
   [DRA GA in v1.34](https://kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/))
3. Maintain one list/watch cache per GroupVersionResource (GVR), paginate the
   initial list, resume watches from each collection's `resourceVersion`, and
   relist after `410 Gone`. A composed inventory is therefore a joined view
   with per-source freshness, not one transactionally consistent cluster
   snapshot.
   ([Kubernetes list/watch contract](https://kubernetes.io/docs/reference/using-api/api-concepts/#efficient-detection-of-changes),
   [paginated lists](https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks))
4. Treat health as unknown unless an explicit health-bearing API field is
   present. Node Ready, extended-resource allocatable, DRA allocation, and a
   ResourceClaim device `Ready` condition answer different questions. Pod
   `allocatedResourcesStatus` is the Kubernetes device-health surface when the
   feature and reporting plugin are available.
   ([Device Plugin health behavior](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-and-unhealthy-devices),
   [DRA observability](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#observability-of-dynamic-resources))
5. Degrade per source. Missing DRA APIs, forbidden Pod reads, or a forbidden
   watch must not hide Node and Scenario data that can still be read. Preserve
   the last successful data only for transient failures and mark it stale;
   never turn absent or forbidden data into a zero count.

The implementation consequence is a read-only `Simulation Inventory` module
that owns discovery, authorization diagnostics, versioned DRA adapters,
list/watch caches, joins, derivations, and evidence metadata. `kasim ui` should
render that stable model and must not decode Kubernetes objects itself.

## Truth classification

The UI needs three explicit evidence classes. A blank field must not carry all
three meanings.

| Class | Meaning | Examples |
| --- | --- | --- |
| **Observable fact** | A field read from a named Kubernetes object, with its GVR and object or collection `resourceVersion`. | Node labels, `capacity`, `allocatable`; a DRA device tuple; a ResourceClaim allocation result; a Pod-reported device health value. |
| **Derived estimate** | Deterministic arithmetic or a join over observable objects. Inputs and derivation must be retained. | Non-terminated assigned Pod requests per Node; `allocatable - requested`; matching an allocated DRA device to its ResourceSlice; mapping an exact resource key to a source-backed Vendor Profile. |
| **Unavailable / unknown** | The API is absent, forbidden, unsupported by the binary, stale, incomplete, or does not expose the requested fact. | Per-device IDs for an extended resource; physical utilization; device health inferred from capacity; DRA data when only Nodes are readable. |

Kubernetes describes `resourceVersion` as an opaque concurrency and watch
cursor. Clients must not parse it or compare versions from different
collections, so every source in the composed view needs its own cursor and
freshness state.
([resource-version semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions))

## API and version matrix

The core inventory floor is stable across the project support range:

| Signal | GVR | Scope | Kubernetes 1.30-1.36 |
| --- | --- | --- | --- |
| Nodes, labels, conditions, capacity, allocatable | `core/v1`, `nodes` | Cluster | Required baseline |
| Assigned Pod specs, status, requests, DRA references, optional resource health | `core/v1`, `pods` | Namespaced, listed across all namespaces | Required for usage; degrade if forbidden |
| Kasim Scenario Instances | `simulation.kasim.io/v1alpha1`, `scenarioinstances` | Cluster | Product CRD; degrade if absent or forbidden |

Nodes and Pods are stable `core/v1` APIs, and the API distinguishes
cluster-scoped resources such as Nodes from namespaced resources such as Pods.
([Kubernetes resource URIs](https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-uris),
[Node v1 API](https://kubernetes.io/docs/reference/kubernetes-api/cluster-resources/node-v1/),
[Pod v1 API](https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/))
The Scenario Instance group, version, plural, and cluster scope come from the
project's canonical
[`ScenarioInstance` CRD](../../config/crd/bases/simulation.kasim.io_scenarioinstances.yaml).

DRA is a versioned optional source:

| Kubernetes minor | Tagged resource API packages relevant to inventory | Schema consequence |
| --- | --- | --- |
| 1.30 | `resource.k8s.io/v1alpha2` | Legacy `ResourceSlice` has top-level `nodeName`, `driverName`, and `namedResources.instances`; allocation may contain opaque handles or the older structured result. It has no stable `(driver, pool, device)` identity. ([types](https://github.com/kubernetes/kubernetes/blob/v1.30.14/staging/src/k8s.io/api/resource/v1alpha2/types.go#L101-L216), [named resources](https://github.com/kubernetes/kubernetes/blob/v1.30.14/staging/src/k8s.io/api/resource/v1alpha2/namedresources.go#L24-L79)) |
| 1.31 | `resource.k8s.io/v1alpha3` | Introduces `spec.driver`, pool generation and shard count, node selection, and `spec.devices[]`; claims use structured device allocation results. ([types](https://github.com/kubernetes/kubernetes/blob/v1.31.14/staging/src/k8s.io/api/resource/v1alpha3/types.go#L55-L230), [claim status](https://github.com/kubernetes/kubernetes/blob/v1.31.14/staging/src/k8s.io/api/resource/v1alpha3/types.go#L610-L699)) |
| 1.32 | `v1alpha3`, `v1beta1` | The structured core reaches beta as `v1beta1`; optional per-allocated-device status is feature-gated. ([v1beta1 types](https://github.com/kubernetes/kubernetes/blob/v1.32.13/staging/src/k8s.io/api/resource/v1beta1/types.go#L59-L217), [claim device status](https://github.com/kubernetes/kubernetes/blob/v1.32.13/staging/src/k8s.io/api/resource/v1beta1/types.go#L677-L728)) |
| 1.33 | `v1alpha3`, `v1beta1`, `v1beta2` | A reader must prefer the actually discovered version and use the matching schema, not assume source-package presence means the API is served. ([tagged API tree](https://github.com/kubernetes/kubernetes/tree/v1.33.13/staging/src/k8s.io/api/resource), [v1beta2 types](https://github.com/kubernetes/kubernetes/blob/v1.33.13/staging/src/k8s.io/api/resource/v1beta2/types.go)) |
| 1.34-1.36 | stable `v1` plus retained pre-GA packages | Core structured DRA is stable from 1.34. Optional DRA extensions continue to have separate feature gates and must remain optional evidence. ([v1.34 types](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go), [v1.36 types](https://github.com/kubernetes/kubernetes/blob/v1.36.3/staging/src/k8s.io/api/resource/v1/types.go), [current DRA docs](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)) |

This table reports schemas present in immutable upstream tags; it is not a
runtime capability assertion. The API server discovery response identifies
served group versions, resource scope, and verbs. A distributor can disable a
feature or restrict a resource even when the upstream minor contains its Go
types.
([`APIGroup` and `APIResource` definitions](https://kubernetes.io/docs/reference/kubernetes-api/definitions/))

Recommended adapter preference is `v1`, `v1beta2`, `v1beta1`, `v1alpha3`, then
`v1alpha2`, but only among versions both discovered and implemented. Stable
`v1` is the only version that may contribute to Kasim's existing
`dra-control-plane` Fidelity Mode. Reading an older API for a real-node
inventory must be labeled `legacy DRA observation`; it does not upgrade the
Scenario Instance's fidelity claim.

## Node extended-resource signals

### Observable facts

`Node.status.capacity` records total resources reported for a Node and
`Node.status.allocatable` records the amount available for Pods. Node-level
extended resources are fully qualified names outside `kubernetes.io`, use
whole-number quantities, and cannot be overcommitted.
([Node status](https://kubernetes.io/docs/reference/node/node-status/#capacity-and-allocatable),
[extended resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources))

For a Device Plugin resource, kubelet advertises the aggregate resource in Node
status. When the plugin marks a device unhealthy, kubelet reduces allocatable
while capacity remains unchanged. Allocation to a Pod does **not** decrement
the Node's allocatable field; scheduler bookkeeping accounts for Pod requests
separately.
([Device Plugin registration and health](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-and-unhealthy-devices),
[scheduler request accounting](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#how-pods-with-resource-requests-are-scheduled))

Node labels are observable metadata, but Kubernetes does not give arbitrary
vendor labels a hardware-attestation meaning. The inventory may show every
exact key/value and may classify it through a matching source-backed Vendor
Profile; it must not infer an Accelerator Model from a similar-looking label or
resource name.
([labels and selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/))

### Derived and unavailable fields

- `requested` is a sum over matching Pods, not a Node field.
- `remainingEstimate = allocatable - requested` is a scheduler-oriented
  estimate, not physical idleness or utilization.
- A scalar extended resource provides no per-device identifier, topology,
  allocation owner, or individual health field. Show the resource key and
  quantities; do not synthesize device rows or IDs.
- `capacity - allocatable` is not a portable device-health count. The Device
  Plugin contract explains one reason for that delta, but real Nodes can have
  other publishers and Kasim intentionally simulates aggregate health.
- Node `Ready` is a Node condition, not Accelerator or RDMA device health.
  ([Node conditions](https://kubernetes.io/docs/reference/node/node-status/#condition))

Within the Kubernetes APIs surveyed here, RDMA has no dedicated core inventory
kind. This is an inference from the generic Node extended-resource and DRA
schemas: an RDMA or other Auxiliary Device Signal must be recognized by an
exact, source-backed Resource Contract or DRA driver/attribute contract. The
presence of `ResourceClaim.status.devices[].networkData` only proves that a
driver reported network data for an allocated DRA device; it does not by itself
classify the device as RDMA.
([`AllocatedDeviceStatus` and `NetworkDeviceData`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/#AllocatedDeviceStatus))

## Pod requested aggregation

### The correct observable estimate

List Pods across all namespaces with all of these server-side field selectors:

```text
spec.nodeName!=,status.phase!=Succeeded,status.phase!=Failed
```

`spec.nodeName` and `status.phase` are supported Pod field selectors, `!=` and
chained selectors are supported, and upstream `kubectl describe node` uses the
same Node-bound, non-terminal selection boundary.
([field-selector contract](https://kubernetes.io/docs/concepts/overview/working-with-objects/field-selectors/),
[`kubectl` Node describer source](https://github.com/kubernetes/kubectl/blob/v0.36.3/pkg/describe/describe.go#L3431-L3455))

For each returned Pod, calculate the effective request per resource and sum by
`spec.nodeName`. Do not merely add ordinary containers. Kubernetes 1.30 already
accounts for ordinary containers, restartable init containers, the maximum
effective init phase, in-place resize state when enabled, and Pod overhead.
The current helper additionally accounts for Pod-level resources and newer
status-based resource state. The scheduler calls the same upstream helper and
adds scalar resources to `NodeInfo.Requested`.
([v1.30 `PodRequests`](https://github.com/kubernetes/kubernetes/blob/v1.30.14/pkg/api/v1/resource/helpers.go#L31-L120),
[v1.36 `PodRequests`](https://github.com/kubernetes/kubernetes/blob/v1.36.3/staging/src/k8s.io/component-helpers/resource/helpers.go#L31-L173),
[v1.36 scheduler call site](https://github.com/kubernetes/kubernetes/blob/v1.36.3/pkg/scheduler/framework/types.go#L832-L870))

Pin `k8s.io/component-helpers` to the same Kubernetes module line as the other
project dependencies and use `resource.PodRequests` rather than duplicating
this evolving algorithm. The implementation must set options only for
capabilities it actually supports; optional in-place-resize or Pod-level
behavior must not be guessed from the server minor.

### What the estimate does not prove

Call the result **requested**, never **used**. Kubernetes explicitly schedules
from requests even when actual CPU or memory use is lower, and actual usage is
a separate metrics concern.
([requests versus usage](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#how-pods-with-resource-requests-are-scheduled))

The API-derived sum also cannot reproduce the scheduler's exact instantaneous
cache: `NodeInfo.Requested` includes assumed Pods sent for binding that may not
yet be visible with `spec.nodeName`, and a custom scheduler may use different
plugins or state. Therefore expose `requestedFromObservedPods`, the contributing
Pod count, and the observation time rather than claiming “scheduler used.”
([`NodeInfo.Requested` contract](https://github.com/kubernetes/kubernetes/blob/v1.36.3/pkg/scheduler/framework/types.go#L313-L321))

## DRA inventory and scheduling state

### ResourceSlice

For `v1alpha3` and newer structured schemas, a device identity is the tuple
`(driver, pool, device)`. Preserve all three parts. A slice also carries pool
`generation` and `resourceSliceCount`; consumers must use the highest complete
generation and mark a pool incomplete until all expected slices for that
generation are observed. A slice can place a pool on one Node, a Node selector,
or all Nodes; newer optional fields can move node selection to each device.
([stable `ResourceSlice` API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/),
[stable type contract](https://github.com/kubernetes/kubernetes/blob/v1.34.10/staging/src/k8s.io/api/resource/v1/types.go#L98-L320))

Do not count devices from incomplete pool generations as a complete inventory.
For selector-based or all-Node devices, retain placement as a selector/shared
scope instead of duplicating one physical-looking device into every Node row.
Any per-Node expansion is a derived eligibility view and must be labeled so.

The legacy 1.30 adapter has a different identity: driver name, optional Node
name, and a named-resource instance name. Preserve that native evidence and do
not invent a pool to make it look like the stable tuple.
([v1alpha2 `ResourceSlice`](https://github.com/kubernetes/kubernetes/blob/v1.30.14/staging/src/k8s.io/api/resource/v1alpha2/types.go#L531-L566),
[v1alpha2 named instance](https://github.com/kubernetes/kubernetes/blob/v1.30.14/staging/src/k8s.io/api/resource/v1alpha2/namedresources.go#L24-L79))

### ResourceClaim and Pods

For structured DRA, `ResourceClaim.status.allocation.devices.results[]` is the
allocation fact and identifies the selected driver, pool, and device.
`status.reservedFor[]` identifies consumers permitted to use the claim. A
matching Pod UID, Pod claim reference, and `spec.nodeName` provide an observed
scheduling join, but do not prove that a kubelet prepared the device or that a
container is actively using it.
([ResourceClaim API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/),
[DRA allocation workflow](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#scheduling-pods))

Expose separate states instead of one `inUse` boolean:

- `unallocated`: no allocation result;
- `allocated`: an allocation result exists;
- `reserved`: `reservedFor` contains a consumer reference;
- `scheduledObserved`: the referenced Pod UID is visible and assigned to a
  Node;
- `runtimeUseUnknown`: no kubelet PodResources or metrics evidence is in this
  API-server-only inventory.

The official DRA observability documentation assigns exact in-use device
monitoring to the kubelet `PodResourcesLister` gRPC service. `kasim ui` connects
only to the API server, so it must keep `runtimeUseUnknown` rather than reaching
through `nodes/proxy` or claiming runtime use.
([DRA observability](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#observability-of-dynamic-resources),
[kubelet authorization warning](https://kubernetes.io/docs/reference/access-authn-authz/kubelet-authn-authz/))

Do not calculate universal DRA “free devices” as
`published devices - allocations`: current DRA can allow multiple allocations
and consumable capacity, so allocation cardinality is not always exclusive
device cardinality. Show published and allocated facts separately unless the
specific schema and Vendor Profile establish exclusive semantics.
([stable Device fields](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/#Device))

## Health truth boundary

Use this precedence without filling missing levels from weaker signals:

1. **Pod resource health**: when present,
   `containerStatuses[].allocatedResourcesStatus[].resources[]` reports a
   resource ID with `Healthy`, `Unhealthy`, or `Unknown`. It is feature-gated
   and reporter-dependent; current Kubernetes documents it as beta and enabled
   by default from 1.36. DRA health additionally requires a driver that
   implements the health RPC.
   ([Device Plugin health status](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-and-unhealthy-devices),
   [DRA health status](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#device-health-monitoring),
   [v1.36 core types](https://github.com/kubernetes/kubernetes/blob/v1.36.3/staging/src/k8s.io/api/core/v1/types.go#L3392-L3466))
2. **ResourceClaim device conditions**: `status.devices[]` is optional,
   driver-owned data. Its `Ready=True` means the allocated device was configured
   according to claim/class configuration; it is not a substitute for the Pod
   resource health enum, and the official docs warn that accuracy depends on
   the driver.
   ([DRA ResourceClaim device status](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#resourceclaim-device-status),
   [`AllocatedDeviceStatus` API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/#AllocatedDeviceStatus))
3. **Aggregate schedulability**: Node allocatable says how many units are
   offered for new scheduling. It can reflect Device Plugin health changes but
   has no per-device status or cause. Present it as schedulability only.
4. **Node health**: Node Ready concerns the Node. Do not propagate it to all
   devices.
5. **No evidence**: return `Unknown / not reported`, never a green default.

For Synthetic Nodes, Kasim's Scenario and status may supply a declared
aggregate health signal. Keep that source labeled `Kasim simulated`; do not
present it as Device Plugin, DRA driver, or physical health evidence.

## List, watch, pagination, and freshness

Use one cache per required GVR:

1. Discover the GVR and its advertised scope and verbs.
2. List with a bounded `limit`, following the opaque `continue` token until it
   is empty. A continued list retains one collection `resourceVersion` and a
   consistent snapshot; an expired token returns `410 Gone`, which requires a
   fresh list when consistency matters.
   ([chunking semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks))
3. Start watch from that collection `resourceVersion`, request bookmarks, and
   treat bookmarks only as progress markers—the server is not required to send
   them at a fixed interval.
4. Resume a disconnected watch from its last event `resourceVersion`. On
   `410 Gone`, discard only that source cache, relist it, and rebuild dependent
   joins. Kubernetes recommends client-go's `Reflector` for this list/watch
   loop.
   ([watch recovery and bookmarks](https://kubernetes.io/docs/reference/using-api/api-concepts/#efficient-detection-of-changes))
5. Publish an immutable composed snapshot after a short debounce. Each source
   records its own `resourceVersion`, `lastSuccessfulSync`, and condition; there
   is no cross-GVR resource version that makes Nodes, Pods, claims, and slices
   atomic.

Streaming lists (`sendInitialEvents=true`) cannot be the 1.30 baseline: the
feature reached beta later and requires `resourceVersionMatch=NotOlderThan`.
The conventional paginated list plus watch path works throughout 1.30+; a
streaming-list optimization can be capability-tested separately.
([streaming-list contract](https://kubernetes.io/docs/reference/using-api/api-concepts/#streaming-lists))

For scale, avoid a Pod list per Node. One cluster-wide filtered Pod list/watch
is sufficient for all per-Node request totals. Paginate Nodes, Pods, Scenario
Instances, ResourceSlices, DeviceClasses, and ResourceClaims independently;
bound UI payloads and diagnostics after the cache has built the complete
inventory rather than silently truncating the Kubernetes list.

## Minimum read-only RBAC

The inventory itself needs `list` and `watch`. `get` is not necessary when
details are served from the synchronized cache; add it only if a later design
introduces direct, uncached object fetches.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kasim-inventory-viewer
rules:
  - apiGroups: [""]
    resources: ["nodes", "pods"]
    verbs: ["list", "watch"]
  - apiGroups: ["simulation.kasim.io"]
    resources: ["scenarioinstances"]
    verbs: ["list", "watch"]
  - apiGroups: ["resource.k8s.io"]
    resources: ["resourceslices", "resourceclaims", "deviceclasses"]
    verbs: ["list", "watch"]
```

Nodes, ResourceSlices, and DeviceClasses are cluster-scoped. Pods and
ResourceClaims are namespaced, but a complete inventory lists them across all
namespaces, which requires a ClusterRole binding for those list/watch requests.
Kubernetes authorizes `get`, `list`, and `watch` as distinct verbs.
([RBAC authorization](https://kubernetes.io/docs/reference/access-authn-authz/rbac/),
[Kubernetes API verbs](https://kubernetes.io/docs/reference/using-api/api-concepts/#api-verbs),
[ResourceClaim API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/))

Do not grant `nodes/proxy`, Pod logs, Secrets, metrics, or any write verb. In
particular, `nodes/proxy` reaches kubelet APIs and is substantially broader than
an API-server inventory.
([kubelet authorization](https://kubernetes.io/docs/reference/access-authn-authz/kubelet-authn-authz/))

`SelfSubjectAccessReview` can improve permission diagnostics, but it is a
`create` request to a virtual authorization resource and is not required for
the data path. Prefer actual discovery plus list/watch results as definitive;
if SSAR is used, make its `create` permission optional and explain that the
review is an authorization query rather than a persistent cluster object.
([checking API access](https://kubernetes.io/docs/reference/access-authn-authz/authorization/#checking-api-access),
[`SelfSubjectAccessReview` API](https://kubernetes.io/docs/reference/kubernetes-api/definitions/self-subject-access-review-v1-authorization/))

## Graceful degradation matrix

| Observation | Source state | UI behavior |
| --- | --- | --- |
| Group/resource not discovered or `404` / no match | `unsupported` | Hide the corresponding data section or show “API not served”; keep core Node/Pod data. |
| Schema version is served but the binary has no adapter | `unsupportedSchema` | Show GVR and diagnostic; never decode by field-name guesswork. |
| `403 Forbidden` on list | `forbidden` | Show no count for that source, list the missing `list` permission, and continue with other sources. |
| List succeeds, watch is forbidden | `snapshotOnly` | Show the completed list with observation time and a prominent non-live badge; optional bounded relist polling is a later policy choice. |
| Watch disconnect / timeout / transient server error | `stale` after grace period | Keep last successful data, show age and error, reconnect from last resource version. |
| Watch returns `410 Gone` | `resyncing` | Relist only that GVR, temporarily mark dependent derived fields stale, then replace atomically. |
| ResourceSlice pool shard count is incomplete | `incomplete` | Show expected/observed shards; do not report a complete device total. |
| Pod access is forbidden | `usageUnavailable` | Continue to show Node capacity/allocatable and DRA publication; do not display requested or remaining as zero. |
| ResourceClaim access is forbidden | `allocationUnavailable` | Continue to show ResourceSlice devices; do not infer allocation/free state. |
| No health-bearing field is present | `healthUnknown` | Show “unknown / not reported”; do not derive health from Ready or allocatable. |

Only failure to establish the configured API-server connection should make the
CLI startup fail. A forbidden Node list can still start a diagnostics-only or
Scenario-only page if those sources are reachable; it must not be reported as
an empty cluster. All source failures belong in a visible diagnostics area and
in the source metadata returned by the read model.

## Simulation Inventory read-model constraints

The stable presentation model should contain at least these concepts:

```text
InventorySnapshot
  target: context, server version, observedAt
  sources[]: gvr, schema, scope, mode, resourceVersion,
             lastSuccessfulSync, stale, diagnostic
  nodes[]
    identity: name, uid, origin(kasim|real), scenarioRef?
    nodeState: ready, unschedulable, labels
    signals[]: resourceName, capacity, allocatable,
               requestedFromObservedPods?, remainingEstimate?, evidence
    devices[]: nativeID, identityScheme, driver?, pool?, attributes,
               capacities, placement, allocationState, health, evidence
    auxiliarySignals[]: profileContract, signal, evidence
  draPools[]: driver, pool, generation, expectedSlices, observedSlices,
              completeness, placement
  claims[]: namespace, name, allocation, reservations,
            scheduledConsumers, runtimeUseUnknown
```

Required invariants:

- Every quantity, identity, allocation, health value, and vendor/model
  classification carries provenance and an evidence class.
- `origin=kasim` comes from exact Kasim ownership metadata and Scenario joins;
  all other Nodes remain `real` or `unclassified`, never “not simulated” by
  absence of one guessed label.
- Extended-resource rows are scalar. DRA device rows are individual only when
  the native schema provides an ID.
- `requested`, `remainingEstimate`, eligible Node expansion, profile mapping,
  and claim-to-Pod joins are explicitly derived fields.
- `unknown`, `zero`, `forbidden`, `unsupported`, `incomplete`, and `stale` are
  distinct states.
- DRA version conversion occurs behind a versioned inventory Adapter. The UI
  receives no `runtime.Unstructured`, CRD payload, or client-go type.
- Kasim-owned stable DRA evidence may contribute to the Scenario Instance's
  declared Fidelity Mode; legacy or real-node observations never modify that
  claim.

## Decisions unlocked for follow-up tickets

1. Keep the required first release compatible with Kubernetes 1.30+ through
   core Node/Pod signals and stable DRA v1 observation on 1.34+.
2. Decide explicitly whether pre-1.34 real-cluster DRA observation is required
   in the first release. If yes, fund separate typed adapters and fixtures for
   `v1alpha2`, `v1alpha3`, `v1beta1`, and `v1beta2`; do not weaken the stable
   Kasim DRA fidelity gate.
3. Add a dedicated read-only inventory ClusterRole (or document equivalent
   user permissions). The current UI contract requires `watch`, not only the
   existing one-shot operator reads.
4. Make provenance, source freshness, permission diagnostics, pool
   completeness, and unknown health first-class API fields before designing
   visual status colors.
5. Keep RDMA and other Auxiliary Device Signals profile-driven. Kubernetes
   supplies generic scalar/DRA evidence, not a portable core RDMA identity.
