# Accelerator vendor signals and model seeds

- Status: research snapshot
- Cut-off date: 2026-07-31
- Target: Kubernetes 1.30+ simulation scenarios
- Scope: Kubernetes Device Plugin extended resources, provider-managed equivalents, and model identities suitable for `kube-accelerator-sim` examples

## Decision

The default catalog should contain only signals whose exact spelling and scheduling
meaning can be reproduced from a vendor-maintained Device Plugin, vendor
documentation, or a cloud-provider document:

| Vendor / platform | Default resource signals | Signal class | Recommended model seeds | Admission |
| --- | --- | --- | --- | --- |
| NVIDIA | `nvidia.com/gpu`; selected `nvidia.com/mig-*` profiles | physical/device instance; hardware partition | A100 80GB, H100, H200, L40S, B200; A800/H800/H20 aliases | default |
| AMD Instinct | `amd.com/gpu`; `amd.com/cpx_nps4`, `amd.com/spx_nps1`, `amd.com/cpx_nps1` | physical or partition under single naming; partition under mixed naming | MI210, MI250X, MI300A, MI300X, MI325X, MI350X, MI355X | default |
| Intel Data Center GPU | `gpu.intel.com/i915`, `gpu.intel.com/xe` | driver-visible GPU instance | Flex 140/170, Max 1100/1550 | default |
| Huawei Ascend | `huawei.com/Ascend310`, `huawei.com/Ascend310P`, `huawei.com/Ascend910`; optional `huawei.com/npu-core` | physical device family; virtual NPU core | Ascend 310/310P/910; 910B/A2 and 910A3/A3 as model aliases behind the `Ascend910` resource | default; `npu-core` opt-in |
| Cambricon | `cambricon.com/mlu`, `cambricon.com/mlu370`; selected `.share` and `.mim-*` variants | physical/model device; shared token; hardware partition | MLU370, MLU590; MLU270/290 compatibility | default |
| Enflame | `enflame.com/gcu`, `enflame.com/shared-gcu`, `enflame.com/drs-gcu` | physical device; shared token; dynamically split device | S60, S60G | default |
| Iluvatar CoreX | `iluvatar.com/gpu` | board, chip, or time-slice token depending plugin flags | BI-V150, BI-V150S | default |
| Moore Threads | `mthreads.com/gpu`; `mthreads.com/sgpu-core` plus `mthreads.com/sgpu-memory` | physical GPU; split compute and memory | MTT S80, S2000, S3000, S4000 | default |
| Biren | `birentech.com/gpu`; `birentech.com/1-4-gpu`, `birentech.com/1-2-gpu` | physical device; pre-created SVI partition | BR100 series | default |
| Intel Gaudi | `habana.ai/gaudi` | physical Gaudi device | Gaudi2, Gaudi3 | default |
| AWS Neuron | `aws.amazon.com/neuron`, `aws.amazon.com/neuroncore` | whole Neuron device; Neuron core | Inf2/Inferentia2, Trn2/Trainium2, Trn3/Trainium3 | provider-specific |
| Google Cloud TPU | `google.com/tpu` | TPU chips on a GKE TPU slice node | TPU v5e, v5p, Trillium v6e, Ironwood TPU7x | provider-specific |
| Hygon DCU | `hygon.com/dcu`; selected `hygon.com/dcu-share-*` and `hygon.com/dcu-mig-*` profiles | physical DCU; pre-created vDCU; MIG partition | K100-AI, BW1000; Z100L/BW1100 as newer model identities | default |
| MetaX | `metax-tech.com/gpu`, `metax-tech.com/vfio-gpu`, `metax-tech.com/sgpu` | physical GPU; VFIO-bound passthrough GPU; software-split GPU | C500/C500X/C550/C600; N260 inference | default |
| Kunlunxin | **unconfirmed** | no exact first-party public resource contract found | P800, R480-X8 | model-only |

This produces three catalog gates:

1. `verified-default`: exact resource name and its unit are established by a
   first-party implementation or documentation.
2. `provider-specific`: exact and useful, but valid only in the named managed
   environment.
