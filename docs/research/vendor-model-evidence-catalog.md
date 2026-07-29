# Accelerator vendor and model evidence catalog

Status: research snapshot for [issue #10](https://github.com/LinkMaq/kube-accelerator-sim/issues/10)

Evidence cut-off: 2026-07-29

Scope: scheduler-visible Kubernetes contracts for data-center Accelerators

## Decision summary

The simulator should model an Accelerator vendor contract, not manufacture a
different extended resource for every board model.

- Ship a first verified profile set for NVIDIA, AMD, Intel GPU, Intel Gaudi,
  Huawei Ascend, Cambricon, Biren, Iluvatar CoreX, Enflame, Moore Threads,
  FuriosaAI, Graphcore, AWS Neuron, and Google Cloud TPU.
- Keep MetaX, Hygon DCU, Qualcomm Cloud AI 100, and Kunlunxin as provisional
  profiles until their exact public contracts are complete enough to reproduce
  without a vendor package or screenshot.
- Keep SOPHGO, Tenstorrent, Cerebras, SambaNova, Groq, Rebellions, and other
  product-only entries in a discovery backlog. A known product name is not
  evidence for a Kubernetes resource name.
- Represent the board model independently from the Kubernetes resource key.
  Only NVIDIA MIG, AMD partition naming, Cambricon device-type/MIM, Biren SVI,
  and a few documented sharing integrations justify derived resource keys.
- Preserve provenance per profile. Public contracts drift: for example,
  Iluvatar's current source defaults to `iluvatar.com/gpu`, while an older
  package page used `iluvatar.ai/gpu`.

“Verified” below means the scheduler-facing contract is verified from current
first-party source or documentation. It does **not** mean that this project ran
the software on physical hardware.

## Method

### Research questions

For each ecosystem this survey looked for:

1. an official Device Plugin, Dynamic Resource Allocation (DRA) driver, managed
   Kubernetes contract, or vendor-operated developer site;
2. an exact fully-qualified resource name or DRA `DeviceClass`;
3. model identity exposed through resource names, Node labels, DRA attributes,
   device IDs, or documented examples;
4. sharing and hardware partitioning behavior;
5. topology and health semantics; and
6. current or still-supported data-center model families.

Searches started from vendor documentation and vendor-owned source
organizations. Kubernetes and vendor source were preferred over third-party
blogs, marketplace listings, and deployment tutorials. Community integrations
were used only to identify a provisional contract or a gap.

### Evidence grades

| Grade | Admission rule | Simulator policy |
| --- | --- | --- |
| A | Vendor-owned public source, official Kubernetes documentation, or official managed-service documentation exposes the exact contract. | Eligible for a built-in verified profile. |
| B | A first-party public document or package demonstrates the contract, but source, exact labels, or one material behavior is not public. | Built-in only as explicitly provisional and version-pinned. |
| C | A community/operator integration exposes a usable name while the vendor baseline is closed or unavailable. | Custom/experimental profile; never present it as the vendor default. |
| D | Product evidence exists, but no public Kubernetes scheduling contract was found. | Model inventory only; do not create a resource key. |

### Model status

The model list deliberately does not claim market share. Public vendor sources
rarely publish comparable installation counts.

- **K8s-identified**: the model occurs in official plugin/operator
  documentation, a model label, a resource example, or supported-device logic.
- **Current-product**: the model occurs in a current first-party product,
  driver, or platform support page, but may not be exposed by Kubernetes.
- **Deployed-retention**: an older model remains in current driver/operator
  support or official examples and is useful for compatibility scenarios.

This is the defensible interpretation of “mainstream/current and still
deployed.” It avoids turning marketing language into unsupported popularity
rankings.

## Kubernetes contract baseline

The Kubernetes Device Plugin API registers a
`vendor-domain/resource-type` name with kubelet. `ListAndWatch` returns device
IDs, `Healthy`/`Unhealthy`, and optional NUMA `TopologyInfo`; kubelet then
publishes an integer extended resource in Node `status.capacity` and
`status.allocatable`. Extended resources do not carry a model schema. See the
official [Device Plugins documentation](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)
and [v1beta1 API](https://github.com/kubernetes/kubelet/blob/master/pkg/apis/deviceplugin/v1beta1/api.proto).

That boundary matters to an API-submission simulator:

- Node capacity and labels reproduce the scheduler-visible surface.
- A device ID, per-device health transition, allocation response, CDI device,
  or kubelet Topology Manager decision is not represented by Node capacity
  alone.
- DRA uses `ResourceSlice`, `DeviceClass`, `ResourceClaim`, and driver
  attributes instead of only an integer extended resource. DRA profiles need a
  separate rendering path rather than being flattened into fake Device Plugin
  resources.
- Health simulation should change the advertised allocatable count or a
  simulator-owned state record. It must not claim to reproduce a vendor
  diagnostic when only Node status is mocked.

## Grade A: verified public contracts

### Contract matrix

| Ecosystem | Verified Device Plugin / DRA contract | Model identity signal | Sharing, partitioning, topology, and health | Phase-1 model seed |
| --- | --- | --- | --- | --- |
| NVIDIA | Device Plugin resource `nvidia.com/gpu`; mixed MIG resources use `nvidia.com/mig-<slice>g.<memory>gb`. Official DRA driver is [`kubernetes-sigs/dra-driver-nvidia-gpu`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu). | GPU Feature Discovery (GFD) publishes `nvidia.com/gpu.product`, `nvidia.com/gpu.count`, `nvidia.com/gpu.memory`, and MIG labels. IDs may be UUID or index. | [Time-slicing and MPS](https://github.com/NVIDIA/k8s-device-plugin/blob/main/README.md#shared-access-to-gpus) can multiply advertised replicas and optionally rename to `.shared`; MIG is hardware partitioning. Device Plugin reports health, but its README calls comprehensive health checking an outstanding area. | A100 40/80GB, H100, H200, L40S, B200, B300; A800, H800, and H20 as regional/deployed-retention aliases. |
| AMD Instinct | Device Plugin base resource `amd.com/gpu`. Partition-aware naming can expose resources such as `amd.com/cpx_nps4` and `amd.com/spx_nps1`. Official DRA is [`ROCm/k8s-gpu-dra-driver`](https://github.com/ROCm/k8s-gpu-dra-driver) with `gpu.amd.com`. | Official labeller keys include `amd.com/gpu.vram`, `.cu-count`, `.simd-count`, `.device-id`, `.family`, `.product-name`, and `.driver-version`; older installations may use `beta.amd.com/gpu.*`. | [Single naming](https://instinct.docs.amd.com/projects/k8s-device-plugin/en/latest/user-guide/configuration.html) keeps physical/partitioned devices under `amd.com/gpu`; mixed naming exposes partition type. The operator has best-effort topology-aware allocation and can consume exporter health over gRPC to mark a device unavailable. | MI210, MI250X, MI300A, MI300X, MI325X, MI350X, MI355X. |
| Intel Data Center GPU | [`intel-device-plugins-for-kubernetes`](https://github.com/intel/intel-device-plugins-for-kubernetes/tree/main/cmd/gpu_plugin) exposes `gpu.intel.com/i915` and `gpu.intel.com/xe`; monitoring resources are separate. | NFD detects Intel PCI devices; the public Device Plugin contract does not make the board model part of the resource name. | `shared-dev-num` advertises replicas; policies are balanced, packed, or none. Preconfigured SR-IOV VFs are discoverable, but the plugin does not create them. Device Plugin health is healthy by default; Level Zero/xpumd sidecars add memory, PCI, temperature, or severity checks. NUMA topology is reported where available. | Flex 140/170/170V; Max 1100/1550. |
| Intel Gaudi | Official [`HabanaAI/gaudi-device-plugin`](https://github.com/HabanaAI/gaudi-device-plugin) and [Kubernetes install guide](https://docs.habana.ai/en/latest/Installation_Guide/Additional_Installation/Kubernetes_Installation/Deamonsets_Kubernetes_Operator.html) expose `habana.ai/gaudi`. | The resource is vendor-wide; public workload examples do not define a per-model Node label contract. | Lists devices and health. Multi-node training is documented through MPI Operator, not a new extended resource. No public sharing or partition-resource contract was found. | Gaudi2, Gaudi3. |
| Huawei Ascend | Official Ascend Device Plugin, now maintained in [`Ascend/mind-cluster`](https://gitee.com/ascend/mind-cluster), exposes examples including `huawei.com/Ascend310`, `huawei.com/Ascend310P`, and `huawei.com/Ascend910`. | Product identity is commonly encoded in the resource suffix and device IDs/annotations. The public sources do not establish one stable cross-version Node model label. | Device health and fault reporting are implemented. vNPU workflows can expose `huawei.com/npu-core`; templates and scheduler integrations carry extra allocation metadata. Do not infer `Ascend910B` or `Ascend910C` resource names. | Ascend 310, 310P, 910; Atlas training products in the A2 and A3 generations as model aliases, while retaining the verified resource keys. |
| Cambricon MLU | Official [`Cambricon/cambricon-k8s-device-plugin`](https://github.com/Cambricon/cambricon-k8s-device-plugin/tree/cc0f7735f208810df14c4178a888d2ad50613a8d/device-plugin) base is `cambricon.com/mlu`. Enabling device type can create `cambricon.com/mlu370`. | Source publishes legacy unprefixed Node labels `Model`, `DriverVersion`, `MCUVersion`, and `CPUType`; model can also be encoded in a device-type resource. | Modes include default, env-share, MIM, and topology-aware. Sharing appends `.share`; a verified MIM example is `cambricon.com/mlu370.mim-2m.8gb`. MLULink allocation policies include best-effort, guaranteed, and restricted. CNDEV health may be disabled explicitly. | MLU370 and MLU590; MLU270/290 as deployed-retention. |
| Biren | Official [`BirenTechnology/k8s-device-plugin`](https://gitee.com/BirenTechnology/k8s-device-plugin/tree/a9984054f975d3430c61cd1f068691b7137da9a6) exposes `birentech.com/gpu`; pre-created SVI partitions expose `birentech.com/1-4-gpu` and `birentech.com/1-2-gpu`. | No stable public model label was found; resource instances and IDs are not board-model names. | Source supplies NUMA `TopologyInfo`, a topology-aware allocator, health updates, CDI, and preconfigured SR-IOV/Kata paths. The plugin discovers SVI/SR-IOV state; it does not justify arbitrary fractional names. | The first-party-documented [BR100 series](https://www.birentech.com/csr-article/gblwq16edn07xrb9pjky963s/); no per-model resource key. |
| Iluvatar CoreX / 天数智芯 | Current official [`Deep-Spark/ix-device-plugin`](https://gitee.com/deep-spark/ix-device-plugin/tree/2fcca7862b62c002ed4ba45169efb82b7433e5f8) source defaults to `iluvatar.com/gpu`. | No stable public model label. A split-board option treats BI-V150's two chips as two schedulable units. | Time-slicing multiplies replicas of the same resource. Source reports NUMA topology, performs topology-aware selection, and maps device API health failures to `Unhealthy`. The older `iluvatar.ai/gpu` spelling is a compatibility alias, not the current default. | BI-V150 and BI-V150S. |
| Enflame / 燧原 | Official [`EnflameTechnology/gcushare`](https://github.com/EnflameTechnology/gcushare/tree/ee04a65c2e28b397f49d8aced1be11d761856074) defaults to `enflame.com/gcu` and defines `enflame.com/shared-gcu`, `enflame.com/drs-gcu`, and `enflame.com/gcu-count`. | Source device records include product model, but no stable public Node model label contract was found. | Scheduler/device-plugin annotations include `enflame.com/gcu-request-size`, assigned-device fields, and shared/DRS capacity. Source watches device health and transitions fake devices between healthy and unhealthy. No public NUMA or interconnect topology contract was found. | S60 and source-tested `S60G`. |
| Moore Threads / 摩尔线程 | Official KUAE v2.1.0 docs expose `mthreads.com/gpu`; sGPU uses `mthreads.com/sgpu-core` and `mthreads.com/sgpu-memory`. | A Node gate uses `mthreads.com/gpu=enable`; DCGM metrics expose `modelName`, UUID, and CPU affinity, but metrics are not Node labels. | Official docs cover GPU Operator, Device Plugin, device controller, sGPU, and DCGM health monitoring. The implementation package is distributed rather than public source, so model labels and plugin-level health transitions should not be inferred beyond the documented surface. | MTT S3000 is K8s-identified in official DCGM output; S80 and S2000 remain documented examples. |
| FuriosaAI | Official [Device Plugin documentation](https://developer.furiosa.ai/latest/en/cloud_native_toolkit/kubernetes/device_plugin.html) exposes `furiosa.ai/rngd`. | The resource itself identifies the RNGD family; one unit represents one RNGD card. | Plugin discovers devices, registers them, allocates them, and tracks health. RNGD supports hardware multi-instance/SR-IOV, but the public Device Plugin page does not specify separate partition resource names, so the base profile must not invent them. | RNGD. |
| Graphcore | Official Kubernetes IPU Device Plugin exposes [`c600.graphcore.ai/ipu`](https://docs.graphcore.ai/projects/kubernetes-ipu-device-plugin-user-guide/en/latest/createpod.html). | The domain identifies the C600 platform; no public per-card model label contract was found. | Plugin exposes counts, allocates IPUs, and inspects health. V-IPU management is a separate operator/control-plane concern. Public docs do not define fractional sharing or NUMA attributes. | GC200/C600, M2000 and IPU-POD systems as deployed-retention aliases. |
| AWS Neuron | Official Device Plugin exposes `aws.amazon.com/neuroncore` for cores and `aws.amazon.com/neuron` for whole devices. Official DRA uses `neuron.aws.com`. | DRA attributes include EC2 `instanceType`; Device Plugin resources do not encode the silicon generation. | [Neuron DRA](https://awsdocs-neuron.readthedocs-hosted.com/en/latest/deploy/eks/dra.html) selects connected devices and supports logical NeuronCore configuration. Device Plugin allocation differs between whole-device and core resources. The contract is EKS/EC2-specific. | Inferentia/Inf1, Inferentia2/Inf2, Trainium/Trn1, Trainium2/Trn2, Trainium3/Trn3. |
| Google Cloud TPU | GKE exposes `google.com/tpu`; official Node labels include `cloud.google.com/gke-tpu-accelerator` and `cloud.google.com/gke-tpu-topology`. | Accelerator and topology labels are the authoritative scheduler-facing identity. | [GKE TPU documentation](https://cloud.google.com/kubernetes-engine/docs/concepts/tpus) models TPU slices/topologies and often requires consuming all TPU chips on a node. Health and lifecycle are provider-managed. This is a GKE contract, not evidence of a portable on-premises plugin. | TPU v4, v5e, v5p, Trillium v6e, Ironwood TPU7x. |

### Verified model support sources

The seed above is intentionally broader than model names present in resource
keys:

- NVIDIA's current [GPU Operator support matrix](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/platform-support.html)
  retains A100/H100/H200/L40S and adds B200/B300-class systems; its current
  driver matrix also retains A800/H800/H20-family products.
- AMD's current [Instinct cluster networking matrix](https://instinct.docs.amd.com/projects/gpu-cluster-networking/en/latest/reference/hardware-support.html)
  covers MI300X, MI325X, MI350X, MI355X, and pre-MI300 products. The
  [MI300 product page](https://www.amd.com/en/products/accelerators/instinct/mi300.html)
  covers MI300A, MI300X, and MI325X.
- Intel's [Data Center GPU catalog](https://www.intel.com/content/www/us/en/ark/products/series/210574/data-center-gpu.html)
  lists Flex 140/170/170V and Max 1100/1550.
- FuriosaAI documents RNGD's
  [multi-instance and SR-IOV capabilities](https://developer.furiosa.ai/docs/latest/en/overview/rngd.html),
  but those hardware capabilities remain separate from the verified Device
  Plugin resource contract.

## Provisional public integrations

These ecosystems are important coverage targets, but a generated scenario must
carry `evidence: provisional` and the source version. Defaults should not
silently depend on undocumented labels or resource derivation.

| Ecosystem | Publicly evidenced contract | Material gap | Model seed | Grade |
| --- | --- | --- | --- | --- |
| MetaX / 沐曦 | First-party developer material and support examples use `metax-tech.com/gpu`; the allocator is described as topology-aware and health-removing. | Plugin source, exact Node labels from `gpu-label`, and partition/sharing resource names are not public. The evidence is first-party release/support content rather than a reproducible source contract. | C500, C500-P, C500X, C280, C290, C550; N260 is a separate inference family in the official [software release model list](https://developer.metax-tech.com/doc/221). | B |
| Hygon DCU / 海光 | The vendor-operated 光合 developer site demonstrates `hygon.com/dcu`; its service catalog describes Device Plugin registration/health plus separate shared/vDCU components. | Public implementation and exact labels/topology are unavailable. HAMi's `hygon.com/dcunum`, `hygon.com/dcucores`, and `hygon.com/dcumem` are an extension, not evidence for the baseline vendor plugin. | `K100_AI` and `BW200`, both present in first-party [model-zoo material](https://developer.sourcefind.cn/document). | B |
| Qualcomm Cloud AI 100 | The official Cloud AI SDK guide says the packaged `qaic-k8s-device-plugin` can advertise `qaic`, `qaic-std`, `qaic-pro`, or `qaic-ultra` suffixes and monitors health/allocation. | The textual public guide does not expose the fully-qualified namespace, and the plugin is distributed in the SDK rather than as public source. Never prepend a guessed domain. | Standard, Pro, Ultra. | B |
| Kunlunxin / 昆仑芯 | HAMi's integration uses `kunlunxin.com/xpu`; current docs describe P800 topology constraints. The VXPU extension uses `kunlunxin.com/vxpu` and `kunlunxin.com/vxpu-memory`. | The vendor Device Plugin package is obtained from the vendor and is not public. The VXPU names are a HAMi integration, not proven vendor defaults. | P800 (current integration and first-party deployment reports); R480 as deployed-retention. | C |
| Vastai / 瀚博 | HAMi exposes `vastaitech.com/va` in its Vastai integration. | No vendor-owned public Device Plugin/DRA contract was found. | No built-in board-model seed until a first-party source is available. | C |

Useful first-party/provisional sources:

- MetaX:
  [first-party Kubernetes support example](https://developer.metax-tech.com/forum/t/k8san-zhuang-wen-ti/344/)
  and [AI software release](https://developer.metax-tech.com/doc/221)
- Hygon:
  [光合 service catalog](https://developer.sourcefind.cn/servicelist) and
  [developer documentation index](https://developer.sourcefind.cn/document)
- Qualcomm:
  [Kubernetes Device Plugin guide](https://quic.github.io/cloud-ai-sdk-pages/1.20/Getting-Started/Installation/Docker/k8s/index.html)
- Kunlunxin:
  [whole-device integration](https://project-hami.io/docs/userguide/kunlunxin-device/enable-kunlunxin-schedule)
  and [VXPU integration](https://project-hami.io/docs/userguide/kunlunxin-device/enable-kunlunxin-vxpu)

## Product families without a verified public contract

No exact resource key should be emitted for these rows. “Not found” means not
found in public first-party material during this research, not that the vendor
or a customer has no private integration.

| Ecosystem | Current/still-relevant product evidence | Kubernetes contract result | Grade |
| --- | --- | --- | --- |
| SOPHGO / 算能 | Official SDK docs cover BM1684X and BM1688; the company timeline also lists server-class BM1690. | An official user guide mentions a Kubernetes Device Plugin, but no current public source or exact fully-qualified resource name was found. | D |
| Tenstorrent | Official product pages list current Blackhole P100/P150 cards and Wormhole N150/N300 plus Galaxy systems. | No vendor-owned Device Plugin/DRA resource contract was found in the official organization or docs. | D |
| Cerebras | Official material identifies WSE-3 in the CS-3 system. | The platform is scheduled as an appliance/service; no Kubernetes Device Plugin/DRA device resource contract was found. | D |
| SambaNova | Official product material identifies the SN40L RDU/DataScale platform. | No public per-node Kubernetes device resource contract was found. | D |
| Groq | Official material identifies the Groq LPU and GroqRack/GroqCloud systems. | No public Device Plugin/DRA resource contract was found. | D |
| Rebellions | Official product/developer material covers ATOM/ATOM-Max and current REBEL products. | No public Device Plugin/DRA resource contract was found. | D |
| Alibaba Hanguang / 含光 | First-party Alibaba material documents Hanguang 800 as inference silicon. | No current public vendor Device Plugin/DRA resource contract was found. | D |
| Tencent Zixiao / 紫霄 | First-party Tencent material describes the Zixiao inference chip/platform. | No public vendor Device Plugin/DRA resource contract was found. | D |

Product-only sources include
[Tenstorrent cards](https://tenstorrent.com/hardware/cards),
[Cerebras WSE-3/CS-3](https://www.cerebras.ai/press-release/cerebras-announces-third-generation-wafer-scale-engine),
[SambaNova SN40L](https://sambanova.ai/hubfs/SN40L%20RDU%20Paper%2009%2007%2025.pdf),
[Rebellions developer material](https://rebellions.ai/developers/), and
[SOPHGO SDK terms](https://doc.sophgo.com/sdk-docs/v23.09.01-lts-sp5/docs_latest_release/docs/SophonSDK_doc/en/html/2_Term_Definitions.html).
They establish product identity only.

## Required model catalog shape

The first implementation should store a Vendor Profile separately from its
Accelerator Models. A minimal evidence-bearing record is:

```yaml
vendorProfile:
  id: nvidia-device-plugin
  contractKind: device-plugin
  evidence:
    grade: A
    source: https://github.com/NVIDIA/k8s-device-plugin
    revision: "<tag-or-commit>"
    checkedAt: "2026-07-29"
  resources:
    - name: nvidia.com/gpu
      unit: device
  identitySignals:
    - kind: node-label
      key: nvidia.com/gpu.product
  capabilities:
    health: device-plugin
    topology: numa
    sharing: [time-slicing, mps]
    partitioning: [mig]
models:
  - canonicalName: NVIDIA H100
    aliases: [H100, NVIDIA-H100-80GB-HBM3]
    status: K8s-identified
    resourceRef: nvidia.com/gpu
```

Rules for the implementation:

1. `canonicalName` and aliases select identity metadata; they do not create a
   new extended resource.
2. Preserve the exact case of vendor keys. `huawei.com/Ascend910` is not
   interchangeable with a lower-case guess.
3. Store legacy aliases with the source version that used them. Never advertise
   both current and legacy resources unless the scenario asks for a migration
   test.
4. Resource derivation must be a profile-defined, validated function. Free-form
   strings are acceptable only for an explicit custom profile.
5. A capability has three states: `verified`, `not-public`, and `not-applicable`.
   Absence of evidence is not `false`.
6. Managed-service contracts (`google.com/tpu`, AWS Neuron DRA) must be tagged
   provider-specific.
7. DRA attributes and Device Plugin Node resources are different output
   contracts, even for the same Accelerator model.

## Scheduling and test implications

### What an initial scenario can reproduce

- single-node/single-device, single-node/multi-device, and
  multi-node/multi-device integer capacity;
- exact vendor resource keys and verified model labels;
- heterogeneous nodes and model aliases;
- partition/count variants that have a verified resource-name rule;
- coarse health events by reducing/restoring allocatable capacity; and
- DRA objects for separately implemented, source-backed DRA profiles.

### What needs an explicit fidelity level

| Behavior | Node-status simulation | Full Device Plugin / DRA fidelity |
| --- | --- | --- |
| Scheduler filters on extended resource count | Yes | Yes |
| Scheduler filters on verified Node labels | Yes | Yes |
| kubelet registration socket and `ListAndWatch` | No | Device Plugin only |
| Stable device IDs and allocation response | No | Device Plugin or DRA driver |
| CDI/environment/device mounts | No | Device Plugin allocation path |
| NUMA Topology Manager admission | Capacity alone is insufficient | Device Plugin `TopologyInfo` plus kubelet |
| Interconnect-aware multi-device selection | Only with explicit scheduler-visible metadata | Vendor allocator/scheduler extension |
| Hardware diagnostics | No; only a scripted state transition | Vendor library/exporter |
| Claims, classes, slices, per-device attributes | No | DRA |

The CLI can remain responsible only for submitting a Scenario to an existing
cluster. An E2E harness may manage a lightweight cluster and install the
controller needed to reconcile Synthetic Node status, but that lifecycle is
not part of the CLI's normal contract.

## Gaps and follow-up decisions

1. Pin an exact release/commit for every built-in profile when implementation
   starts. This catalog uses commit permalinks for Cambricon, Biren, Iluvatar,
   and Enflame where current source was inspected; moving documentation pages
   still need release pinning.
2. Decide whether phase one is scheduler-surface only or also includes a real
   fake Device Plugin. The latter is required for device IDs, NUMA allocation,
   CDI, and kubelet health behavior.
3. Treat DRA as a separate feature flag and Kubernetes-version compatibility
   matrix. NVIDIA, AMD, and AWS do not expose identical DRA attributes or
   sharing behavior.
4. Ask Moore Threads, MetaX, Hygon, Qualcomm, Kunlunxin, and SOPHGO for a
   publishable versioned contract before promoting their profiles to grade A.
5. Add automated evidence-drift checks for source availability, resource-name
   constants, and release age. A product-page update alone must not mutate a
   scheduling contract.
6. Revisit the D-grade backlog periodically. Promotion requires an exact
   public resource/DRA contract, not merely confirmation that Kubernetes is
   used internally.

## Sources inspected

All sources were checked on 2026-07-29.

- Kubernetes:
  [Device Plugins](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/),
  [Device Plugin API](https://github.com/kubernetes/kubelet/blob/master/pkg/apis/deviceplugin/v1beta1/api.proto)
- NVIDIA:
  [`NVIDIA/k8s-device-plugin`](https://github.com/NVIDIA/k8s-device-plugin),
  [GFD labels](https://github.com/NVIDIA/k8s-device-plugin/blob/main/docs/gpu-feature-discovery/README.md),
  [NVIDIA DRA](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu)
- AMD:
  [Device Plugin docs](https://instinct.docs.amd.com/projects/k8s-device-plugin/en/latest/index.html),
  [resource allocation](https://instinct.docs.amd.com/projects/gpu-operator/en/main/device_plugin/resource-allocation.html),
  [AMD DRA](https://github.com/ROCm/k8s-gpu-dra-driver)
- Intel:
  [Intel Device Plugins](https://github.com/intel/intel-device-plugins-for-kubernetes),
  [Gaudi Device Plugin](https://github.com/HabanaAI/gaudi-device-plugin)
- Huawei:
  [mind-cluster](https://gitee.com/ascend/mind-cluster),
  [Ascend Device Plugin legacy source and migration notice](https://gitee.com/ascend/ascend-device-plugin/blob/master/README.md)
- Cambricon:
  [Device Plugin at inspected revision](https://github.com/Cambricon/cambricon-k8s-device-plugin/tree/cc0f7735f208810df14c4178a888d2ad50613a8d/device-plugin)
- Biren:
  [Device Plugin at inspected revision](https://gitee.com/BirenTechnology/k8s-device-plugin/tree/a9984054f975d3430c61cd1f068691b7137da9a6)
- Iluvatar:
  [Device Plugin at inspected revision](https://gitee.com/deep-spark/ix-device-plugin/tree/2fcca7862b62c002ed4ba45169efb82b7433e5f8)
- Enflame:
  [gcushare at inspected revision](https://github.com/EnflameTechnology/gcushare/tree/ee04a65c2e28b397f49d8aced1be11d761856074)
- Moore Threads:
  [KUAE GPU Operator and Device Plugin](https://docs.mthreads.com/en/cloud-native/cloud-native-doc-online/install_guide/)
- FuriosaAI:
  [Kubernetes Device Plugin](https://developer.furiosa.ai/latest/en/cloud_native_toolkit/kubernetes/device_plugin.html)
- Graphcore:
  [Kubernetes IPU Device Plugin guide](https://docs.graphcore.ai/projects/kubernetes-ipu-device-plugin-user-guide/en/latest/overview.html)
- AWS:
  [Neuron Kubernetes Device Plugin](https://awsdocs-neuron.readthedocs-hosted.com/en/latest/containers/kubernetes-getting-started.html),
  [Neuron DRA](https://awsdocs-neuron.readthedocs-hosted.com/en/latest/deploy/eks/dra.html)
- Google Cloud:
  [TPUs in GKE](https://cloud.google.com/kubernetes-engine/docs/concepts/tpus)
