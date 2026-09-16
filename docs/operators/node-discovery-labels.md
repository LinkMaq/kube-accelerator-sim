# Node discovery labels

Real accelerator clusters publish the properties of every installed card as
Node labels, and workloads select them with `nodeSelector` or affinity — for
example `nvidia.com/gpu.product: NVIDIA-H100-80GB-HBM3`. Kasim can project the
same scheduling-visible label set onto Synthetic Nodes so placement tests
exercise the identical selection path.

This feature is evidence-backed and opt-in. It never claims drivers, device
files, CUDA, or real hardware; it projects the exact label keys and values the
vendor's own component publishes, sourced from the pinned catalog evidence.

## How vendors publish these labels

Every mainstream vendor publishes card properties as Node labels. Vendors
differ in *who writes the label*, not in whether the requirement exists:

| Route | Who writes the Node label | Vendors |
| --- | --- | --- |
| Through Node Feature Discovery | The vendor component writes a feature file under `/etc/kubernetes/node-feature-discovery/features.d/` (NVIDIA GFD and AMD's Accelerator Integration Module), or creates and updates a `NodeFeature` custom resource (NVIDIA GFD). NFD's local source merges the result onto the Node | NVIDIA, AMD, Intel |
| Directly from the device plugin | The vendor device plugin patches the Node with its own API client, using its own service account | Huawei Ascend, Cambricon, Alibaba Cloud PPU, Hygon, MetaX, Moore Threads, Biren, and the remaining vendors |

Node Feature Discovery is therefore not a prerequisite for the labels
themselves — it is one implementation route, and it is absent from many target
clusters, including the reference cluster this catalog was validated against.
AMD uses both routes for different label families: its Node Labeller patches
Nodes directly, while Accelerator Integration Module writes NFD feature files.
Kasim projects the resulting labels rather than modeling the writer, so a
Scenario does not depend on which route the target cluster would have used.

A `xpu=on`, `mlu=on`, `dcu=on`, or `ascend=on` label on a real cluster is a
different thing entirely: those are hand-applied scheduling switches that a
device plugin DaemonSet selects on. They are not discovered device
properties, and Kasim does not emit them.

## Enable discovery labels

Set `discoveryLabels: true` on the Node of a Node Group, or pass
`--discovery-labels` to the `apply demo` shortcut:

```yaml
metadata:
  name: discovery-labels
spec:
  fidelity: scheduling
  nodeGroups:
    - name: workers
      replicas: 2
      node:
        discoveryLabels: true
      acceleratorPools:
        - name: accelerator
          profile:
            id: nvidia
            revision: 2026-09-16.2
            digest: sha256:a04f86407d43998b667d61e6d2bbbc91a3ef004634e776dfc2fe330716a2a479
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          count: 8
          healthy: 8
```

Omitting the field preserves the exact prior canonical Scenario bytes and
digests; existing revisions are unaffected.

## NVIDIA GPU Operator labels

For one eight-card H100 Node Group the two Synthetic Nodes receive the same
36-label `nvidia.com/*` set a GPU Operator-managed node carries (minus
`gfd.timestamp` and `gpu.machine`, see Boundaries). GFD publishes these
through NFD:

| Label | Value | Source |
| --- | --- | --- |
| `nvidia.com/gpu.present` | `true` | Contract-derived, static |
| `nvidia.com/gpu.count` | `8` | Sum of Accelerator Pool totals on the Node |
| `nvidia.com/gpu.product` | `NVIDIA-H100-80GB-HBM3` | Model evidence, GFD-normalized product name |
| `nvidia.com/gpu.family` | `hopper` | Model evidence |
| `nvidia.com/gpu.compute.major` | `9` | Model evidence |
| `nvidia.com/gpu.compute.minor` | `0` | Model evidence |
| `nvidia.com/gpu.memory` | `81920` | Model evidence, MiB envelope |
| `nvidia.com/gpu.mode` | `compute` | Model evidence, PCI-class mode of data-center boards |
| `nvidia.com/mig.capable` | `true` | Model evidence (`false` for L40S) |
| `nvidia.com/mig.strategy` | `single` | Contract-derived, gpu-operator default |
| `nvidia.com/gpu.replicas` | `1` | Contract-derived, no sharing configured |
| `nvidia.com/gpu.sharing-strategy` | `none` | Contract-derived, no sharing configured |
| `nvidia.com/mps.capable` | `false` | Contract-derived, no MPS configured |
| `nvidia.com/vgpu.present` | `false` | Contract-derived, no vGPU manager |
| `nvidia.com/cuda.driver-version.full` | `580.126.16` | Contract-derived, driver evidence shared with DCGM telemetry |
| `nvidia.com/cuda.driver-version.major` | `580` | Contract-derived |
| `nvidia.com/cuda.driver-version.minor` | `126` | Contract-derived |
| `nvidia.com/cuda.driver-version.revision` | `16` | Contract-derived |
| `nvidia.com/cuda.driver.major` | `580` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.driver.minor` | `126` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.driver.rev` | `16` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.runtime-version.full` | `13.3.1` | Contract-derived, latest CUDA GA release |
| `nvidia.com/cuda.runtime-version.major` | `13` | Contract-derived |
| `nvidia.com/cuda.runtime-version.minor` | `3` | Contract-derived |
| `nvidia.com/cuda.runtime.major` | `13` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.runtime.minor` | `3` | Contract-derived, deprecated GFD key |
| `nvidia.com/gpu-driver-upgrade-state` | `upgrade-done` | Contract-derived, operator upgrade lifecycle |
| `nvidia.com/gpu.deploy.container-toolkit` | `true` | Contract-derived, operator component state |
| `nvidia.com/gpu.deploy.dcgm` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.dcgm-exporter` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.device-plugin` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.driver` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.gpu-feature-discovery` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.node-status-exporter` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.nvsm` | `` (empty) | Contract-derived, NVSM not deployed |
| `nvidia.com/gpu.deploy.operator-validator` | `true` | Contract-derived |

## AMD Instinct labels

AMD's Kubernetes Node Labeller patches every AMD GPU node with `amd.com/*`
properties. Kasim projects the keys and the values that the pinned
[k8s-device-plugin Node Labeller documentation](https://github.com/ROCm/k8s-device-plugin/blob/2af2fdbf29472b9e9c84087118c87e1bf2f804cd/cmd/k8s-node-labeller/README.md)
publishes for the selected model:

| Label | Value per model | Source |
| --- | --- | --- |
| `amd.com/gpu.cu-count` | `304` (MI300X) | Node Labeller `-cu-count` (grade A); per-model value from AMD Instinct product documentation |
| `amd.com/gpu.device-id` | `74a1` (MI300X) | Node Labeller `-device-id` (grade A) |
| `amd.com/gpu.product-name` | `AMD_Instinct_MI300X_OAM` (MI300X) | Node Labeller `-product-name` (grade A) |
| `amd.com/gpu.simd-count` | `1216` (MI300X) | Node Labeller `-simd-count` (grade A) |
| `amd.com/gpu.family` | declared, no evidenced value | Node Labeller `-family` (grade A); the documented acronym table does not cover CDNA parts |
| `amd.com/gpu.vram` | declared, no evidenced value | Node Labeller `-vram` (grade A); only a legacy `16G`-style sample is published |

The labeller also publishes `driver-version`, `driver-src-version`, and
`firmware`; the catalog does not declare those keys, so Kasim emits no
`amd.com/*` label for them rather than guessing a value.

## Intel Data Center GPU labels

Intel publishes data-center GPU labels through NFD rules. The pinned
[`gpu_plugin` label documentation](https://github.com/intel/intel-device-plugins-for-kubernetes/blob/6460392f95275dd68774aeef3c39f14538ddb3d9/cmd/gpu_plugin/labels.md)
states that "GPU labels originate from two main sources: NFD rules and GPU
plugin (& NFD hook)". Kasim projects the rule-derived set:

| Label | Value per model | Source |
| --- | --- | --- |
| `gpu.intel.com/device.count` | `8` | Sum of Accelerator Pool totals on the Node, matching the documented rule |
| `gpu.intel.com/family` | `Max_Series` / `Flex_Series` | NFD rule (grade A); sample values published per platform |
| `gpu.intel.com/product` | `Max_1550` / `Flex_170` | NFD rule (grade A); sample values published per platform |

Intel documents four covered platforms — Flex 140, Flex 170, Max 1100, Max
1550 — but publishes sample `family` and `product` values for Flex 170 and Max
1550 only. The other two models keep the declared keys with empty values.

## Huawei Ascend labels

The same mechanism covers the Huawei Ascend device plugin. Real clusters run
Ascend Device Plugin from MindCluster, which stamps every NPU Node with a chip
identity label pair; Kasim projects the identical keys from the pinned catalog
evidence:

| Label | Value per model | Source |
| --- | --- | --- |
| `accelerator` | `huawei-Ascend310` / `huawei-Ascend310P` / `huawei-Ascend910` | MindCluster `AcceleratorLabelKey` (grade A); the official Node label usage table names Ascend Device Plugin as the writing component |
| `node.kubernetes.io/npu.chip.name` | `310` / `310P` / `910A` / `910B` (Atlas A2) | MindCluster `ChipNameLabel` (grade A); value reported at chip-family granularity (grade B) |
| `servertype` | `Ascend310-4` / `Ascend310P-8` / `Ascend910-32` / `Ascend910B-20` | MindCluster `ServerTypeLabelKey` (grade A); AI-core counts from Huawei product documentation (grade B) |

Evidence is anchored to the MindCluster
[v26.1.0 release](https://gitcode.com/ascend/mind-cluster/blob/v26.1.0/component/ascend-device-plugin/pkg/common/constants.go)
where the label constants are byte-identical to the audited master snapshot.

`huawei-atlas-a3` emits no vendor labels: its chip name and AI-core count are
not publicly evidenced, and the catalog never fabricates values. `huawei-atlas-a2`
publishes no `accelerator` value: the official table lists `huawei-Ascend910`
for that node type, which its chip family already carries, and the catalog does
not duplicate a value to fill a key.

Real device plugins additionally emit `accelerator-type=card-910b-infer` and
`infer-card-type=card-300i-duo` only for specific inference boards, plus
`mind-cluster/npu-chip-memory`; those depend on board identity rather than the
chip and are deliberately not emitted (see Boundaries).

## Alibaba Cloud PPU labels

Alibaba Cloud ACK's PPU device plugin stamps `aliyun.accelerator/*` labels on
PPU nodes. Kasim projects the published keys, combining node-level labels with
values the PPU device plugin resolves at runtime:

| Label | Value | Source |
| --- | --- | --- |
| `aliyun.accelerator/xpu_type` | `ppu` | ACK Lingjun node pool guide (grade A), contract-derived static value |
| `aliyun.accelerator/ppu_count` | `8` | Sum of Accelerator Pool totals on the Node |
| `aliyun.accelerator/ppu_name` | `PPU-ZW810E` | ACK guide sample value (grade A) |
| `aliyun.accelerator/ppu_mem` | `98304MiB` | ACK guide sample value (grade A) |

`alibaba-ppu-m890p` emits no vendor labels: the guide publishes sample values
for PPU-ZW810E only.

## Keys without an evidenced value

Vendor label keys are declared at the contract level and values at the model
level. A declared key with no model-specific evidence is still projected, but
with an empty value — for example `amd.com/gpu.family` on MI300X, `accelerator`
on Atlas A2, and every `hygon.com/dcu*` key. This is deliberate:

- The key keeps its collision reservation, so a Scenario that supplies the same
  key manually still fails compilation instead of silently disagreeing with the
  vendor.
- An empty value never satisfies a positive `nodeSelector` match, so a workload
  that requires a real vendor value is not falsely scheduled. The alternative —
  substituting the model ID or the resource name — would make a placement test
  pass for a label no real node carries.

Run `kasim profile show <id> -o json` to see which models carry evidenced
values. Keys whose vendor publishes no node label at all are recorded as open
research items rather than filled in.

## Failure behavior

- A model without catalog node-label evidence emits no vendor label values; the
  Scenario still applies, and the discovery request simply projects nothing for
  that pool. Check `kasim profile show` for the model's evidence.
- A Scenario-provided Node label that collides with a vendor discovery label
  fails compilation with `reserved by vendor node discovery`. Identical
  repeated values across pools of the same model remain legal; conflicting
  values across pools fail compilation.
- The values ride the `extended-resources` fidelity surface: reconciliation
  and `kasim status` verify the labels on every Synthetic Node exactly like
  capacity, so a drifted label fails the Snapshot instead of being silently
  reapplied.

## Boundaries

Kasim deliberately does not emit `nvidia.com/gfd.timestamp` (breaks Snapshot
determinism) or `nvidia.com/gpu.machine` (host-specific, no simulated host
exists). On the Ascend side it does not emit `accelerator-type`,
`infer-card-type`, or `mind-cluster/npu-chip-memory`, because those depend on
the installed board or a chip sub-model rather than the selected simulated
model; `npu.chip.name` is emitted at chip-family granularity (`910B`, not
`910B1`–`910B4`), and sub-model precision would need separate catalog models.
The CUDA runtime version is pinned per catalog revision to the latest CUDA GA
release rather than detected from a node toolkit install; upgrade the catalog
to move it.

Kasim projects vendor label *values*; it does not install, run, or require
Node Feature Discovery, a GPU Operator, or a vendor device plugin on the target
cluster, and it does not project the `feature.node.kubernetes.io/*` namespace
that NFD itself owns. Cluster-specific NFD labels such as
`feature.node.kubernetes.io/pci-*.present` are only observable on clusters that
actually run NFD with the matching rules, so Kasim does not synthesize them.
The labels describe simulated scheduling inventory; they never prove drivers,
device files, or accelerated computation.
