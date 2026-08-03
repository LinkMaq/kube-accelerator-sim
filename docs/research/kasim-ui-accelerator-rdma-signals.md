# Kasim UI accelerator and RDMA Kubernetes-visible signals

- Status: research snapshot for [Research: 主流加速器与 RDMA 的 Kubernetes
  可见信号](https://github.com/LinkMaq/kube-accelerator-sim/issues/36)
- Evidence cut-off: 2026-08-03
- Scope: read-only signals available to a `kasim ui` client through the
  Kubernetes API of one explicit Simulation Target
- Kubernetes floor: 1.30; stable DRA inventory is capability-detected on
  Kubernetes 1.34+

## Decision summary

The Cluster Simulation Inventory must preserve the distinction between a
scheduler-visible signal and physical device capability.

1. A Device Plugin extended resource provides a scalar `capacity` and
   `allocatable` value through a Node. It does not provide device IDs, model,
   topology, or per-device health through the Kubernetes API.
2. Exact, source-backed Node labels may add model or topology identity, but a
   label is not a schedulable device and does not prove that a device works.
3. A stable DRA `ResourceSlice` can provide driver, pool, per-device name,
   attributes, capacity, and node reachability. `ResourceClaim.status` can
   identify allocated devices. Those are the only portable cluster-API sources
   for device-level rows in the first UI release.
4. The RDMA Shared Device Plugin and SR-IOV Network Device Plugin both use
   administrator-configurable resource names. Their quantities represent
   allocatable tokens or selected PCI/network functions, not installed NIC
   count or a working RDMA data plane.
5. Kasim may simulate an Auxiliary Device Signal, including an RDMA scalar
   resource, but it must not claim to create a NIC, driver, CNI path, InfiniBand
   fabric, RoCE configuration, GPUDirect path, or physical network health.

The verified accelerator rows below may seed built-in Vendor Profiles. The
RDMA examples may seed generic Auxiliary Device Signal templates only when the
exact resource name and count are selected explicitly by the scenario. A
community-only or incomplete contract remains provisional.

## Evidence classes

| Class | Evidence rule | Profile policy |
| --- | --- | --- |
| A | Exact contract in a vendor-owned source tree, vendor documentation, Kubernetes SIG source, or official managed-service documentation. | Eligible for a verified built-in contract, pinned to the cited revision or document version. |
| B | First-party material documents part of the contract, but a fully qualified resource name or another material behavior is missing. | Catalog/model metadata only, or provisional with no runnable default. |
| C | An upstream community integration publishes an exact contract that is not established as the vendor default. | Provisional, named after the integration, and never presented as first-party. |
| D | Product evidence exists but no exact public Kubernetes scheduling contract was found. | Do not emit a resource name, label, or DRA driver guess. |

“Verified” here means that the Kubernetes-visible contract is reproducible
from primary evidence. It does not mean that Kasim or this research validated
the vendor software on physical hardware.

## Kubernetes observability boundary

### Extended resources

The Device Plugin API's `ListAndWatch` message contains a device `ID`,
`Healthy` or `Unhealthy` state, and optional NUMA `TopologyInfo`. Kubelet turns
the healthy count into a Node extended resource. The Kubernetes documentation
also describes device IDs and topology in the node-local PodResources API, not
in the Node object ([Device Plugin API and topology](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)).

Consequently, a kubeconfig-based UI can safely read:

- exact extended resource keys and quantities from
  `Node.status.capacity` / `Node.status.allocatable`;
- exact source-backed identity labels from `Node.metadata.labels`; and
- requested quantities from Node-bound Pod resource requirements, using
  Kubernetes scheduling semantics for regular, init, and Pod-level resources.
  Kubernetes defines those as scheduler accounting, not runtime utilization
  ([extended resource semantics](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources)).

It cannot safely derive:

- Device Plugin IDs, UUIDs, PCI addresses, or per-device rows;
- which physical device satisfies a Pod request;
- the reason why `allocatable` is lower than a previously observed value; or
- hardware health from `allocatable > 0`.

For real Nodes, the UI must display scalar resources as count-only and health
as **unknown / not published**. Kasim-owned Synthetic Nodes may show simulated
healthy counts from Scenario Instance state, clearly marked as simulated.

### Dynamic Resource Allocation

Stable `resource.k8s.io/v1` became available by default in Kubernetes 1.34;
Kubernetes 1.30 through 1.33 do not provide that stable API
([Kubernetes 1.34 DRA graduation](https://kubernetes.io/blog/2025/08/27/kubernetes-v1-34-release/#stable-the-core-of-dra-is-ga)).
The UI therefore must discover `resource.k8s.io/v1` and degrade cleanly when it
is absent or forbidden.

A `ResourceSlice` publishes the unique tuple `<driver, pool, device name>`,
attributes, capacities, and node availability. Consumers must use the latest
complete pool generation, not concatenate stale shards
([ResourceSlice semantics](https://kubernetes.io/docs/reference/kubernetes-api/resource/)).
`ResourceClaim.status.allocation.devices.results` records the selected driver,
pool, and device names
([ResourceClaim v1 API](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)).

The UI may therefore show per-device rows for DRA and link allocated claims to
their devices. A ResourceSlice device name is a driver-scoped identifier, not
automatically a hardware serial number. Only a separately published attribute,
such as a source-backed `uuid`, may be labeled as a UUID.

DRA does not make health universally portable. Resource health reporting and
some device condition/taint mechanisms have had separate feature gates, and
drivers choose what to implement
([Kubernetes DRA health status](https://kubernetes.io/blog/2025/09/17/kubernetes-v1-34-pods-report-dra-resource-health/)).
The UI must show a health state only when the exact API field and driver
contract are present; otherwise it remains unknown.

## Accelerator signal matrix

All extended-resource rows below remain count-only for real Nodes. “Plugin
health” means that the cited Device Plugin participates in kubelet health
updates; it does not make per-device health observable through the Kubernetes
API.

### Verified first-party and provider contracts

| Ecosystem | Exact scheduling resources and units | Source-backed identity or detail | DRA | Evidence and admission |
| --- | --- | --- | --- | --- |
| NVIDIA | `nvidia.com/gpu` (device); `nvidia.com/gpu.shared` (shared token when explicitly renamed); source-backed MIG examples such as `nvidia.com/mig-1g.10gb`, `nvidia.com/mig-2g.20gb`, and `nvidia.com/mig-7g.80gb` (partition) | GFD labels include `nvidia.com/gpu.product`, `nvidia.com/gpu.count`, `nvidia.com/gpu.memory`, and `nvidia.com/mig.capable`; Plugin health is not a cluster-API per-device signal | Driver / DeviceClass `gpu.nvidia.com`; `ResourceSlice` devices include names and namespaced attributes such as `productName`, `uuid`, architecture and memory capacity | A, vendor-owned [Device Plugin at the catalog revision](https://github.com/NVIDIA/k8s-device-plugin/tree/5f27eeeee7eb7f7a4c0581aa10abeda7e4604ed2), [GFD labels](https://github.com/NVIDIA/k8s-device-plugin/blob/5f27eeeee7eb7f7a4c0581aa10abeda7e4604ed2/docs/gpu-feature-discovery/README.md), and SIG-owned [DRA device publisher](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/16c671c4f7f1e9d3de0d46bb9f1d9b9a6a449fa6/cmd/gpu-kubelet-plugin/deviceinfo.go); verified |
| AMD Instinct | `amd.com/gpu` (device or partition under `single` naming); `amd.com/cpx_nps4`, `amd.com/spx_nps1`, `amd.com/cpx_nps1` (partition under `mixed` naming) | Labels include `amd.com/gpu.product-name`, `amd.com/gpu.vram`, `amd.com/gpu.device-id`, `amd.com/gpu.cu-count`, `amd.com/gpu.family`, and driver version | Driver / DeviceClass `gpu.amd.com`; devices publish `productName`, device/partition type, NUMA, device ID, partition profile, memory, compute-unit and SIMD capacities | A, vendor-owned [Device Plugin guide](https://instinct.docs.amd.com/projects/k8s-device-plugin/en/latest/user-guide/configuration.html) and pinned [DRA device publisher](https://github.com/ROCm/k8s-gpu-dra-driver/blob/ecb882a4fda352a282e845ec1c0ed62a73793f68/cmd/gpu-kubeletplugin/deviceinfo.go); verified |
| Intel Data Center GPU | `gpu.intel.com/i915` and `gpu.intel.com/xe` (driver-visible device instances) | No stable board-model Node label is part of the Device Plugin contract; `shared-dev-num` produces shared instances, not additional boards | No source-backed built-in DRA contract in this snapshot | A, vendor-owned [GPU Device Plugin](https://github.com/intel/intel-device-plugins-for-kubernetes/blob/6460392f95275dd68774aeef3c39f14538ddb3d9/cmd/gpu_plugin/README.md); verified |
| Intel Gaudi | `habana.ai/gaudi` (device) | Resource is generation-neutral; Plugin reports discovered device health to kubelet but no stable model label is documented | None verified | A, vendor-owned [Gaudi Device Plugin](https://github.com/HabanaAI/gaudi-device-plugin/tree/2d8e327c8f31dcc996467e3c42924aae206a71e0) and [workload guide](https://docs.habana.ai/en/latest/Quick_Start_Guides/Kubernetes_Quick_Start.html); verified |
| Huawei Ascend | `huawei.com/Ascend310`, `huawei.com/Ascend310P`, `huawei.com/Ascend910` (device); opt-in `huawei.com/npu-core` (virtual core) | Model family is encoded in the three physical resource suffixes; no stable cross-version Node model label was established | None verified | A, vendor-owned [constants](https://gitee.com/ascend/ascend-device-plugin/blob/4cd80a88d37c4b1dfcf17fd1b4f7c8f784e66e01/pkg/common/constants.go) and [virtual device parser](https://gitee.com/ascend/ascend-device-plugin/blob/37362c31c7017a2f297b5a6887c1b5e6618775ab/pkg/common/device.go); verified. Do not invent `Ascend910B` or `Ascend910C` resource keys |
| Hygon DCU | `hygon.com/dcu` (device); evidenced vDCU examples `hygon.com/dcu-share-30c-16g`, `hygon.com/dcu-share-36c-16g`, `hygon.com/dcu-share-36c-5g`; evidenced MIG examples `hygon.com/dcu-mig-2g-15gb`, `hygon.com/dcu-mig-4g-31gb` (partition) | Labels: `hygon.com/dcu`, `hygon.com/dcu.name`, `hygon.com/dcu.cu-count`, `hygon.com/dcu.vram` | None verified | A, vendor-operated [standard/vDCU guide](https://developer.sourcefind.cn/document/87ee5c5b-c10d-11f0-b077-0242ac150003?id=44dc5a98-c8d9-11f0-8db6-0242ac150003) and [MIG guide](https://developer.sourcefind.cn/document/87ee5c5b-c10d-11f0-b077-0242ac150003?id=4cca45d7-c8d9-11f0-8db6-0242ac150003); verified for only the cited exact variants |
| MetaX | `metax-tech.com/gpu` (device), `metax-tech.com/vfio-gpu` (VFIO-bound passthrough device), `metax-tech.com/sgpu` (software-split virtual device) | Topology labels: `metax-tech.com/gpu.topology.scores` and `metax-tech.com/gpu.topology.losses`; no stable public model label | None verified | A, vendor-owned [Kubernetes component guide](https://developer.metax-tech.com/api/client/document/preview/1269/k8s/03_component.html); verified |
| Moore Threads | `mthreads.com/gpu` (device); `mthreads.com/sgpu-core` plus `mthreads.com/sgpu-memory` (paired virtual compute and memory) | `mthreads.com/gpu=enable` is a Node deployment gate, not an extended resource or model value | None verified | A, vendor-owned [KUAE guide](https://docs.mthreads.com/en/cloud-native/cloud-native-doc-online/install_guide/); verified for the documented surface |
| Cambricon | `cambricon.com/mlu` (device); optional device-type `cambricon.com/mlu370`; `cambricon.com/mlu370.share` (shared token); `cambricon.com/mlu370.mim-2m.8gb` (partition) | Legacy Node labels include unprefixed `Model`, `DriverVersion`, `MCUVersion`, and `CPUType` | None verified | A, vendor-owned [Device Plugin README](https://github.com/Cambricon/cambricon-k8s-device-plugin/blob/cc0f7735f208810df14c4178a888d2ad50613a8d/device-plugin/README.md) and [resource construction](https://github.com/Cambricon/cambricon-k8s-device-plugin/blob/cc0f7735f208810df14c4178a888d2ad50613a8d/device-plugin/pkg/mlu/server.go#L543-L569); verified |
| Biren | `birentech.com/gpu` (device); `birentech.com/1-4-gpu`, `birentech.com/1-2-gpu` (pre-created SVI partitions) | No stable public model label; the exact fractions do not authorize arbitrary generated fraction names | None verified | A, vendor-owned [Device Plugin at the inspected revision](https://gitee.com/BirenTechnology/k8s-device-plugin/tree/a9984054f975d3430c61cd1f068691b7137da9a6); verified |
| Iluvatar CoreX | `iluvatar.com/gpu`; its unit can be board, chip in split-board mode, or shared token under time-slicing | No stable public model label; mode must accompany the count. `iluvatar.ai/gpu` is a legacy compatibility spelling, not the current default | None verified | A, vendor-owned [Device Plugin](https://gitee.com/deep-spark/ix-device-plugin/tree/2fcca7862b62c002ed4ba45169efb82b7433e5f8) and [mode documentation](https://gitee.com/deep-spark/ix-device-plugin/blob/2fcca7862b62c002ed4ba45169efb82b7433e5f8/README.md); verified |
| Enflame | `enflame.com/gcu` (device), `enflame.com/shared-gcu` (shared token), `enflame.com/drs-gcu` (dynamic split), `enflame.com/gcu-count` (count/accounting signal) | Device records contain product model internally, but no stable public Node model label is established | None verified | A, vendor-owned [configuration](https://github.com/EnflameTechnology/gcushare/blob/ee04a65c2e28b397f49d8aced1be11d761856074/config/topscloud.json) and [constants](https://github.com/EnflameTechnology/gcushare/blob/ee04a65c2e28b397f49d8aced1be11d761856074/gcushare-device-plugin/pkg/consts/const.go); verified |
| FuriosaAI | `furiosa.ai/rngd` (RNGD device/card) | No separate public model label; hardware SR-IOV support does not create an evidenced partition resource name | None verified | A, vendor-owned [Device Plugin](https://github.com/furiosa-ai/furiosa-device-plugin/tree/f09450c6025dbe6f435bb1e191c906ca7dcece7c); verified |
| Graphcore | `c600.graphcore.ai/ipu` (IPU) | Domain identifies the C600 platform; no separate per-card Node label is documented | None verified | A, vendor-owned [IPU workload guide](https://docs.graphcore.ai/projects/kubernetes-ipu-device-plugin-user-guide/en/latest/createpod.html); verified, provider scope `graphcore-c600` |
| AWS Neuron | `aws.amazon.com/neuron` (whole device), `aws.amazon.com/neuroncore` (NeuronCore) | Extended-resource key does not encode Inferentia/Trainium generation | DeviceClass / driver `neuron.aws.com`; `ResourceSlice` plus `instanceType` and topology attributes; claims identify allocated devices | A, provider-owned [Device Plugin documentation](https://awsdocs-neuron.readthedocs-hosted.com/en/latest/deploy/infrastructure/plugins.html) and [Neuron DRA guide](https://awsdocs-neuron.readthedocs-hosted.com/en/latest/deploy/eks/dra.html); verified, provider scope `aws-ec2-eks` |
| Google Cloud TPU | `google.com/tpu` (TPU chip on a GKE TPU slice Node) | `cloud.google.com/gke-tpu-accelerator` and `cloud.google.com/gke-tpu-topology` | No portable vendor DRA contract in this snapshot | A, provider-owned [TPUs in GKE](https://cloud.google.com/kubernetes-engine/docs/concepts/tpus); verified, provider scope `google-gke` |

### Provisional and non-runnable contracts

| Ecosystem | Public signal | Provenance and gap | Admission |
| --- | --- | --- | --- |
| Kunlunxin | HAMi publishes `kunlunxin.com/xpu`; its VXPU integration publishes `kunlunxin.com/vxpu` and `kunlunxin.com/vxpu-memory` | C, upstream community [HAMi integration source](https://github.com/Project-HAMi/HAMi/tree/e831337db299f331b170a46d6ca3dba256b9d6f1). No first-party public Device Plugin contract establishes these as vendor defaults | Provisional profile `kunlunxin-hami`, with `providerScope: hami-integration` |
| Vastai | HAMi publishes `vastaitech.com/va` | C, upstream community [HAMi integration source](https://github.com/Project-HAMi/HAMi/tree/e831337db299f331b170a46d6ca3dba256b9d6f1). No vendor-owned public baseline was found | Provisional profile `vastai-hami` |
| Qualcomm Cloud AI 100 | Official package documentation names suffix choices `qaic`, `qaic-std`, `qaic-pro`, and `qaic-ultra` | B, vendor-owned [Cloud AI SDK guide](https://quic.github.io/cloud-ai-sdk-pages/1.20/Getting-Started/Installation/Docker/k8s/index.html) does not publish the fully qualified namespace | Catalog models only; never prepend a guessed domain |

An ecosystem for which the survey finds product identity but no exact public
Device Plugin or DRA contract remains grade D. The UI may show an unrecognized
extended resource verbatim as `custom / unknown`, but must not assign a vendor
or model from name similarity.

## Vendor DRA detail that the UI may expose

The first version should use a namespaced allowlist for interpreted attributes
and still retain unknown attributes as raw, typed values.

| Driver | Interpretable detail | Health rule |
| --- | --- | --- |
| `gpu.nvidia.com` | Device name such as `gpu-<minor>`; attributes `type`, `uuid`, `productName`, `brand`, `architecture`, `cudaComputeCapability`, `driverVersion`, `cudaDriverVersion`, standard NUMA attribute, and memory capacity are published by the pinned [SIG source](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/16c671c4f7f1e9d3de0d46bb9f1d9b9a6a449fa6/cmd/gpu-kubelet-plugin/deviceinfo.go). DeviceClasses also distinguish full GPU and MIG. | Only interpret source-backed DRA taints or resource-health status when the cluster advertises the required API/feature. NVIDIA's source defines XID, GPU-lost, and unmonitored taints, but these are not a portable boolean health field ([health publisher](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/16c671c4f7f1e9d3de0d46bb9f1d9b9a6a449fa6/cmd/gpu-kubelet-plugin/device_health.go)). |
| `gpu.amd.com` | Device name `gpu-<card>-<render>`; `type`, `productName`, `numaNode`, optional driver/device/partition attributes, and memory/compute-unit/SIMD capacities are published by the pinned [AMD source](https://github.com/ROCm/k8s-gpu-dra-driver/blob/ecb882a4fda352a282e845ec1c0ed62a73793f68/cmd/gpu-kubeletplugin/deviceinfo.go). | No portable health interpretation is admitted by the current built-in profile; display unknown unless a future pinned contract adds it. |
| `neuron.aws.com` | `ResourceSlice` devices and `instanceType` are documented; claims can select all devices matching an instance type ([AWS Neuron DRA](https://docs.aws.amazon.com/eks/latest/userguide/device-management-neuron.html)). | No portable health interpretation is admitted; display unknown unless the actual API reports source-backed device status. |

Never replace `<driver, pool, device name>` with a UUID attribute. Show both
when both exist, with separate labels such as “DRA device name” and “vendor
UUID”.

## RDMA, SR-IOV, and network-operator signals

### RDMA Shared Device Plugin

The NVIDIA/Mellanox upstream plugin supports InfiniBand and RoCE HCAs. Its
configuration requires a `resourceName`, defaults `resourcePrefix` to `rdma`,
requires `rdmaHcaMax`, and selects host devices by vendor ID, device ID, driver,
interface name, or link type
([pinned configuration contract](https://github.com/Mellanox/k8s-rdma-shared-dev-plugin/blob/3beab3b1c014b976dc1da80d0dfa2ccbebec0513/README.md#rdma-shared-device-plugin-configurations)).

Examples therefore produce full extended resources such as:

- `rdma/hca_shared_devices_a` when `resourceName` is
  `hca_shared_devices_a` and the default prefix is used; or
- `rdma/rdma_shared_device_a` for the current NVIDIA Network Operator example
  with `resourceName: rdma_shared_device_a`
  ([Network Operator deployment](https://docs.nvidia.com/networking/display/kubernetes25100/deployment-guide-kubernetes.html#networkoperatordeploymentguidewithkubernetes-networkoperatordeploymentwithrdmashareddeviceplugin)).

Neither spelling is a universal default. The administrator chooses the suffix,
and may override the prefix. `rdmaHcaMax` is the maximum number of shareable
RDMA resource tokens exposed by that plugin resource; it is not the physical
HCA or port count. The UI may display capacity, allocatable, requested tokens,
and the configured provenance, but must label physical adapter count and link
health as unknown.

The plugin documents that RDMA-capable hardware and the RDMA kernel stack are
prerequisites. A custom NFD rule may publish
`feature.node.kubernetes.io/custom-rdma.available=true`, but that key is an
example custom label, not a standard Kubernetes or vendor guarantee
([deployment label guidance](https://github.com/Mellanox/k8s-rdma-shared-dev-plugin/blob/3beab3b1c014b976dc1da80d0dfa2ccbebec0513/README.md#rdma-shared-device-plugin-deployment-with-node-labels)).

### SR-IOV Network Device Plugin

The Kubernetes Network Plumbing WG plugin discovers PCI PFs, VFs, and auxiliary
network devices. Both `resourceName` and `resourcePrefix` are configurable; the
prefix defaults to `intel.com`, but that default does not mean the device is an
Intel NIC. The plugin supports `netDevice`, `accelerator`, and `auxNetDevice`
pools, NUMA topology, and selectors including vendor/device/driver, PCI address,
PF name, link type, PKey, and `isRdma`
([pinned upstream configuration](https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/efe22f8722ceae918c6703830107b3e82b089ef1/README.md#configurations)).

An example `resourceName: sriov_net_A` with the default prefix becomes
`intel.com/sriov_net_A`; an operator may instead publish any valid owned domain.
`isRdma: true` asks the allocation path to mount RDMA resources. It does not
change the extended-resource name and, by itself, is not proof that an RDMA
fabric, CNI network, routing, PKey, RoCE lossless configuration, or end-to-end
communication works.

The plugin explicitly does not bind drivers or create VFs/PFs; those are
deployment prerequisites. Thus a simulated SR-IOV resource promises only the
selected scalar scheduling surface and optional simulated topology metadata.

### Network Operator and CNI objects

NVIDIA Network Operator's `NicClusterPolicy` / `NicNodePolicy` can deploy the
OFED driver, RDMA Shared Device Plugin, SR-IOV Device Plugin, Multus, and CNI
components. Its API describes the RDMA plugin as managing IB and RoCE HCAs
through the Device Plugin framework
([current API reference](https://docs.nvidia.com/networking/display/kubernetes2640/customizations/crds.html)).
Those CRs are operator desired state and status; they do not replace the Node
extended resource as the scheduling signal.

The upstream RDMA CNI moves an RDMA interface associated with a selected
network interface into a container network namespace. It requires an
RDMA-capable SR-IOV NIC, kernel support, the SR-IOV Device Plugin, Multus, and a
fabric CNI
([pinned RDMA CNI requirements](https://github.com/k8snetworkplumbingwg/rdma-cni/blob/54204ee361e85c7a193d06ccec8520a2d9eeb831/README.md#deployment-requirements-kubernetes)).
A CNI object or NetworkAttachmentDefinition is therefore evidence of requested
network configuration, not proof of a working physical data plane.

No primary source establishes a portable rule that pairs a particular GPU,
NPU, DCU, or XPU resource with a particular RDMA resource. Even NVIDIA's
Network Operator documents GPU Operator as a separate prerequisite for
GPUDirect RDMA workloads
([Network Operator platform requirements](https://docs.nvidia.com/networking/display/kubernetes2570/platform-support.html)).
Kasim must require an explicit association in a Scenario or Vendor Profile and
must never infer it from co-location on a Node.

## Auxiliary Device Signal schema constraints

The evidence supports a generic schema rather than a vendor-specific `rdma`
boolean:

```yaml
id: rdma-shared-a
kind: extended-resource
resourceName: rdma/rdma_shared_device_a
unit: shared-token
association: explicit
providerScope: any-kubernetes
evidence:
  class: A
  provenance: upstream-device-plugin
  source: https://github.com/Mellanox/k8s-rdma-shared-dev-plugin
  revision: 3beab3b1c014b976dc1da80d0dfa2ccbebec0513
semantics:
  schedulingOnly: true
  physicalCapabilityClaim: none
```

Required durable fields:

- exact signal kind: `extended-resource`, `node-label`, or `dra-device`;
- exact resource name / label key / DRA driver and DeviceClass;
- unit, including `device`, `function`, `partition`, `core`, `memory`, or
  `shared-token`;
- source owner, provenance (`vendor`, `provider`, `kubernetes-sig`, or
  `community`), evidence class, URL, revision, and check date;
- naming and mode preconditions, including any configurable prefix;
- provider scope and an explicit Accelerator association, if any; and
- an unconditional `schedulingOnly` truth boundary for simulated auxiliary
  signals.

Runtime inventory values are separate observations:

- Node `capacity`, `allocatable`, and summed Pod requested quantity;
- DRA driver/pool/device and claim allocation, when supported;
- `source: kasim | cluster-observed` and evidence state;
- `health: simulated | reported | unknown`, never inferred from quantity; and
- stale/permission-denied diagnostics for each unavailable source.

## UI interpretation rules

1. Show exact extended resources on the home page, grouped by Node and known
   profile, with Kasim Synthetic Nodes visually dominant but real Nodes still
   present.
2. A scalar resource produces one aggregate row, not invented device rows or
   IDs. Its count is rendered with its evidenced unit.
3. DRA devices produce individual rows only from a complete latest-generation
   ResourceSlice pool. Link a claim only from its allocation result.
4. Show capacity, allocatable, and requested separately. “Requested” is
   scheduler reservation, not utilization, throughput, or load.
5. Show health as unknown unless Kasim owns the simulated state or a
   source-backed DRA field is actually present. A decrease in allocatable is an
   observation, not a diagnosis.
6. Preserve raw unknown extended resources and DRA attributes without assigning
   a vendor, unit, model, or capability. Never infer from a domain substring.
7. Shared tokens, virtual cores, memory quantities, and partitions are not
   included in a “physical cards” total.
8. RDMA/SR-IOV resources are Auxiliary Device Signals. Their presence does not
   certify a NIC, driver, CNI, fabric, GPUDirect, or physical network path.
9. Permission failure is partial: keep the available Node/Pod/Scenario data and
   identify which API group was forbidden. Absence of DRA on Kubernetes 1.30–
   1.33 is a capability result, not an error.

## Admission result

- **Verified Accelerator profiles:** NVIDIA, AMD, Intel GPU, Intel Gaudi,
  Huawei Ascend, Hygon DCU, MetaX, Moore Threads, Cambricon, Biren, Iluvatar,
  Enflame, FuriosaAI, Graphcore, AWS Neuron, and Google Cloud TPU, limited to
  the exact resources and labels in the matrix.
- **Verified DRA interpretations:** `gpu.nvidia.com`, `gpu.amd.com`, and
  `neuron.aws.com`, capability-detected and limited to source-backed
  attributes.
- **Verified generic auxiliary templates:** configurable RDMA Shared Device
  Plugin and SR-IOV Network Device Plugin scalar contracts. No universal
  resource name or Accelerator pairing exists.
- **Provisional integrations:** Kunlunxin and Vastai through HAMi, named and
  scoped as community integrations.
- **Not runnable from public evidence:** Qualcomm Cloud AI 100 and all grade-D
  ecosystems until an exact fully qualified public contract is available.

These findings constrain a future implementation; they do not add product
code, Profile entries, or simulated network behavior.
