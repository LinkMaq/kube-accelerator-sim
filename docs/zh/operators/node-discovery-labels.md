# 节点发现标签

真实加速卡集群会把每张已安装卡的属性以 Node 标签的形式发布出来，工作负载通过 `nodeSelector` 或亲和性选择它们，例如 `nvidia.com/gpu.product: NVIDIA-H100-80GB-HBM3`。Kasim 可以把同一套调度可见标签投射到模拟节点上，使放置类测试走过完全一致的选择路径。

该特性证据驱动且默认关闭。它不声明驱动、设备文件、CUDA 或真实硬件，只投射厂商自有组件实际发布的标签键值，证据来源为锚定的目录数据。

## 各厂商如何发布这些标签

所有主流厂商都会把加速卡属性发布为 Node 标签。厂商之间的差异在于**由谁写这个标签**，而不是这个需求是否存在：

| 路线 | 由谁写 Node 标签 | 厂商 |
| --- | --- | --- |
| 经由 Node Feature Discovery | 厂商组件在 `/etc/kubernetes/node-feature-discovery/features.d/` 下写 features 文件（NVIDIA GFD 与 AMD 的 Accelerator Integration Module），或创建并更新 `NodeFeature` 自定义资源（NVIDIA GFD）；NFD 的 local source 把结果合并到 Node 上 | NVIDIA、AMD、Intel |
| 由设备插件直接写入 | 厂商设备插件用自己的 service account 与 API 客户端直接 patch Node | 华为昇腾、寒武纪、阿里 PPU、海光、沐曦、摩尔线程、壁仞，以及其余厂商 |

因此 Node Feature Discovery 并不是这些标签本身的前提——它只是一种实现路线，而且很多目标集群并不部署它，包括本目录用于验证的参考集群。AMD 对不同标签族同时使用了两种路线：其 Node Labeller 直接 patch Node，而 Accelerator Integration Module 写 NFD features 文件。Kasim 投射的是最终标签，而不是建模「由谁写」，所以场景不依赖目标集群本会走哪条路线。

真实集群上出现的 `xpu=on`、`mlu=on`、`dcu=on`、`ascend=on` 是完全不同的东西：它们是人工打的调度开关，供设备插件 DaemonSet 的 nodeSelector 使用，不是被发现的设备属性，Kasim 不输出它们。

## 开启发现标签

在 Node Group 的 `node` 上设置 `discoveryLabels: true`，或对 `apply demo` 快捷方式传入 `--discovery-labels`：

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

省略该字段时，规范场景字节与摘要保持不变；既有场景修订版不受影响。

## NVIDIA GPU Operator 标签

对一个 8 卡 H100 节点组，两个模拟节点会获得与 GPU Operator 管理节点一致的 36 个 `nvidia.com/*` 标签（仅缺 `gfd.timestamp` 与 `gpu.machine`，见"边界"）。GFD 通过 NFD 发布这些标签：

| 标签 | 值 | 来源 |
| --- | --- | --- |
| `nvidia.com/gpu.present` | `true` | 契约派生，静态值 |
| `nvidia.com/gpu.count` | `8` | 节点上加速器池总量之和 |
| `nvidia.com/gpu.product` | `NVIDIA-H100-80GB-HBM3` | 型号证据，GFD 规范化产品名 |
| `nvidia.com/gpu.family` | `hopper` | 型号证据 |
| `nvidia.com/gpu.compute.major` | `9` | 型号证据 |
| `nvidia.com/gpu.compute.minor` | `0` | 型号证据 |
| `nvidia.com/gpu.memory` | `81920` | 型号证据，MiB 内存包络 |
| `nvidia.com/gpu.mode` | `compute` | 型号证据，数据中心板的 PCI class 模式 |
| `nvidia.com/mig.capable` | `true` | 型号证据（L40S 为 `false`） |
| `nvidia.com/mig.strategy` | `single` | 契约派生，gpu-operator 默认值 |
| `nvidia.com/gpu.replicas` | `1` | 契约派生，未配置共享 |
| `nvidia.com/gpu.sharing-strategy` | `none` | 契约派生，未配置共享 |
| `nvidia.com/mps.capable` | `false` | 契约派生，未配置 MPS |
| `nvidia.com/vgpu.present` | `false` | 契约派生，无 vGPU manager |
| `nvidia.com/cuda.driver-version.full` | `580.126.16` | 契约派生，与 DCGM 遥测共享的驱动证据 |
| `nvidia.com/cuda.driver-version.major` | `580` | 契约派生 |
| `nvidia.com/cuda.driver-version.minor` | `126` | 契约派生 |
| `nvidia.com/cuda.driver-version.revision` | `16` | 契约派生 |
| `nvidia.com/cuda.driver.major` | `580` | 契约派生，GFD 已废弃键 |
| `nvidia.com/cuda.driver.minor` | `126` | 契约派生，GFD 已废弃键 |
| `nvidia.com/cuda.driver.rev` | `16` | 契约派生，GFD 已废弃键 |
| `nvidia.com/cuda.runtime-version.full` | `13.3.1` | 契约派生，最新 CUDA GA 版本 |
| `nvidia.com/cuda.runtime-version.major` | `13` | 契约派生 |
| `nvidia.com/cuda.runtime-version.minor` | `3` | 契约派生 |
| `nvidia.com/cuda.runtime.major` | `13` | 契约派生，GFD 已废弃键 |
| `nvidia.com/cuda.runtime.minor` | `3` | 契约派生，GFD 已废弃键 |
| `nvidia.com/gpu-driver-upgrade-state` | `upgrade-done` | 契约派生，operator 升级生命周期 |
| `nvidia.com/gpu.deploy.container-toolkit` | `true` | 契约派生，operator 组件状态 |
| `nvidia.com/gpu.deploy.dcgm` | `true` | 契约派生 |
| `nvidia.com/gpu.deploy.dcgm-exporter` | `true` | 契约派生 |
| `nvidia.com/gpu.deploy.device-plugin` | `true` | 契约派生 |
| `nvidia.com/gpu.deploy.driver` | `true` | 契约派生 |
| `nvidia.com/gpu.deploy.gpu-feature-discovery` | `true` | 契约派生 |
| `nvidia.com/gpu.deploy.node-status-exporter` | `true` | 契约派生 |
| `nvidia.com/gpu.deploy.nvsm` | ``（空） | 契约派生，NVSM 未部署 |
| `nvidia.com/gpu.deploy.operator-validator` | `true` | 契约派生 |

