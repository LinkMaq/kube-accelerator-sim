# Kubernetes Accelerator Resource Contract

Research date: 2026-07-29

Upstream baseline: Kubernetes v1.36.2

## Answer first

The lightweight contract should stop at the Kubernetes scheduling boundary:

1. A Synthetic Node controller can reproduce the scheduler-visible behavior of a Device Plugin managed Accelerator by maintaining the Node's extended-resource `status.capacity` and `status.allocatable`, its Ready/heartbeat state, and the Scenario Instance's ownership. The default scheduler fits integer Pod requests against `allocatable - already requested`; it does **not** inspect device IDs, `ListAndWatch`, per-device health, NUMA topology, `AllocateResponse`, or CDI data. This is enough to test resource discovery, inventory, ordinary scheduling, exhaustion, recovery, and heterogeneous multi-node placement. ([extended resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources), [scheduler fit source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/scheduler/framework/plugins/noderesources/fit.go#L678-L760))
2. Device Plugin registration and all device-level operations are node-local kubelet behavior. A plugin registers on the kubelet's Unix socket, streams device IDs and health to the kubelet, and receives allocation/start RPCs from the kubelet. No API object can make those RPCs occur. Protocol fidelity therefore requires a real kubelet or a deliberately compatible node agent; an API-only Synthetic Node cannot provide it. ([Device Plugin workflow](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-implementation), [v1beta1 protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L5-L57))
3. Dynamic Resource Allocation (DRA) should be a separate, optional control-plane fidelity mode, not the default abstraction. Core structured-parameter DRA is stable in current Kubernetes and exposes device identity, attributes, capacity, node accessibility, claims, and allocation results to the scheduler through `resource.k8s.io/v1`. Synthetic Nodes plus `DeviceClass`, `ResourceSlice`, and `ResourceClaim` objects can exercise DRA allocation and placement, but a bound Pod cannot start without kubelet invoking the node-side DRA driver. In v1.36, skipping node preparation is not part of the released contract; optional preparation is only targeted as Alpha for v1.37. ([current DRA docs](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/), [DRA node protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/dra/v1/api.proto#L24-L112), [KEP-5945 status](https://github.com/kubernetes/enhancements/blob/e32008ea3ed16998fca89b72754bc7c598a07679/keps/sig-node/5945-dra-optional-node-preparation/kep.yaml#L16-L38))

The architecture ticket should therefore design three explicit fidelity levels: **extended-resource scheduling** (default), **DRA control-plane allocation** (optional and version-gated), and **node-runtime protocol** (end-to-end harness only). It should not describe the default mode as a Device Plugin emulator.

## Evidence baseline and maturity

The Kubernetes project lists v1.36.2 as the latest v1.36 patch release on the research date; v1.37 is still upcoming. Code, protocol, KEP, and CDI specification links below are pinned to v1.36.2 or immutable upstream commits; documentation links refer to the current v1.36 site. ([Kubernetes releases](https://kubernetes.io/releases/))

| Contract | Current maturity | Consequence |
| --- | --- | --- |
| Extended resources | Established core behavior | Safe baseline for scheduler-visible Accelerator Simulation. |
| Device Manager | Stable framework, but the Device Plugin API itself remains `v1beta1` and is not declared stable | High-fidelity protocol adapters must tolerate API evolution even though the common runtime path is mature. ([API compatibility](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#api-compatibility)) |
| Device Plugin CDI devices | Stable since v1.31 | CDI names are a supported `AllocateResponse` output, but still need node-local kubelet/runtime processing. ([KEP-4009 maturity](https://github.com/kubernetes/enhancements/blob/e32008ea3ed16998fca89b72754bc7c598a07679/keps/sig-node/4009-add-cdi-devices-to-device-plugin-api/kep.yaml#L20-L41)) |
| Core structured-parameter DRA | Stable and enabled in current releases; `resource.k8s.io/v1` is the current API | Suitable for a distinct DRA control-plane simulation mode. ([feature-gate history](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#feature-gates-for-graduated-or-deprecated-features), [KEP-4381 maturity](https://github.com/kubernetes/enhancements/blob/e32008ea3ed16998fca89b72754bc7c598a07679/keps/sig-node/4381-dra-structured-parameters/kep.yaml#L22-L48)) |
| DRA-backed extended-resource syntax | Beta and enabled by default in v1.36 | Profiles may later offer compatibility with `limits: vendor.example/device`, but the implementation must feature-detect it rather than assume it on older clusters. ([current feature state](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#extended-resource-allocation-by-dra), [KEP-5004 milestones](https://github.com/kubernetes/enhancements/blob/e32008ea3ed16998fca89b72754bc7c598a07679/keps/sig-scheduling/5004-dra-extended-resource/kep.yaml#L23-L46)) |
| Per-container assigned-device health | Beta and enabled by default in v1.36 | Useful for a node-runtime test tier, but not part of the portable aggregate scheduler contract. ([`ResourceHealthStatus`](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#ResourceHealthStatus)) |

## What the control plane and scheduler observe

### Extended-resource object contract

| Surface | Upstream behavior | What it means for a Synthetic Node |
| --- | --- | --- |
| Resource name | A node-level extended resource is a fully-qualified name outside the reserved `kubernetes.io` domain. Device Plugin registration also validates the advertised name as an extended resource. ([resource rules](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources), [registration validation](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/plugin/v1beta1/server.go#L159-L181)) | A Vendor Profile must emit the real vendor resource name; model names must not become invented resource names unless the vendor contract actually does so. |
| Quantity | Extended resources are whole-number, non-overcommittable resources. If both request and limit are present they must be equal; a Pod stays Pending when all resource requests cannot be satisfied. ([consumption rules](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#consuming-extended-resources)) | A Scenario device count compiles to an integer quantity. Fractional models need a source-backed logical resource or DRA representation, not fractional extended-resource quantities. |
| Node capacity | `Node.status.capacity` represents total resources. The normal Device Plugin path counts healthy plus unhealthy devices as capacity. ([Node API](https://kubernetes.io/docs/reference/kubernetes-api/core/node-v1/#NodeStatus), [Device Manager capacity source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L432-L495)) | Keep configured physical/logical inventory in `capacity`, including temporarily unhealthy devices. |
| Node allocatable | `Node.status.allocatable` is the scheduling supply. In the Device Plugin path it is the count of healthy devices. ([Node API](https://kubernetes.io/docs/reference/kubernetes-api/core/node-v1/#NodeStatus), [Device Manager capacity source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L432-L495)) | Set `allocatable` to healthy, schedulable units. Health loss changes `allocatable` without changing `capacity`. |
| Status propagation | In the normal non-plugin manual procedure, an operator patches `status.capacity` and the kubelet later updates `status.allocatable`; kubelet's node-status setter also overlays Device Plugin capacity and allocatable. ([manual advertisement](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#node-level-extended-resources), [node-status setter](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/nodestatus/setters.go#L265-L323)) | **Inference:** because an API-only Synthetic Node has no kubelet to perform the asynchronous step, its controller must reconcile both fields through the Node status subresource. Patching capacity alone is insufficient for deterministic scheduling. |
| Scheduler fit | `NodeResourcesFit` compares every scalar request with `NodeInfo.Allocatable - NodeInfo.Requested`; an insufficient quantity yields `Insufficient <resource-name>`. ([scheduler source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/scheduler/framework/plugins/noderesources/fit.go#L678-L760)) | Native scheduling, exhaustion, and release can be tested without any device object or Device Plugin RPC. |
| Node health | A Node may be manually created, but it is only eligible when healthy. Node status and Lease heartbeats drive availability; stale Ready state is changed to Unknown and results in `unreachable`/`not-ready` taints. ([Node management and heartbeats](https://kubernetes.io/docs/concepts/architecture/nodes/), [Ready condition behavior](https://kubernetes.io/docs/reference/node/node-status/#condition)) | The Scenario Instance must own and renew Synthetic Node status/Lease state, or use a backend that already does. A one-time Node manifest is not a stable scheduler simulation. |

### Information deliberately absent from the extended-resource scheduler contract

For an ordinary Device Plugin managed resource, the scheduler sees a scalar resource name and quantity, plus normal Node labels, taints, affinity, and topology-domain labels. It does not receive the plugin's device IDs or `TopologyInfo`. The scheduler source accounts scalar quantities, while device ID selection and NUMA filtering happen later inside kubelet's Device Manager. ([scheduler scalar accounting](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/scheduler/framework/plugins/noderesources/fit.go#L731-L760), [kubelet device selection](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L580-L735))

Consequently:

- Model, memory size, interconnect, partition type, and driver version only influence ordinary scheduler placement if a vendor integration exposes them separately as Node labels/taints or distinct extended-resource names. They are not fields of the extended-resource quantity itself.
- Updating a Synthetic Node's aggregate `allocatable` faithfully reproduces the scheduler consequence of a health change, but it does not reproduce which device ID failed or which already-bound Pod owns it.
- Normal topology spread, Node affinity, taints, and vendor labels remain scheduler-visible and can be modeled on a Synthetic Node. Device Plugin NUMA topology is a separate kubelet-local contract.

These are direct consequences of the cited scheduler and Device Manager boundaries, not additional Device Plugin capabilities.

Device IDs and NUMA assignments do have a native observation surface, but it is node-local rather than scheduler/control-plane state: kubelet's `PodResourcesLister` gRPC API exposes running containers' Device Plugin IDs, topology, and DRA claim/CDI information over a Unix socket. `GetAllocatableResources` also exposes more device detail than kubelet publishes to the API server. An API-only Synthetic Node does not provide this service. ([PodResources API](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#monitoring-device-plugin-resources))

## Device Plugin registration and allocation lifecycle

| Stage | Exact contract and observer | Synthetic Node only? |
| --- | --- | --- |
| Start service | The plugin serves the `DevicePlugin` gRPC API from a Unix socket under the hard-coded host path `/var/lib/kubelet/device-plugins/`. ([upstream workflow](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-implementation)) | No. This is a node filesystem and kubelet contract. |
| Register | The plugin calls the kubelet's `Registration.Register` service at `kubelet.sock` with API version, endpoint, extended `resource_name`, and options. It must start serving before registering. ([protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L5-L29), [server validation](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/plugin/v1beta1/server.go#L159-L181)) | No. There is no API-server registration object. |
| Discover options | Kubelet calls `GetDevicePluginOptions`; the response declares whether `PreStartContainer` and `GetPreferredAllocation` are available. ([protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L14-L19), [upstream behavior](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-implementation)) | No. |
| List and watch | The plugin streams a complete list of device IDs, health, and optional NUMA `TopologyInfo`; it sends a new list when a device changes or disappears. Kubelet partitions those IDs into healthy and unhealthy sets. ([protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L40-L89), [receiver source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L259-L319)) | Aggregate consequence only: update Node capacity/allocatable. The stream and identity semantics require kubelet. |
| Publish supply | Kubelet counts all devices into capacity and healthy devices into allocatable, then publishes both through Node status. ([capacity source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L432-L495), [status source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/nodestatus/setters.go#L265-L323)) | Yes, at aggregate level, by writing the same resulting Node status. |
| Schedule | The scheduler reserves a count, not a particular device ID. It fits the Pod against Node allocatable and already requested scalar resources. ([scheduler source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/scheduler/framework/plugins/noderesources/fit.go#L731-L760)) | Yes. This is the default fidelity target. |
| Select IDs | During node admission/container preparation, kubelet chooses unallocated healthy device IDs, first respecting Topology Manager affinity and then optionally consulting `GetPreferredAllocation`. ([selection source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L580-L735)) | No. |
| Allocate | For each new Device Plugin resource needed by a container, kubelet calls `Allocate` with selected device IDs. The plugin may perform device-specific work and returns environment variables, mounts, device nodes, annotations, and/or CDI names; kubelet checkpoints the assignment and passes runtime configuration onward. ([protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L126-L191), [runtime path source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L835-L940)) | No. A bound Pod on a Synthetic Node proves scheduling only, not successful allocation or startup. |
| Pre-start | If the plugin advertises `pre_start_required`, kubelet calls `PreStartContainer` with allocated IDs before each container start. ([protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L91-L100), [kubelet call site](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L955-L1025)) | No. |
| Restart recovery | Kubelet checkpoints assignments. A restarting kubelet removes plugin sockets, and plugins must detect the deletion and re-register. ([upstream behavior](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#handling-kubelet-restarts), [checkpoint/allocation source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L337-L354)) | No, unless a compatible node agent deliberately implements this lifecycle. |

### `GetPreferredAllocation` is advisory and node-local

The request contains available IDs, must-include IDs, and a desired count. Its result is explicitly not guaranteed to be the final allocation. Kubelet asks only when the plugin advertises support, intersects the response with its own eligible set, and falls back to its own selection. It can help with vendor-local relationships such as device grouping, but it cannot change the scheduler's Node choice. ([protocol semantics](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L101-L125), [kubelet fallback](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/kubelet/cm/devicemanager/manager.go#L685-L735))

### Health behavior

`ListAndWatch` is authoritative for Device Plugin health. When a device becomes unhealthy:

- kubelet reduces Node `allocatable` while leaving `capacity` unchanged;
- already assigned Pods remain assigned to the failed device; Kubernetes does not automatically move or kill them because of the Device Plugin update;
- with `ResourceHealthStatus` enabled, kubelet can expose assigned-device health in `containerStatuses[].allocatedResourcesStatus`; this is beta and enabled by default in v1.36. ([upstream health behavior](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-and-unhealthy-devices), [feature gate](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#ResourceHealthStatus))

An API-only scenario can faithfully test the first bullet by reconciling aggregate Node status. The latter two require kubelet's device assignment cache and status writer; inventing `allocatedResourcesStatus` from the control plane would test the simulator's fabrication, not Kubernetes Device Plugin behavior.

### Topology behavior

Device Plugin `TopologyInfo` is a list of NUMA node IDs associated with each device. Kubelet's Topology Manager combines local hints from Device Manager and other hint providers and may accept or reject a Pod according to its node-level policy. This is admission after scheduling; the ordinary scheduler does not consume these per-device NUMA hints. ([Device Plugin topology integration](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-integration-with-the-topology-manager), [Topology Manager behavior](https://kubernetes.io/docs/tasks/administer-cluster/topology-manager/#how-topology-manager-works))

Therefore a Synthetic Node can simulate scheduler-visible topology through labels and affinity domains, but cannot honestly claim Device Plugin NUMA alignment, topology-policy rejection, or ID-level preferred allocation without a kubelet-compatible node agent.

### CDI integration

`ContainerAllocateResponse.cdi_devices` holds fully-qualified CDI device names. Kubernetes support for this Device Plugin output is stable, and kubelet forwards the resulting runtime configuration; the Device Plugin protocol does not embed the actual OCI edits. ([Device Plugin proto](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1/api.proto#L139-L169), [Kubernetes CDI integration](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-implementation))

The CDI specification separately defines files at well-known paths and the environment, device-node, mount, hook, and other OCI edits that a CDI-aware runtime applies when a named device is requested. ([CDI specification](https://github.com/cncf-tags/container-device-interface/blob/49ac08dcf160f5de366b6a8b574a51582b66b4cf/SPEC.md#overview), [OCI edits](https://github.com/cncf-tags/container-device-interface/blob/49ac08dcf160f5de366b6a8b574a51582b66b4cf/SPEC.md#oci-edits))

Thus a CDI name in a mock `AllocateResponse` is not by itself container access. Protocol-level CDI verification needs all three node-side pieces: kubelet handling, a matching CDI spec, and a CDI-capable container runtime.

## Dynamic Resource Allocation contract

### Control-plane objects and allocation

| Object or phase | Current `resource.k8s.io/v1` contract | Synthetic control-plane feasibility |
| --- | --- | --- |
| `DeviceClass` | Cluster-scoped, vendor/admin-provided selectors and configuration that claims reference. ([API overview](https://kubernetes.io/docs/reference/kubernetes-api/resource/)) | Yes. A Vendor Profile can compile source-backed class selectors/configuration. |
| `ResourceSlice` | Cluster-scoped inventory published by a driver. It groups uniquely named devices into versioned pools and can publish attributes, capacities, and access through `nodeName`, `nodeSelector`, `allNodes`, or per-device node selection. Consumers use pool generation and slice count to avoid incomplete views. ([API source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/api/resource/v1/types.go#L74-L180), [API reference](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)) | Yes. The simulator can own slices for Synthetic Nodes and update them as a Scenario changes. |
| `ResourceClaim` / `ResourceClaimTemplate` | Namespaced device requests. Requests can use a class, CEL selectors, constraints, configuration, and exact-count or all-device allocation modes. Claim status holds the selected driver/pool/device results and node accessibility. ([claim API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/), [usage task](https://kubernetes.io/docs/tasks/configure-pod-container/assign-resources/allocate-devices-dra/)) | Yes. User-authored claims or generated claims can exercise native control-plane behavior. |
| Template expansion | `resourceclaim-controller` in kube-controller-manager creates claims for Pod references to templates. ([DRA workflow](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#workflow-for-kubernetes)) | Yes, when the target cluster supports current DRA. |
| Allocation and placement | The scheduler filters ResourceSlices for matching unallocated devices accessible from eligible Nodes, writes allocation into claim status, reserves it for the Pod, and binds the Pod to a compatible Node. Current allocation is first-fit, not device scoring. ([DRA workflow](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#workflow-for-kubernetes), [selection behavior](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#naming-and-prioritization)) | Yes. This is the meaningful DRA control-plane simulation boundary. |
| Node preparation | After binding, kubelet calls the selected driver's `NodePrepareResources`; on teardown it calls `NodeUnprepareResources`. A successful prepare response returns zero or more CDI device IDs for each allocated claim/device. ([DRA protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/dra/v1/api.proto#L24-L112), [cluster-admin behavior](https://kubernetes.io/docs/concepts/cluster-administration/dra/#kubelet-metrics)) | No. A real kubelet or compatible node agent plus a DRA driver is required for Pod startup/cleanup. |

Core DRA is materially richer than extended-resource scheduling because device identity, attributes, capacities, pool consistency, and node accessibility are scheduler-visible. It is not merely another encoding of `Node.status.allocatable`.

### DRA health and source-presence boundary

DRA drivers may update per-device information in `ResourceClaim.status.devices`, but upstream explicitly states that its accuracy depends on the driver and may not be real time. In v1.36, the beta DRA health service is a separate node-local stream: the driver implements `DRAResourceHealth.NodeWatchResources`, kubelet receives health/timeout/message updates, and kubelet writes assigned-device health to Pod status. ([claim status caveat](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#resourceclaim-device-status), [DRA health protocol](https://github.com/kubernetes/kubernetes/blob/v1.36.2/staging/src/k8s.io/kubelet/pkg/apis/dra-health/v1alpha1/api.proto#L20-L76))

Creating a `status.devices` field or a `ResourceSlice` proves that the API accepts simulated metadata; it does not prove a DRA driver detected hardware, prepared a claim, exposed CDI devices, or streamed health. Those runtime claims require the node-side protocol.

### DRA-backed extended-resource requests

In v1.36, a `DeviceClass.spec.extendedResourceName` can map familiar Pod extended-resource syntax to DRA. The same resource name may be served by a Device Plugin on some Nodes and DRA on others. Scheduler source gives a positive Node scalar resource precedence; only when no positive scalar allocatable is present does it delegate a mapped name to DRA. ([DRA documentation](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#extended-resource-allocation-by-dra), [scheduler delegation source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/pkg/scheduler/framework/plugins/noderesources/fit.go#L270-L290))

**Architecture consequence:** a Scenario backend must choose and report which provider owns a resource on each Synthetic Node. It must not publish a positive scalar `status.allocatable` and assume that the same request will test DRA allocation on that Node.

## Synthetic Node versus node-agent capability matrix

| Behavior to test | API-only Synthetic Node | Requires kubelet or compatible node agent |
| --- | --- | --- |
| Vendor resource name, total count, healthy allocatable count | Yes: Node status | No |
| Single-node/single-card, single-node/multi-card, multi-node/multi-card scheduler placement | Yes: Node status plus Ready/Lease lifecycle | No |
| Heterogeneous placement using Node labels, taints, selectors, affinity, and topology-domain labels | Yes | No |
| Resource exhaustion and later rescheduling after allocatable increases or Pods release requests | Yes | No |
| Aggregate health loss/recovery for future placements | Yes: keep capacity fixed, change allocatable | No |
| Exact Device Plugin registration and reconnect behavior | No | Yes |
| Device IDs, uniqueness, per-container assignment, and checkpoint recovery | No | Yes |
| `ListAndWatch` stream semantics | Aggregate result only | Yes |
| Device Plugin `GetPreferredAllocation` | No | Yes |
| `Allocate` side effects and response handling | No | Yes |
| `PreStartContainer` before every container start | No | Yes |
| Device Plugin NUMA hints and Topology Manager admission | No | Yes |
| Assigned-device health in Pod status | No honest native path | Yes |
| CDI edits inside a running container | No | Yes, plus CDI spec and capable runtime |
| DRA class/slice/claim allocation and scheduler placement | Yes, on a DRA-capable control plane | No |
| DRA `NodePrepareResources`, `NodeUnprepareResources`, CDI delivery, and node health stream | No | Yes |
| Real Accelerator computation or performance | No | Outside this effort |

## Implications for the blocked architecture decision

1. **Name the default mode accurately.** Use “scheduler-visible extended-resource simulation,” not “Device Plugin simulation.” The latter promises RPC and runtime behavior that an API-only Synthetic Node cannot produce.
2. **Define a narrow backend interface around observable outcomes.** The core Scenario should declare desired Synthetic Nodes, vendor resource quantities, health transitions, labels, and topology domains. An extended-resource backend reconciles Node/status/Lease objects; it should not expose fake device IDs merely to resemble Device Plugin internals.
3. **Keep DRA as a separate compiler/backend.** A DRA profile compiles model attributes and capacities into `DeviceClass` and `ResourceSlice` objects and validates claim allocation/binding. It has different objects, version requirements, cleanup rules, and scheduler semantics from Node scalar resources.
4. **Reserve protocol fidelity for the end-to-end harness.** A later harness can run a real kubelet or compatible node agent, a mock Device Plugin/DRA driver, and a CDI-capable runtime. Its assertions should cover RPC order, ID allocation, health streams, topology, CDI, prepare/unprepare, and container startup independently of the default CLI path.
5. **Make validation tiers explicit in status/output.** Suggested tiers are `scheduling`, `dra-control-plane`, and `node-runtime`. A successful lower tier must never be reported as evidence for a higher one.
6. **Treat Node lifecycle as part of the backend, not the Scenario vocabulary.** Stable scheduling requires Ready conditions and heartbeats as well as resource quantities. The backend must reconcile only Scenario Instance-owned Nodes and must clean up only those objects.
7. **Plan for elevated but bounded RBAC.** Creating cluster-scoped Nodes and writing their status/Leases requires more authority than namespaced workload creation. DRA additionally uses cluster-scoped classes/slices and fine-grained claim-status authorization. Separate these permissions by backend and document the exact preflight requirements. ([Node authorization model](https://kubernetes.io/docs/reference/access-authn-authz/node/), [DRA hardening](https://kubernetes.io/docs/concepts/security/hardening-guide/dynamic-resource-allocation/))
8. **Feature-detect instead of version-guessing.** Probe API discovery for `resource.k8s.io/v1` and optional fields/gates before offering DRA modes. Keep extended-resource Node status as the broad-compatibility baseline.

## Limitations and open evidence boundaries

- This report establishes the upstream Kubernetes contract; it does not select KWOK or another Synthetic Node backend and does not prove that any candidate backend implements status, Lease, Pod, or DRA behavior correctly.
- It does not catalog vendor resource names, labels, or model attributes. Those belong to the Vendor Profile evidence research.
- It does not claim that API-accepted status written by the simulator was produced by kubelet, Device Manager, or a hardware driver. Source presence and fabricated object state remain distinct from wired runtime behavior.
- Kubernetes v1.37 is upcoming on the research date. KEP-5945 may introduce optional DRA node preparation there, but it is not a v1.36 runtime contract and must not influence the initial compatibility claim. ([release status](https://kubernetes.io/releases/), [KEP-5945](https://github.com/kubernetes/enhancements/blob/e32008ea3ed16998fca89b72754bc7c598a07679/keps/sig-node/5945-dra-optional-node-preparation/kep.yaml#L16-L38))
- Current DRA has additional beta/alpha extensions beyond this project's initial scheduling boundary. Their presence in API/source does not make them portable baseline requirements; each must be separately feature-detected and tested.
