# Kubernetes Accelerator Simulation Backend Landscape

Research date: 2026-07-29
Decision ticket: [Compare lightweight Kubernetes simulation backends](https://github.com/LinkMaq/kube-accelerator-sim/issues/6)

## Executive decision

Use a **layered backend**, not one tool for every fidelity level:

1. **Default product backend: KWOK-assisted Synthetic Nodes.** The project owns the Scenario, Scenario Instance, Vendor Profiles, generated `Node` / DRA objects, ownership metadata, reconciliation, and cleanup. A pinned KWOK deployment supplies only the synthetic kubelet behavior that is expensive to reimplement correctly: Node status/Lease heartbeats and simulated Pod lifecycle. KWOK is already designed to run against an existing API server, select only annotated fake Nodes, and maintain thousands of Nodes with low resource use ([architecture](https://kwok.sigs.k8s.io/docs/design/architecture/), [managed-node selectors](https://kwok.sigs.k8s.io/docs/user/kwok-manage-nodes-and-pods/), [published scale](https://kwok.sigs.k8s.io/), [v0.8.0 release](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0)).
2. **Resource-expression adapters:**
   - Legacy extended-resource mode writes the source-backed Vendor Profile resource name into owned Synthetic Node `status.capacity` and `status.allocatable`. Kubernetes explicitly supports node-level extended resources through Node status and schedules them as opaque, integer, non-overcommittable resources ([extended-resource administration](https://kubernetes.io/docs/tasks/administer-cluster/extended-resource-node/), [resource accounting](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources)).
   - DRA mode reconciles owned `DeviceClass`, `ResourceSlice`, and test workload claims when the Simulation Target exposes compatible `resource.k8s.io` APIs. DRA is stable in Kubernetes v1.35 and represents device attributes, capacity, pools, and node accessibility directly ([DRA concepts](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/), [ResourceSlice API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)).
3. **Protocol-fidelity E2E backend: a disposable kind cluster with real kubelets.** A small fake legacy Device Plugin and a DRA test driver run against real kubelet sockets to test registration, `ListAndWatch`, health, `Allocate`, DRA node preparation, and CDI behavior. kind runs conformant Kubernetes nodes bootstrapped with kubeadm and supports multi-node clusters ([kind overview](https://kind.sigs.k8s.io/), [node-image contract](https://kind.sigs.k8s.io/docs/design/node-image/), [v0.32.0 release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0)). This belongs only to the End-to-End Test Harness; the product CLI does not create or destroy clusters.

Do **not** make Virtual Kubelet, Kubemark, SimKube, k3d/K3s, or an existing NVIDIA-only simulator the product's core backend. Each is useful evidence or test infrastructure, but each either misses the required fidelity seam, couples the product to cluster lifecycle, scales poorly for the target, or is vendor-specific.

This recommendation is **provisional pending four prototypes**. The evidence is strong enough to shortlist the architecture, but it does not prove mixed-cluster isolation, truthful DRA state without a kubelet plugin, or the 1,000-Node / 8,000-Accelerator performance target.

## Scope and evidence rules

The target is an existing Simulation Target. The product CLI submits and manages Accelerator Simulation Scenarios; it does not own target-cluster lifecycle. Disposable cluster lifecycle is allowed only in the End-to-End Test Harness.

The required default truth boundary is Kubernetes control-plane and scheduling behavior:

- owned Synthetic Nodes advertise vendor-correct extended resources or DRA devices;
- the real API server and scheduler make placement and accounting decisions;
- capacity, availability, health, labels, topology, and Scenario Instance lifecycle can change;
- no real Accelerator computation, host driver, device file, or runtime is required;
- existing real Nodes are not mutated by default.

Evidence labels used below:

- **Verified** — directly documented or implemented by the project's own current documentation/source.
- **Inference** — a design conclusion from verified interfaces; it still needs a project prototype.

Only official documentation, maintained upstream repositories, and source owned by the evaluated project were used. Repository activity was checked on 2026-07-29; release links pin the current release where one exists.

## Elimination criteria

A default backend is eliminated if any of the following is true:

1. It cannot safely attach to an existing Simulation Target.
2. It requires product-owned cluster creation or a separate simulation control plane.
3. It mutates unowned real Nodes in its normal path.
4. It cannot plausibly represent 1,000 Nodes and 8,000 Accelerator instances on a developer machine.
5. It makes a vendor-specific simulator the vendor-neutral core.
6. It requires privileged host access or kubelet sockets for the default scheduling-only path.
7. It cannot support deterministic ownership, idempotent updates, status, and cleanup.
8. Its license does not clearly permit reuse or redistribution.

Failure of a default-backend criterion does not eliminate a candidate from a narrower E2E or reference role.

## Decision-oriented comparison

Ratings are relative to this project's declared control-plane-first scope, not general judgments of the projects.

| Candidate | Existing-cluster fit | Control-plane fidelity | Device Plugin fidelity | DRA fidelity | Dynamic Scenario updates | 1,000 Nodes / 8,000 Accelerators | Security / RBAC | Maintenance / license | Coupling | Decision |
|---|---|---|---|---|---|---|---|---|---|---|
| **KWOK-assisted Synthetic Nodes** | **High.** `kwok` runs in-cluster or out-of-cluster against an API server and can select only annotated or labelled fake Nodes ([architecture](https://kwok.sigs.k8s.io/docs/design/architecture/), [selectors](https://kwok.sigs.k8s.io/docs/user/kwok-manage-nodes-and-pods/)). | **High for API/scheduler; low for node runtime.** It updates Node/Pod status and Leases instead of running kubelet or containers ([architecture](https://kwok.sigs.k8s.io/docs/design/architecture/)). | **None by itself.** No kubelet means no kubelet Device Manager socket. Direct Node status can reproduce scheduler-visible counts, not gRPC registration or `Allocate` ([Device Plugin workflow](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)). | **Control-plane only is plausible.** ResourceSlices and scheduler allocation are API/control-plane concerns; node preparation and health gRPC require a driver plus kubelet ([DRA workflow](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#how-resource-allocation-with-dra-works)). | **High.** Nodes and Pods are API objects that can be mutated; Stages can model delayed transitions ([manage Nodes and Pods](https://kwok.sigs.k8s.io/docs/user/kwok-manage-nodes-and-pods/), [Stages](https://kwok.sigs.k8s.io/docs/user/stages-configuration/)). | **Best evidenced.** KWOK publishes reliable maintenance of 1,000 Nodes and 100,000 Pods; 8,000 device records still needs measurement ([KWOK scale](https://kwok.sigs.k8s.io/)). | **Medium.** Upstream v0.8.0 ClusterRole can patch `nodes/status`, patch `pods/status`, mutate/delete Pods, and write Leases/Stages; selectors constrain behavior but RBAC remains cluster-scoped ([upstream RBAC](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/kustomize/rbac/role.yaml)). | Active Kubernetes SIG project, Apache-2.0, [v0.8.0](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0). | **Low if used as an external runtime contract.** Do not expose KWOK Stage CRDs in the Scenario API or import KWOK internals. | **Lead default. Prototype before lock-in.** |
| **Project-owned minimal Synthetic Node reconciler** | **High by design.** It would create only owned Nodes, patch their status, and renew their Leases. Kubernetes defines Node status and Lease heartbeats as the node availability signals ([Node status and heartbeats](https://kubernetes.io/docs/reference/node/node-status/)). | **Same scheduler-visible seam as KWOK**, but the project must implement all lifecycle behavior it needs. | **None.** Direct status is not Device Plugin registration. | **Control-plane only**, if it also reconciles DRA APIs. | **Potentially highest**, because Scenario semantics map directly to owned objects. | **Plausible, unverified.** The object count is modest in extended-resource mode, but controller/API pressure has no evidence yet. | **Potentially narrower than KWOK**, although Kubernetes RBAC cannot label-scope `nodes/status`; application-level ownership checks remain mandatory. | Entire maintenance burden belongs to this project; no new third-party license. | Lowest external coupling, highest code/behavior ownership. | **Shortlist only if prototype shows materially simpler RBAC/operations than pinned KWOK.** |
| **Virtual Kubelet custom provider** | **Medium.** It is a library for custom node agents and can run outside the target cluster ([usage](https://virtual-kubelet.io/docs/usage/), [provider interface](https://virtual-kubelet.io/docs/providers)). | **Medium.** It implements Node and Pod lifecycle through provider interfaces, including capacity, status, logs, exec, and metrics ([current features](https://github.com/virtual-kubelet/virtual-kubelet#current-features)). | **Low.** Its provider abstraction is not the real kubelet Device Manager and exposes no canonical Device Plugin registration path in its documented interface ([provider interface](https://virtual-kubelet.io/docs/providers)). | **Low without substantial custom work.** It does not supply the real kubelet DRA plugin path. | Medium; a custom `NodeProvider` can notify status changes ([NodeProvider](https://virtual-kubelet.github.io/virtual-kubelet/#adding-a-new-provider-via-the-provider-interface)). | **Poorly evidenced.** The standard architecture is a programmable node agent; a 1,000-node implementation would be a project-specific multiplexer with no upstream scale claim. | Medium; provider code and virtual-kubelet need API access, but provider implementations are explicitly expected not to access the API server directly ([provider requirements](https://virtual-kubelet.io/docs/providers)). | Active, Apache-2.0, [v1.13.0](https://github.com/virtual-kubelet/virtual-kubelet/releases/tag/v1.13.0). | High: the project would implement and maintain a provider abstraction unrelated to Accelerator simulation. | **Eliminate as default. No advantage over KWOK for this truth boundary.** |
| **Kubemark / hollow kubelets** | **Low.** Kubemark is a Kubernetes performance-test topology built from a real master plus hollow-node Pods on a base cluster, not a controller added to an arbitrary target ([official Kubemark description](https://kubernetes.io/blog/2016/07/update-on-kubernetes-for-windows-server-containers/), [current source](https://github.com/kubernetes/kubernetes/tree/v1.36.2/test/kubemark)). | **High for kubelet-generated control-plane load.** Hollow nodes reuse kubelet code with runtime/volume behavior mocked ([official description](https://kubernetes.io/blog/2016/07/update-on-kubernetes-for-windows-server-containers/), [hollow-node source](https://github.com/kubernetes/kubernetes/blob/v1.36.2/cmd/kubemark/app/hollow_node.go)). | Unclear for this use case and not an advertised Kubemark contract; fake-runtime hollow nodes are not a supported generic Device Plugin harness. | No packaged Accelerator DRA simulation path. | Low for product Scenario lifecycle; designed for Kubernetes scalability tests. | **Technically scalable but resource-heavy.** The official published figure was about 14 hollow Nodes per CPU core, far heavier than KWOK ([official description](https://kubernetes.io/blog/2016/07/update-on-kubernetes-for-windows-server-containers/)). | High operational privilege and two-cluster complexity. | Maintained inside Kubernetes, Apache-2.0, but not released as an independent product library. | Very high coupling to Kubernetes test internals and topology. | **Eliminate.** |
| **Real kubelet + fake legacy Device Plugin on kind** | **Low for the product; excellent for E2E.** kind owns a disposable cluster, so it violates the product backend boundary but exactly fits the End-to-End Test Harness ([kind scope](https://kind.sigs.k8s.io/)). | **Highest small-scale fidelity.** kind provides conformant Kubernetes and real kubelets ([node-image contract](https://kind.sigs.k8s.io/docs/design/node-image/)). | **Highest.** A fake plugin can exercise the canonical Unix-socket registration, `ListAndWatch`, health, `Allocate`, optional preferred allocation / pre-start, topology, and kubelet restart behavior ([Device Plugin workflow](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/), [Kubernetes test plugin](https://github.com/kubernetes/kubernetes/tree/v1.36.2/test/e2e_node/testdeviceplugin)). | High when paired with a DRA example-derived driver; the upstream example publishes mock GPU ResourceSlices and runs a kubelet plugin ([driver README](https://github.com/kubernetes-sigs/dra-example-driver/tree/v0.4.0), [v0.4.0](https://github.com/kubernetes-sigs/dra-example-driver/releases/tag/v0.4.0)). | High at small scale. | **Not plausible for 1,000 Nodes on a laptop.** Every kind Node is a containerized conformant Kubernetes node; no upstream claim supports this target ([kind overview](https://kind.sigs.k8s.io/)). | **High privilege.** Device Plugin DaemonSets require privileged access and host-mount `/var/lib/kubelet/device-plugins` ([deployment requirements](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-deployment)). | kind and Kubernetes DRA example driver are active Apache-2.0 projects ([kind v0.32.0](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0), [DRA example v0.4.0](https://github.com/kubernetes-sigs/dra-example-driver/releases/tag/v0.4.0)). | Low product coupling if confined behind test fixtures. | **Select for small protocol-fidelity E2E only.** |
| **Real kubelet + fake plugins on k3d/K3s** | Same product-boundary failure as kind; useful only for disposable E2E. k3d creates K3s clusters in containers ([k3d overview](https://k3d.io/), [K3s overview](https://docs.k3s.io/)). | High for kubelet/plugin behavior but lower upstream-distribution fidelity than kind. A K3s agent launches containerd, kubelet, and kube-proxy ([K3s CLI](https://docs.k3s.io/cli)). | High at small scale, subject to K3s paths/configuration. | High at supported K3s versions, but feature/API skew must be managed. | High at small scale. | Not plausible at 1,000 real kubelets on a developer machine. | Same privileged kubelet-socket requirement as any Device Plugin. | Active; k3d is MIT [v5.9.0](https://github.com/k3d-io/k3d/releases/tag/v5.9.0), K3s is Apache-2.0. | Adds K3s distribution differences without solving a requirement kind cannot cover. | **Do not select initially. Revisit only if E2E startup/resource measurements materially beat kind.** |
| **DRA example driver / custom mock DRA driver** | **High as an adapter, not a node backend.** Drivers publish cluster APIs and run node plugins; the upstream quickstart currently uses kind ([README](https://github.com/kubernetes-sigs/dra-example-driver/tree/v0.4.0)). | High for DRA scheduler allocation when the target supports the required API. | Not applicable to the legacy Device Plugin API. | **Highest reference fidelity.** The example exists specifically to demonstrate DRA best practices and exposes mock GPUs with model, version, UUID, and memory attributes ([README and sample ResourceSlice](https://github.com/kubernetes-sigs/dra-example-driver/tree/v0.4.0)). | High; DRA drivers reconcile ResourceSlice generations and device pools ([ResourceSlice semantics](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#resourceslice)). | 8,000 devices are plausible as ResourceSlice entries but **unverified**; DRA scale/scheduler latency needs measurement. | DRA APIs require deliberate persona separation; Kubernetes recommends restricting `DeviceClass` and `ResourceSlice` to admins/drivers ([DRA admin practices](https://kubernetes.io/docs/concepts/cluster-administration/dra/)). | Active Kubernetes SIG project, Apache-2.0, [v0.4.0](https://github.com/kubernetes-sigs/dra-example-driver/releases/tag/v0.4.0). | Low if DRA is an adapter over the same Scenario model; high if the example driver's domain becomes the project's domain. | **Select as design reference and E2E fixture; implement a vendor-neutral adapter, not a fork-shaped core.** |
| **Run:ai Fake GPU Operator** | Medium. It runs in Kubernetes and now documents mixed real/fake Nodes, but its normal identity and cluster objects are NVIDIA-oriented ([README](https://github.com/run-ai/fake-gpu-operator/tree/v0.2.0)). | High for NVIDIA-oriented scheduler/platform observations. In KWOK mode it patches synthetic Node capacity; in real-node mode it supplies fake Device Plugin behavior ([KWOK integration](https://github.com/run-ai/fake-gpu-operator/tree/v0.2.0#-kwok-integration-simulated-nodes)). | High on real kubelet nodes; scheduler-visible only on KWOK nodes. | Strong NVIDIA reference: it documents legacy, DRA, KWOK-DRA, MIG, ComputeDomain, and optional NVML-mock modes ([README](https://github.com/run-ai/fake-gpu-operator/tree/v0.2.0)). | High for configured NVIDIA pools, labels, metrics, and topology. | KWOK-backed scale is plausible, but no direct 1,000/8,000 published benchmark was found. | Medium-to-low for the default requirement: several modes use privileged Pods, host files, RuntimeClass, cluster-scoped objects, or real-node labels; mixed mode requires disabling colliding components ([mock backend and mixed mode](https://github.com/run-ai/fake-gpu-operator/tree/v0.2.0)). | Active, MIT license file, [v0.2.0](https://github.com/run-ai/fake-gpu-operator/releases/tag/v0.2.0). | **Unacceptably vendor-specific as the core**, but valuable as an NVIDIA oracle. | **Reference/differential test only; do not depend on it for the vendor-neutral engine.** |
| **SimKube** | Low for direct submission to the target. It records production traces and replays them in a simulation cluster ([architecture](https://github.com/acrlabs/simkube#overview)). | High for its record/replay purpose; built on KWOK. | None beyond what KWOK provides. | No Accelerator-specific DRA backend. | Trace-driven rather than declarative Scenario Instance lifecycle. | KWOK-backed, but adds machinery not required here. | Production tracer plus simulation-controller permissions increase the surface. | Active, MIT, [v2.7.0](https://github.com/acrlabs/simkube/releases/tag/v2.7.0). | High and orthogonal: trace replay becomes the core abstraction. | **Eliminate. Borrow no runtime dependency; study only if record/replay becomes a future scope.** |
| **Other narrow fake-GPU projects** | Varies; usually assumes real Nodes or owns a kind cluster. | Usually enough for a demo, not a vendor-neutral contract. | Varies. | Limited or absent. | Limited. | No published target-scale evidence. | Often privileged and real-node-mutating. | `chaunceyjiang/fake-gpu` is Apache-2.0 and emulates CUDA/NVML through containerd but still requires NVIDIA Device Plugin or HAMi ([README](https://github.com/chaunceyjiang/fake-gpu)); `kind-gpu-sim` is a cluster-creation script for NVIDIA/AMD and has no repository license file as of the research date ([README](https://github.com/maryamtahhan/kind-gpu-sim)). | High vendor/runtime coupling. | **Eliminate as dependencies. Keep as ecosystem examples only.** |

## What each fidelity level can truthfully claim

### Level 1 — Synthetic extended resources

An owned Synthetic Node can advertise an opaque resource such as `nvidia.com/gpu` or a source-backed vendor equivalent in `status.capacity` / `status.allocatable`. The real scheduler accounts for Pod requests and refuses placements that exceed available integer capacity ([extended resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources), [GPU scheduling rules](https://kubernetes.io/docs/tasks/manage-gpus/scheduling-gpus/)).

This level can truthfully test:

- resource discovery from Node objects;
- inventory and Vendor Profile label interpretation;
- scheduler fit, fragmentation by Node, affinity, taints/tolerations, quotas, priority, and preemption;
- capacity and allocatable changes;
- platform reconciliation against one or many owned Synthetic Nodes.

It cannot truthfully claim Device Plugin registration, device identity allocation, `Allocate`, CDI injection, kubelet checkpointing, container-visible files, or actual computation. Direct Node status is explicitly a supported way to advertise an extended resource, but it bypasses the Device Plugin path ([advertise extended resources](https://kubernetes.io/docs/tasks/administer-cluster/extended-resource-node/), [Device Plugin workflow](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)).

### Level 2 — Synthetic DRA control plane

Owned `ResourceSlice` objects can describe each Accelerator's source-backed model, attributes, capacity, pool, and node accessibility. The real scheduler can filter slices, allocate devices into `ResourceClaim.status`, and place the Pod on an eligible Synthetic Node ([DRA workflow](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#how-resource-allocation-with-dra-works), [ResourceClaim API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)).

This level can truthfully test:

- `DeviceClass` selection and CEL attributes;
- specific device identity and count;
- device pools and node accessibility;
- scheduler-side allocation and claim reservation;
- capacity sharing supported by the installed Kubernetes version;
- platform discovery and display of DRA inventory.

Without a real kubelet and node-local DRA plugin it cannot truthfully claim device preparation, CDI injection, node-local cleanup, kubelet PodResources reporting, or DRA health gRPC. Kubernetes states that after scheduler allocation, the device driver and kubelet configure the device and Pod access; DRA health also flows from the driver to kubelet ([DRA workflow and observability](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#observability-of-dynamic-resources)).

### Level 3 — Real kubelet protocol harness

On a disposable kind worker, a fake plugin can register through `/var/lib/kubelet/device-plugins/kubelet.sock`, stream devices and health via `ListAndWatch`, receive `Allocate`, and exercise restart/re-registration. A DRA driver can publish ResourceSlices and execute its node plugin path. These are the only selected tests that can claim protocol-level fidelity ([Device Plugin implementation](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-implementation), [DRA example driver](https://github.com/kubernetes-sigs/dra-example-driver/tree/v0.4.0)).

This level remains fake hardware. It validates Kubernetes/node protocol behavior, not a vendor driver's real discovery, firmware, runtime, or computation.

## Architecture implications

### Keep the Scenario model above all backends

The core should compile a Scenario into a backend-neutral desired graph:

- Scenario Instance identity and ownership;
- Synthetic Nodes and their lifecycle;
- Vendor Profile and Accelerator Model selections;
- device instances, health, topology, labels, and resource names;
- desired resource-expression mode: extended resource, DRA, or both where explicitly supported;
- optional test workload expectations.

Backend adapters materialize that graph:

- `synthetic/kwok`: Nodes, leases/status contract, optional KWOK configuration;
- `synthetic/native`: only if the prototype wins;
- `resource/extended`: Node status resource counts;
- `resource/dra`: DeviceClasses, ResourceSlices, and optional test claims;
- `e2e/device-plugin` and `e2e/dra`: real-kubelet test fixtures.

**Inference:** This separation keeps KWOK replaceable because the public Scenario API never contains KWOK Stages, labels, or CRDs. It also keeps DRA from becoming a second Scenario model.

### Own objects explicitly

Every created object needs stable Scenario Instance labels/annotations and owner metadata where Kubernetes ownership rules allow it. Apply/update/delete must reconcile only the selected instance. Real Nodes remain outside the ownership set.

KWOK's upstream default selects `kwok.x-k8s.io/node=fake`, while this project requires an additional project-owned instance identity before any status reconciliation or cleanup. Upstream KWOK itself does not create Nodes in its standard ClusterRole; it reads Nodes and patches `nodes/status`, which supports separating Scenario object ownership from KWOK lifecycle simulation ([KWOK RBAC](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/kustomize/rbac/role.yaml), [KWOK configuration](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/kustomize/kwok/kwok.yaml)).

### Treat health according to the selected API

In legacy Device Plugin semantics, an unhealthy device lowers allocatable count while capacity remains unchanged; already assigned Pods stay assigned ([unhealthy-device behavior](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-and-unhealthy-devices)). Synthetic extended-resource mode should mirror that observable result rather than invent a different health contract.

In DRA mode, health and taint behavior depends on Kubernetes version and driver capabilities. Device health through DRA is beta in v1.36 and requires driver gRPC; device taints and other newer features also require feature detection ([DRA health](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#device-health-monitoring), [DRA concepts](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)). The CLI must preflight API discovery and report a truthful degraded capability rather than silently approximating unsupported DRA behavior.

### Use the E2E harness as a contract oracle

The same Scenario fixture should generate:

- a KWOK-backed scheduling test;
- a small kind real-kubelet test;
- expected scheduler-visible Node status, Pod placement, and claim results.

Comparing the outputs detects drift in the low-fidelity backend. Run:ai Fake GPU Operator can serve as an additional NVIDIA differential oracle because it implements both real-node and KWOK/DRA paths, but it must not define the vendor-neutral domain model ([Run:ai Fake GPU Operator v0.2.0](https://github.com/run-ai/fake-gpu-operator/tree/v0.2.0)).

## RBAC and safety findings

1. **KWOK is behaviorally selectable but cluster-wide in authorization.** Its v0.8.0 ClusterRole can patch Node and Pod status, mutate/delete Pods, manage Leases, and manage KWOK CRDs ([RBAC manifest](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/kustomize/rbac/role.yaml)). A product install profile should disable unused interaction/metrics features and test whether a narrower Role set still supports the required Node/Pod stages.
2. **Node status cannot be protected by a label selector in RBAC.** Therefore Scenario ownership checks, an allow-listing selector, admission policy where available, and refusal to manage real Nodes are application invariants, not just permissions.
3. **Legacy Device Plugin fidelity is privileged.** The canonical kubelet plugin directory requires privileged access and a hostPath mount ([Device Plugin deployment](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/#device-plugin-deployment)). This is unacceptable for the default backend and acceptable only in the disposable E2E harness or a future explicit real-node opt-in mode.
4. **DRA permissions must be persona-scoped.** Kubernetes recommends restricting `DeviceClass` and `ResourceSlice` writes to admins/drivers while keeping namespaced `ResourceClaim` permissions separate ([DRA cluster-admin practices](https://kubernetes.io/docs/concepts/cluster-administration/dra/)). The simulator's DRA adapter should use a dedicated driver name and resource ownership boundary.
5. **No implicit context.** The CLI must require or visibly confirm the target kubeconfig context, preflight permissions, and fail before partial mutation when required verbs or APIs are missing.

## Scale assessment

### Extended-resource mode

At 1,000 Nodes and 8 Accelerators per Node, the scheduler-visible Accelerator state is one resource quantity plus source-backed labels per Node, not 8,000 device objects. KWOK already publishes reliable maintenance of 1,000 Nodes and 100,000 Pods ([KWOK scale](https://kwok.sigs.k8s.io/)). **Inference:** the target is plausible, but Scenario reconciliation rate, API server load, scheduler latency, and cleanup still require local measurement because the project adds labels, status updates, ownership, and dynamic changes.

### DRA mode

DRA records individual devices and their attributes in ResourceSlices. Kubernetes uses those slices during claim allocation and scheduling ([ResourceSlice semantics](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/#resourceslice)). **Inference:** 8,000 devices is a reasonable prototype target but cannot be declared supported from current upstream evidence. Required measurements include object bytes, list/watch pressure, allocation latency, update amplification, and scheduler behavior under health/capacity churn.

### Real-kubelet mode

Neither kind nor k3d is a candidate for the 1,000-node target. They run real kubelet/container-runtime stacks per node ([kind overview](https://kind.sigs.k8s.io/), [K3s agent components](https://docs.k3s.io/cli)). Protocol tests should use the smallest topology that covers one node, multi-device allocation, and multi-node scheduling.

## Required prototypes before the backend decision closes

### P0 — KWOK mixed-cluster safety and scale

Against a disposable cluster containing at least one real Node:

- install pinned KWOK v0.8.0 with selector-scoped behavior;
- create only owned Synthetic Nodes;
- verify the real Node's spec, labels, taints, status, and Lease never change;
- apply 1,000 Nodes × 8 extended-resource Accelerators;
- schedule representative single-node/single-card, single-node/multi-card, multi-node/multi-card, heterogeneous-vendor, quota, affinity, taint, priority, and unschedulable workloads;
- update capacity and healthy counts, then delete the Scenario Instance;
- measure creation/update/delete convergence, API request rate, memory/CPU, scheduler latency, and leaked objects;
- test controller and CLI interruption/retry.

Exit criterion: deterministic ownership/cleanup, zero real-Node mutation, and agreed local performance thresholds.

### P0 — KWOK versus minimal native reconciler

Implement the smallest throwaway reconciler that creates owned Nodes, maintains Ready/Lease state, and optionally marks scheduled Pods. Compare it with pinned KWOK on:

- required RBAC;
- code and operational surface;
- status fidelity;
- restart/recovery;
- 1,000-Node resource use;
- mixed-cluster safety.

Exit criterion: choose native only if it is materially smaller/safer without rebuilding a growing subset of KWOK. Otherwise select KWOK as the external runtime.

### P0 — DRA on Synthetic Nodes

On Kubernetes v1.34, v1.35, and v1.36 test clusters where practical:

- discover served `resource.k8s.io` versions and feature gates;
- create source-backed DeviceClasses and per-node ResourceSlices for 8,000 devices;
- allocate claims and schedule Pods onto KWOK Nodes;
- observe claim reservation, Pod status, deletion, device reuse, capacity change, and device removal;
- document which states are produced by scheduler/controllers and which require a kubelet plugin;
- verify that the CLI never reports device preparation or health gRPC when none occurred.

Exit criterion: a precise capability matrix and truthful status model for Synthetic DRA mode.

### P1 — Real-kubelet protocol oracle

In a pinned kind v0.32.0 cluster:

- run a minimal vendor-neutral fake legacy Device Plugin;
- test registration, re-registration after kubelet restart, `ListAndWatch`, healthy/unhealthy transitions, `Allocate`, preferred allocation, NUMA topology, and optional CDI response;
- run a mock DRA driver based on the upstream v0.4.0 example;
- compare scheduler-visible results with the same Scenario compiled for KWOK;
- determine whether a second k3d/K3s job yields enough startup/resource savings to justify its maintenance.

Exit criterion: an E2E fixture that protects the fidelity claims without leaking cluster lifecycle into the product CLI.

## Implication for the blocked backend decision

The backend decision can now be narrowed to one question:

> Does the default Synthetic Node runtime use pinned KWOK as an external implementation detail, or does a minimal project-owned reconciler prove materially simpler and safer?

The other boundaries no longer need to block that choice:

- extended resources and DRA are adapters over one Scenario model, not competing node backends;
- kind is the real-kubelet End-to-End Test Harness, not a product backend;
- Virtual Kubelet and Kubemark are eliminated;
- Run:ai Fake GPU Operator is an NVIDIA reference/oracle, not the core;
- 1,000 Nodes / 8,000 Accelerators is a prototype acceptance target, not a capability inferred from marketing.

**Recommended map gist:** Lead with KWOK-assisted Synthetic Nodes, keep a minimal native reconciler as the only default-backend challenger, layer extended-resource and DRA adapters above it, and use kind for protocol-fidelity E2E.

## Primary source register

- Kubernetes:
  - [Node status and heartbeats](https://kubernetes.io/docs/reference/node/node-status/)
  - [Extended resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources)
  - [Advertise an extended resource](https://kubernetes.io/docs/tasks/administer-cluster/extended-resource-node/)
  - [GPU scheduling](https://kubernetes.io/docs/tasks/manage-gpus/scheduling-gpus/)
  - [Device Plugin framework](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)
  - [Kubelet local sockets and checkpoints](https://kubernetes.io/docs/reference/node/kubelet-files/)
  - [DRA](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
  - [DRA cluster-admin practices](https://kubernetes.io/docs/concepts/cluster-administration/dra/)
  - [ResourceSlice API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
  - [ResourceClaim API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
- KWOK:
  - [Architecture](https://kwok.sigs.k8s.io/docs/design/architecture/)
  - [Manage Nodes and Pods](https://kwok.sigs.k8s.io/docs/user/kwok-manage-nodes-and-pods/)
  - [Stages](https://kwok.sigs.k8s.io/docs/user/stages-configuration/)
  - [Scale claims](https://kwok.sigs.k8s.io/)
  - [v0.8.0 source and release](https://github.com/kubernetes-sigs/kwok/tree/v0.8.0)
- Real-kubelet and DRA test references:
  - [kind](https://kind.sigs.k8s.io/)
  - [kind v0.32.0](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0)
  - [Kubernetes test Device Plugin](https://github.com/kubernetes/kubernetes/tree/v1.36.2/test/e2e_node/testdeviceplugin)
  - [DRA example driver v0.4.0](https://github.com/kubernetes-sigs/dra-example-driver/tree/v0.4.0)
- Evaluated alternatives:
  - [Virtual Kubelet provider interface](https://virtual-kubelet.io/docs/providers)
  - [Virtual Kubelet v1.13.0](https://github.com/virtual-kubelet/virtual-kubelet/releases/tag/v1.13.0)
  - [Kubemark source](https://github.com/kubernetes/kubernetes/tree/v1.36.2/test/kubemark)
  - [k3d](https://k3d.io/)
  - [K3s](https://docs.k3s.io/)
  - [Run:ai Fake GPU Operator v0.2.0](https://github.com/run-ai/fake-gpu-operator/tree/v0.2.0)
  - [SimKube v2.7.0](https://github.com/acrlabs/simkube/releases/tag/v2.7.0)
  - [chaunceyjiang/fake-gpu](https://github.com/chaunceyjiang/fake-gpu)
  - [kind-gpu-sim](https://github.com/maryamtahhan/kind-gpu-sim)