## AMD Instinct 标签

AMD 的 Kubernetes Node Labeller 会给每个 AMD GPU 节点打上 `amd.com/*` 属性。Kasim 投射锚定的 [k8s-device-plugin Node Labeller 文档](https://github.com/ROCm/k8s-device-plugin/blob/2af2fdbf29472b9e9c84087118c87e1bf2f804cd/cmd/k8s-node-labeller/README.md) 中针对所选型号公布的键与值：

| 标签 | 各型号取值 | 来源 |
| --- | --- | --- |
| `amd.com/gpu.cu-count` | `304`（MI300X） | Node Labeller `-cu-count`（A 级证据）；逐型号取值来自 AMD Instinct 产品文档 |
| `amd.com/gpu.device-id` | `74a1`（MI300X） | Node Labeller `-device-id`（A 级证据） |
| `amd.com/gpu.product-name` | `AMD_Instinct_MI300X_OAM`（MI300X） | Node Labeller `-product-name`（A 级证据） |
| `amd.com/gpu.simd-count` | `1216`（MI300X） | Node Labeller `-simd-count`（A 级证据） |
| `amd.com/gpu.family` | 已声明键，无取证取值 | Node Labeller `-family`（A 级证据）；文档给出的缩写表未覆盖 CDNA 系列 |
| `amd.com/gpu.vram` | 已声明键，无取证取值 | Node Labeller `-vram`（A 级证据）；仅公布了 legacy `16G` 形式示例 |

Node Labeller 还会发布 `driver-version`、`driver-src-version` 与 `firmware`；目录未声明这些键，因此 Kasim 不为它们输出任何 `amd.com/*` 标签，而不是去猜一个值。

## Intel 数据中心 GPU 标签

Intel 通过 NFD rule 发布数据中心 GPU 标签。锚定的 [`gpu_plugin` 标签文档](https://github.com/intel/intel-device-plugins-for-kubernetes/blob/6460392f95275dd68774aeef3c39f14538ddb3d9/cmd/gpu_plugin/labels.md) 原文写道："GPU labels originate from two main sources: NFD rules and GPU plugin (& NFD hook)"。Kasim 投射其中 rule 派生的集合：

| 标签 | 各型号取值 | 来源 |
| --- | --- | --- |
| `gpu.intel.com/device.count` | `8` | 节点上加速器池总量之和，与文档所述的规则一致 |
| `gpu.intel.com/family` | `Max_Series` / `Flex_Series` | NFD rule（A 级证据）；逐平台公布了示例值 |
| `gpu.intel.com/product` | `Max_1550` / `Flex_170` | NFD rule（A 级证据）；逐平台公布了示例值 |

Intel 文档列出四个已覆盖平台——Flex 140、Flex 170、Max 1100、Max 1550——但只公布了 Flex 170 与 Max 1550 的 `family`、`product` 示例值。其余两个型号保留已声明的键，值为空。

## 华为昇腾标签

同一机制覆盖华为昇腾设备插件。真实集群运行 MindCluster 的 Ascend Device Plugin，它会在每个 NPU 节点打芯片身份标签对；Kasim 从锚定的目录证据投射完全相同的键：

| 标签 | 各型号取值 | 来源 |
| --- | --- | --- |
| `accelerator` | `huawei-Ascend310` / `huawei-Ascend310P` / `huawei-Ascend910` | MindCluster `AcceleratorLabelKey`（A 级证据）；官方 Node label 使用说明表中标注的写入组件为 Ascend Device Plugin |
| `node.kubernetes.io/npu.chip.name` | `310` / `310P` / `910A` / `910B`（Atlas A2） | MindCluster `ChipNameLabel`（A 级证据）；取值按芯片家族粒度（B 级证据） |
| `servertype` | `Ascend310-4` / `Ascend310P-8` / `Ascend910-32` / `Ascend910B-20` | MindCluster `ServerTypeLabelKey`（A 级证据）；AI Core 数量来自华为产品文档（B 级证据） |

证据锚定到 MindCluster
[v26.1.0 release](https://gitcode.com/ascend/mind-cluster/blob/v26.1.0/component/ascend-device-plugin/pkg/common/constants.go)——该正式版本的标签常量与已审计的 master 快照逐字一致。

`huawei-atlas-a3` 不输出 vendor 标签：其芯片名与 AI Core 数量没有公开证据，目录不编造取值。`huawei-atlas-a2` 不输出 `accelerator` 取值：官方表为该节点类型列出的是 `huawei-Ascend910`，与其芯片家族已携带的值相同，目录不会为了填满一个键而重复取值。

真实设备插件还会对特定推理板打 `accelerator-type=card-910b-infer`、`infer-card-type=card-300i-duo` 以及 `mind-cluster/npu-chip-memory`；它们取决于板卡身份而非芯片型号，Kasim 有意不输出（见"边界"）。

## 阿里云 PPU 标签

阿里云 ACK 的 PPU 设备插件会在 PPU 节点上打 `aliyun.accelerator/*` 标签。Kasim 投射已公布的键，并把节点级标签与 PPU 设备插件运行期解析出的取值结合起来：

| 标签 | 值 | 来源 |
| --- | --- | --- |
| `aliyun.accelerator/xpu_type` | `ppu` | ACK 灵骏节点池指南（A 级证据），契约派生静态值 |
| `aliyun.accelerator/ppu_count` | `8` | 节点上加速器池总量之和 |
| `aliyun.accelerator/ppu_name` | `PPU-ZW810E` | ACK 指南示例值（A 级证据） |
| `aliyun.accelerator/ppu_mem` | `98304MiB` | ACK 指南示例值（A 级证据） |

`alibaba-ppu-m890p` 不输出 vendor 标签：指南只为 PPU-ZW810E 公布了示例值。

## 无取证取值的键

厂商标签的**键**在契约层声明，**取值**在型号层提供。已声明但该型号没有证据的键仍会被投射，只是值为空——例如 MI300X 的 `amd.com/gpu.family`、Atlas A2 的 `accelerator`，以及 `hygon.com/dcu*` 的全部键。这是有意为之：

- 键保留其冲突占位，用户在 Scenario 中手工提供同名键仍会编译失败，而不会与厂商语义静默不一致。
- 空值永远不会满足正向的 `nodeSelector` 匹配，因此依赖真实厂商取值的工作负载不会被错误调度。另一种做法——用型号 ID 或资源名顶替——会让放置测试因为一个真实节点根本不存在的标签而通过。

用 `kasim profile show <id> -o json` 查看哪些型号带有取证取值。厂商完全没有发布节点标签的键，会作为待办研究项记录，而不是被填上。

## 失败行为

- 型号在目录中没有节点标签证据时不输出 vendor 标签取值，场景仍可提交，该池只是不投射任何发现标签；可用 `kasim profile show` 查看型号证据。
- 用户在 Scenario 中提供的节点标签与 vendor 发现标签冲突时，编译失败并报 `reserved by vendor node discovery`；同型号多池之间的相同值合法，冲突值编译失败。
- 标签值纳入 `extended-resources` 保真面：reconcile 与 `kasim status` 会像容量一样逐节点校验标签，标签漂移会让 Snapshot 失败，而不是被静默重打。

## 边界

Kasim 有意不输出 `nvidia.com/gfd.timestamp`（破坏 Snapshot 确定性）与 `nvidia.com/gpu.machine`（主机特定，模拟节点无真实主机）。昇腾侧不输出 `accelerator-type`、`infer-card-type` 与 `mind-cluster/npu-chip-memory`，因为它们取决于实际插的板卡或芯片子型号而非所选模拟型号；`npu.chip.name` 按芯片家族粒度输出（`910B`，而非 `910B1`–`910B4`），子型号精度需要目录层拆分型号。CUDA runtime 版本按目录修订版锚定到最新 CUDA GA 版本，而非从节点 toolkit 安装探测；升级目录即可更新。

Kasim 投射的是厂商标签的**值**；它不会在目标集群上安装、运行或依赖 Node Feature Discovery、GPU Operator 或任何厂商设备插件，也不会投射 NFD 自己拥有的 `feature.node.kubernetes.io/*` 命名空间。诸如 `feature.node.kubernetes.io/pci-*.present` 这类集群特定的 NFD 标签，只有在真正运行 NFD 且规则匹配的集群上才可观测，因此 Kasim 不会合成它们。这些标签描述的是模拟调度清单，永远不证明驱动、设备文件或加速计算的存在。