3. `model-only`: never loaded as a runnable profile by the default catalog; an
   unconfirmed resource name must not be supplied by the project.

Implementation note: this admission result excludes a vendor-default
Kunlunxin profile. The product separately retains the community contract as
the integration-specific provisional profile `kunlunxin-hami`, requires
explicit provisional acceptance, reports `providerScope: hami-integration`,
and does not present its resource names as first-party vendor evidence. This
follows [ADR 0002](../adr/0002-vendor-profile-and-model-contract.md) without
changing the first-party finding above.

## Evidence method

Only these evidence classes were accepted:

- vendor-maintained Device Plugin or operator source;
- vendor documentation or official product pages;
- cloud-provider documentation for provider-managed accelerators.

Community schedulers are useful compatibility references but do not prove a
vendor baseline. Community-only Hygon and Kunlunxin signals are not promoted
unless the same spelling and semantics are independently reproduced in the
first-party sources cited below.

Kubernetes defines a Device Plugin resource as a vendor-domain extended
resource registered with kubelet. The scheduler sees an integer quantity, not
the underlying model, memory, topology, or sharing mechanism. See the official
[Kubernetes Device Plugins documentation](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/device-plugins/)
and [Device Plugin API](https://github.com/kubernetes/kubelet/blob/master/pkg/apis/deviceplugin/v1beta1/api.proto).

For this project, “Kubernetes 1.30+” means that simulated Node
capacity/allocatable fields, labels, taints, and Pod resource requests conform
to the Kubernetes 1.30+ API contract. It does not assert that every cited
vendor Operator or driver is certified on every 1.30+ minor release. Runtime
support windows remain vendor-specific and are outside a CLI that only submits
simulation scenarios to an existing cluster.

This report therefore uses four distinct signal classes:

- **physical/device instance**: one schedulable device as enumerated by the
  plugin. It can still be a chip instead of a board if the plugin says so.
- **partition**: a hardware, virtual, core, or memory partition.
- **shared token**: an over-advertised access token; its count must not be
  interpreted as installed-card count.
- **capability label**: Node metadata used for selection or discovery. It is not
  legal under `resources.limits`.

## Vendor findings

### NVIDIA

**Verified resources**

- `nvidia.com/gpu` is the base resource registered by the official
  [NVIDIA Kubernetes Device Plugin](https://github.com/NVIDIA/k8s-device-plugin).
- With mixed MIG strategy, the plugin exposes resources of the form
  `nvidia.com/mig-<slice_count>g.<memory_size>gb`. The official examples include
  A100 40GB profiles such as `nvidia.com/mig-1g.5gb`,
  `nvidia.com/mig-2g.10gb`, `nvidia.com/mig-3g.20gb`, and
  `nvidia.com/mig-7g.40gb`, and their A100 80GB counterparts.
- Time-slicing and MPS can replicate advertised resources. With
  `renameByDefault`, the resource gains a `.shared` suffix. These quantities
  are shared-access tokens, not extra physical GPUs. The distinction is
  documented in the plugin's
  [shared-access section](https://github.com/NVIDIA/k8s-device-plugin/blob/main/README.md#shared-access-to-gpus).

**Labels, not resources**

GPU Feature Discovery publishes model and capability labels including
`nvidia.com/gpu.product`, `nvidia.com/gpu.count`, `nvidia.com/gpu.memory`,
`nvidia.com/mig.capable`, and sharing-strategy metadata. These belong in Node
labels, not `resources.limits`; see the official
[GFD documentation](https://github.com/NVIDIA/k8s-device-plugin/blob/main/docs/gpu-feature-discovery/README.md).

**Model set**

The current [GPU Operator platform support
matrix](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/platform-support.html)
supports the major data-center families needed for examples. Seed A100 80GB,
H100, H200, L40S, and B200; retain A800, H800, and H20 as deployed/regional
aliases. A model alias must not create a new extended-resource name.

### AMD Instinct

**Verified resources**

The official [AMD Device Plugin
documentation](https://instinct.docs.amd.com/projects/gpu-operator/en/main/device_plugin/device-plugin.html)
defines two naming modes:

- `single` advertises `amd.com/gpu`. Depending on partition configuration, its
  integer may represent a whole GPU or an enumerated partition.
- `mixed` exposes partition-type resources. The official examples include
  `amd.com/cpx_nps4`, `amd.com/spx_nps1`, and `amd.com/cpx_nps1`.

Consequently, `amd.com/gpu: 8` is not always proof of eight physical boards.
Each AMD example must record its naming mode and partition state.

**Labels, not resources**

The official
[configuration guide](https://instinct.docs.amd.com/projects/k8s-device-plugin/en/latest/user-guide/configuration.html)
documents labels such as `amd.com/gpu.vram`, `amd.com/gpu.cu-count`,
`amd.com/gpu.device-id`, `amd.com/gpu.family`, and
`amd.com/gpu.product-name`, plus partition-support labels. They describe
capability or identity and must not be emitted as extended resources.

**Model set**

Use MI210, MI250X, MI300A, MI300X, MI325X, MI350X, and MI355X. The model list is
supported by AMD's current
[hardware support matrix](https://instinct.docs.amd.com/projects/gpu-cluster-networking/en/latest/reference/hardware-support.html)
and [MI300 product page](https://www.amd.com/en/products/accelerators/instinct/mi300.html).
MI300X is the primary full-device and partition example; MI210 is useful as a
non-partition-capable contrast.

### Intel Data Center GPU

The official
[Intel GPU Device Plugin](https://github.com/intel/intel-device-plugins-for-kubernetes/blob/6460392f95275dd68774aeef3c39f14538ddb3d9/cmd/gpu_plugin/README.md)
registers:

- `gpu.intel.com/i915` for GPU instances served by the legacy i915 kernel-mode
  driver;
- `gpu.intel.com/xe` for GPU instances served by the xe kernel-mode driver.

The resource suffix identifies the driver path, not Flex or Max product
identity. The plugin's `shared-dev-num` option can replicate instances, and
preconfigured SR-IOV VFs can be discovered; neither justifies a new
model-named resource.

Seed Flex 140/170 and Max 1100/1550, supported by Intel's
[Flex Series product page](https://www.intel.com/content/www/us/en/products/details/discrete-gpus/data-center-gpu/flex-series.html)
and [Max Series product brief](https://www.intel.com/content/dam/www/central-libraries/us/en/documents/2023-01/data-center-gpu-max-series-product-brief.pdf).
The selected resource must follow the simulated driver mode.

### Huawei Ascend

The official Ascend Device Plugin source defines the `huawei.com/` prefix and
the exact physical-family resources
`huawei.com/Ascend310`, `huawei.com/Ascend310P`, and
`huawei.com/Ascend910`; it also defines `npu-core` as the virtual-device
resource. See the pinned
[constants](https://gitee.com/ascend/ascend-device-plugin/blob/4cd80a88d37c4b1dfcf17fd1b4f7c8f784e66e01/pkg/common/constants.go)
and [virtual-device parsing](https://gitee.com/ascend/ascend-device-plugin/blob/37362c31c7017a2f297b5a6887c1b5e6618775ab/pkg/common/device.go).

`huawei.com/npu-core` is a vNPU/core quantity and must be opt-in. An official
project issue also records that scheduler plugins registered only the three
physical-family resources while a vNPU workload requested `npu-core`; this is
useful evidence that the two contracts are not interchangeable:
[Ascend/mind-cluster issue ICE3JI](https://gitee.com/ascend/mind-cluster/issues/ICE3JI).

Use Ascend 310, 310P, and 910 as direct model/resource examples. Modern
910B/Atlas A2 and 910A3/Atlas A3 identities may be model metadata behind
`huawei.com/Ascend910`, but the project must not invent
`huawei.com/Ascend910B`, `huawei.com/Ascend910C`, or
`huawei.com/Ascend910A3`.

### Hygon DCU

The vendor-operated [光合 developer
site](https://developer.sourcefind.cn/) identifies K100-AI and BW1000 products,
and its current documentation index additionally exposes Z100L and BW1100 model
identities. The [service catalog](https://developer.sourcefind.cn/servicelist)
describes separate Device Plugin, shared-device, and vDCU components.

The official DCU Kubernetes deployment document establishes exact scheduling
signals:

- The
  [standard-mode chapter](https://developer.sourcefind.cn/document/87ee5c5b-c10d-11f0-b077-0242ac150003?id=44dc5a98-c8d9-11f0-8db6-0242ac150003)
  states that `hygon.com/dcu` is the whole-card resource and shows it in Node
  capacity, allocatable capacity, and a Pod limit.
- The same chapter shows pre-created vDCU resources including
  `hygon.com/dcu-share-30c-16g`, `hygon.com/dcu-share-36c-16g`, and
  `hygon.com/dcu-share-36c-5g`. The general shape is
  `hygon.com/dcu-share-<compute-units>c-<memory>g`, but the simulator should
  ship only profiles present in first-party evidence.
- The
  [MIG-mode chapter](https://developer.sourcefind.cn/document/87ee5c5b-c10d-11f0-b077-0242ac150003?id=4cca45d7-c8d9-11f0-8db6-0242ac150003)
  documents `hygon.com/dcu-mig-2g-15gb` and
  `hygon.com/dcu-mig-4g-31gb` as MIG instances.

The official label chapter uses `hygon.com/dcu=true`,
`hygon.com/dcu.name=K100_AI`, `hygon.com/dcu.cu-count`, and
`hygon.com/dcu.vram` as Node labels. Although the base string
`hygon.com/dcu` is shared with the extended resource, the label and Node
capacity are different Kubernetes fields and must remain separate.

The standard-mode capacity example also contains `hygon.com/dcunum: 0`, but
does not define its scheduling unit. Exclude that auxiliary signal, along with
unverified `hygon.com/dcucores` and `hygon.com/dcumem`, from the default
catalog until first-party semantics are explicit.

### MetaX

The versioned official
[MetaX Kubernetes component guide](https://developer.metax-tech.com/api/client/document/preview/1269/k8s/03_component.html)
defines the scheduler-visible contract:

- `metax-tech.com/gpu` is the normal GPU resource requested by workloads;
- `metax-tech.com/vfio-gpu` is a GPU bound to `vfio-pci` and registered for
  KubeVirt passthrough;
- `metax-tech.com/sgpu` is registered when gpu-device runs in sGPU mode; the
  guide describes sGPU as a software split with up to 16 virtual instances.

The same guide documents `metax-tech.com/gpu.topology.scores` and
`metax-tech.com/gpu.topology.losses` as Node labels. They are topology
metadata, not extended resources. It also states that gpu-device discovers
devices, reports health, removes failed devices from allocatable capacity, and
performs topology-aware allocation. These exact contracts are suitable for the
default catalog; no other MetaX partition or sharing names should be inferred.

The official
[曦云 GPU user guide](https://developer.metax-tech.com/api/client/document/file/138/preview/?file_type=pdf)
and current
[C-series/N260 release material](https://developer.metax-tech.com/api/client/document/file/1212/preview/?file_type=pdf)
establish C500/C500X/C550 and N260, while the official
[C600 release notes](https://developer.metax-tech.com/doc/1213) establish C600.
Seed those products as model metadata, without model-specific resource names.

### Kunlunxin

The official [R480-X8 product
sheet](https://www.kunlunxin.com/wp-content/uploads/2023/02/r480..pdf) and
official Kunlunxin/Baidu material establish R480 and P800 product identities.
However, no exact Kubernetes extended resource could be confirmed from a
public vendor Device Plugin or Kubernetes guide.

Do not promote community names such as `kunlunxin.com/xpu`,
`kunlunxin.com/vxpu`, or `kunlunxin.com/vxpu-memory` into the default catalog.
Retain P800 and R480-X8 as model-only entries pending first-party Kubernetes
evidence.

### Cambricon

The official Device Plugin at a pinned revision documents and implements:

- `cambricon.com/mlu` for the base device;
- `cambricon.com/mlu370` when device-type naming is enabled;
- `cambricon.com/mlu370.share` for env-share mode;
- `cambricon.com/mlu370.mim-2m.8gb` as a concrete MIM hardware partition.

See the official
[Device Plugin README](https://github.com/Cambricon/cambricon-k8s-device-plugin/blob/cc0f7735f208810df14c4178a888d2ad50613a8d/device-plugin/README.md)
and [resource-name
construction](https://github.com/Cambricon/cambricon-k8s-device-plugin/blob/cc0f7735f208810df14c4178a888d2ad50613a8d/device-plugin/pkg/mlu/server.go#L543-L569).

Use MLU370 as the model-specific and MIM example. Use MLU590 with the generic
`cambricon.com/mlu` unless a public source confirms an exact MLU590 resource
suffix. MLU270/290 can remain compatibility models.

### Enflame

The official
[`gcushare` configuration](https://github.com/EnflameTechnology/gcushare/blob/ee04a65c2e28b397f49d8aced1be11d761856074/config/topscloud.json)
uses `enflame.com/gcu`. Its pinned
[constants](https://github.com/EnflameTechnology/gcushare/blob/ee04a65c2e28b397f49d8aced1be11d761856074/gcushare-device-plugin/pkg/consts/const.go)
define:

- `enflame.com/shared-gcu` for shared access;
- `enflame.com/drs-gcu` for dynamically split GCU capacity;
- `enflame.com/gcu-count` as scheduler/accounting metadata rather than the
  baseline physical-device resource.

Use S60 as the primary model. The official source's device tests also carry the
S60G product model, so S60G is suitable as a compatibility seed. Shared and DRS
examples must be named explicitly and must not claim physical-card counts.

### Iluvatar CoreX / 天数智芯

The current official
[`ix-device-plugin`](https://gitee.com/deep-spark/ix-device-plugin/tree/2fcca7862b62c002ed4ba45169efb82b7433e5f8)
defaults to `iluvatar.com/gpu`. Its
[README](https://gitee.com/deep-spark/ix-device-plugin/blob/2fcca7862b62c002ed4ba45169efb82b7433e5f8/README.md)
states that split-board mode can expose the two chips on a BI-V150 board as two
schedulable units. Time-slicing also multiplies the same resource name.

Thus the key stays `iluvatar.com/gpu`, but its unit can be board, chip, or
time-slice token. Each example must record the mode. Use BI-V150 and BI-V150S;
do not use the older `iluvatar.ai/gpu` spelling as the new default.

### Moore Threads

The official [KUAE installation
guide](https://docs.mthreads.com/en/cloud-native/cloud-native-doc-online/install_guide/)
shows:

- `mthreads.com/gpu` as physical GPU capacity;
- `mthreads.com/sgpu-core` and `mthreads.com/sgpu-memory` together for sGPU
  allocation;
- `mthreads.com/gpu=enable` as a Node label/deployment gate, not an extended
  resource.

The same guide contains current S80 and S4000 device output, while the official
[cloud-native product white
paper](https://docs.mthreads.com/cloud-native/cloud-native-doc-online/history_version/v1.9.0/white_paper/)
documents S2000 and S3000. Seed S80, S2000, S3000, and S4000. A split example
must request both core and memory resources; neither quantity alone represents
a full GPU.

### Biren

The official
[Biren Device Plugin](https://gitee.com/BirenTechnology/k8s-device-plugin/tree/a9984054f975d3430c61cd1f068691b7137da9a6)
exposes `birentech.com/gpu` for the base device and
`birentech.com/1-4-gpu` / `birentech.com/1-2-gpu` for pre-created SVI
partitions. The plugin discovers existing partition state; these exact
fractions do not authorize arbitrary `birentech.com/<fraction>-gpu` names.

Use the vendor-documented [BR100
series](https://www.birentech.com/csr-article/gblwq16edn07xrb9pjky963s/) as the
model family, without creating a per-model resource.

### Intel Gaudi

Intel's current [Gaudi Kubernetes
guide](https://docs.habana.ai/en/latest/Quick_Start_Guides/Kubernetes_Quick_Start.html)
requests `habana.ai/gaudi` in both single-device and eight-device jobs. The
official
[Device Plugin](https://github.com/HabanaAI/gaudi-device-plugin)
registers discovered Gaudi devices and tracks health.

Use Gaudi2 and Gaudi3 behind the same resource key. The
[Intel Gaudi product page](https://www.intel.com/content/www/us/en/products/details/processors/ai-accelerators/gaudi.html)
establishes both generations and current Gaudi3 products. No public
model-specific, sharing, or partition resource should be inferred.

### AWS Inferentia and Trainium

The official [Neuron Kubernetes Device Plugin
documentation](https://awsdocs-neuron.readthedocs-hosted.com/en/latest/deploy/infrastructure/plugins.html)
defines:

- `aws.amazon.com/neuron` for whole Neuron devices;
- `aws.amazon.com/neuroncore` for Neuron cores.

These are exclusive allocations with different units. The resource key does
not encode Inferentia or Trainium generation. Use Inf2/Inferentia2 and
Trn2/Trainium2 as primary examples, plus Trn3/Trainium3 when testing current
catalog coverage. Mark all of them `providerScope: aws-ec2-eks`; they do not
prove a portable on-premises resource contract.

### Google Cloud TPU

GKE exposes `google.com/tpu`; official documentation states that this quantity
represents TPU chips on TPU slice nodes and that workloads commonly request all
chips on a node. Model and topology are Node labels:

- `cloud.google.com/gke-tpu-accelerator`;
- `cloud.google.com/gke-tpu-topology`.

See [TPUs in GKE](https://cloud.google.com/kubernetes-engine/docs/concepts/tpus)
and [planning TPU
configurations](https://cloud.google.com/kubernetes-engine/docs/concepts/plan-tpus).
Use v5e, v5p, Trillium v6e, and Ironwood TPU7x examples with
`providerScope: gke`; do not describe `google.com/tpu` as an on-premises Device
Plugin contract.

## Recommended example matrix

These examples exercise distinct scheduler-visible semantics rather than
duplicating every marketing SKU:

| Example ID | Nodes × quantity | Resource | Model metadata | Purpose |
| --- | --- | --- | --- | --- |
| `nvidia-h100-1x1` | 1 × 1 | `nvidia.com/gpu` | H100 | single-node, single-device baseline |
| `nvidia-h100-2x8` | 2 × 8 | `nvidia.com/gpu` | H100 | multi-node, multi-device baseline |
| `nvidia-a100-80gb-mig` | 1 × selected profiles | `nvidia.com/mig-1g.10gb`, `nvidia.com/mig-2g.20gb`, `nvidia.com/mig-7g.80gb` | A100 80GB | hardware-partition scheduling |
| `nvidia-l40s-timeslice` | 1 × replicas | `nvidia.com/gpu.shared` | L40S | shared tokens, not physical count |
| `amd-mi300x-single` | 1 × 8 | `amd.com/gpu` | MI300X | AMD single naming |
| `amd-mi300x-mixed` | 1 × partitions | `amd.com/cpx_nps4` | MI300X | partition-aware mixed naming |
| `amd-mi210-whole` | 1 × 4 | `amd.com/gpu` | MI210 | non-partition contrast |
| `intel-flex-xe` | 1 × 2 | `gpu.intel.com/xe` | Flex 170 | driver-keyed resource |
| `ascend-910-2x8` | 2 × 8 | `huawei.com/Ascend910` | Ascend 910B / Atlas A2 | modern model alias, verified family key |
| `ascend-310p-vnpu` | 1 × cores | `huawei.com/npu-core` | Ascend 310P | opt-in virtual/core semantics |
| `cambricon-mlu370` | 1 × 8 | `cambricon.com/mlu370` | MLU370 | model-keyed physical resource |
| `cambricon-mlu370-mim` | 1 × partitions | `cambricon.com/mlu370.mim-2m.8gb` | MLU370 | verified MIM profile |
| `enflame-s60` | 1 × 8 | `enflame.com/gcu` | S60 | physical GCU |
| `enflame-s60-shared` | 1 × tokens | `enflame.com/shared-gcu` | S60 | shared GCU tokens |
| `iluvatar-bi-v150-splitboard` | 1 board × 2 chips | `iluvatar.com/gpu` | BI-V150 | resource unit changes under split-board |
| `mthreads-s3000` | 1 × 8 | `mthreads.com/gpu` | MTT S3000 | physical GPU |
| `mthreads-s80-sgpu` | 1 × core/memory pairs | `mthreads.com/sgpu-core` + `mthreads.com/sgpu-memory` | MTT S80 | split compute and memory |
| `metax-c500` | 1 × 8 | `metax-tech.com/gpu` | C500 | physical MetaX GPU |
| `metax-c500-sgpu` | 1 × virtual instances | `metax-tech.com/sgpu` | C500 | software-split GPU |
| `hygon-k100-ai` | 1 × 8 | `hygon.com/dcu` | K100-AI | whole DCU |
| `hygon-k100-ai-vdcu` | 1 × partitions | `hygon.com/dcu-share-30c-16g` | K100-AI | pre-created vDCU |
| `hygon-dcu-mig` | 1 × partitions | `hygon.com/dcu-mig-2g-15gb` | compatible DCU model | MIG instance without inferred model |
| `biren-br100-svi` | 1 × partitions | `birentech.com/1-4-gpu` | BR100 | pre-created hardware partition |
| `gaudi3-2x8` | 2 × 8 | `habana.ai/gaudi` | Gaudi3 | multi-node accelerator baseline |
| `aws-inf2-device` | provider topology | `aws.amazon.com/neuron` | Inferentia2 | whole Neuron device |
| `aws-trn2-core` | provider topology | `aws.amazon.com/neuroncore` | Trainium2 | core allocation |
| `gke-tpu-v5e` | slice topology | `google.com/tpu` | TPU v5e | managed-provider chip count |

Kunlunxin should have model documentation/catalog fixtures, but no default
runnable example because an exact first-party public resource name is still
missing.

## Catalog rules for implementation

Each catalog entry should preserve evidence and semantics explicitly:

```yaml
vendor: nvidia
model: H100
resources:
  nvidia.com/gpu:
    unit: device
    evidenceState: verified-default
    source: https://github.com/NVIDIA/k8s-device-plugin
labels:
  nvidia.com/gpu.product: NVIDIA-H100-80GB-HBM3
```

Required fields for every resource signal:

- exact resource key;
- `unit`: `device`, `chip`, `partition`, `core`, `memory`, or `shared-token`;
- naming/mode preconditions;
- `evidenceState`;
- first-party source URL and, for source evidence, preferably a pinned revision;
- provider scope when the contract is not portable.

Required invariants:

1. Model identity and extended-resource identity are independent unless the
   vendor explicitly embeds the model in the key.
2. Shared replicas never increase the recorded physical device count.
3. A label is never copied into `status.capacity` or `resources.limits`.
4. A community scheduler resource is not a vendor baseline.
5. A generic pattern does not authorize arbitrary generated names; examples
   ship only exact profiles established by first-party evidence.
6. `model-only` entries are not reachable through the normal default catalog
   command.

## Remaining evidence gaps

The following evidence would change admission status:

- Hygon: a public compatibility table mapping MIG-capable DCU models to
  supported profiles; whole-card and example partition resources are already
  confirmed.
- MetaX: a stable public Node model-label contract mapping C500/C550/C600
  identities to plugin output; resource signals themselves are confirmed.
- Kunlunxin: a public vendor Kubernetes Device Plugin or workload guide stating
  exact whole-device and virtual-device resources.
- Huawei: a current public matrix mapping 910B/910A3 hardware to registered
  resource names across plugin versions.
- Newer Chinese accelerator SKUs: product identity alone is insufficient;
  each needs a separate Kubernetes signal review before entering the default
  catalog.
