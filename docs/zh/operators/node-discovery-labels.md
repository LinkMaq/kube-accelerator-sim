# 节点发现标签

真实 GPU 集群通常运行 NVIDIA GPU Operator，其 GPU Feature Discovery（GFD）组件会在每个 GPU 节点上打一组 `nvidia.com/*` 标签。工作负载普遍通过 `nodeSelector` 或亲和性选择这些标签，例如 `nvidia.com/gpu.product: NVIDIA-H100-80GB-HBM3`。Kasim 可以把同一套调度可见标签投射到模拟节点上，使放置类测试走过完全一致的选择路径。

该特性证据驱动且默认关闭。它不声明驱动、设备文件、CUDA 或真实硬件，只投射 GFD 实际输出的标签键值，证据来源为固定 commit 的 [k8s-device-plugin GFD 文档](https://github.com/NVIDIA/k8s-device-plugin/blob/3c6be400411aad793892e976824910d0880dd3a8/docs/gpu-feature-discovery/README.md) 与目录中逐型号的证据数据。

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
            revision: 2026-09-05.1
            digest: sha256:a04f86407d43998b667d61e6d2bbbc91a3ef004634e776dfc2fe330716a2a479
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          count: 8
          healthy: 8
```

省略该字段时，规范场景字节与摘要保持不变；既有场景修订版不受影响。

## 输出的标签集

对一个 8 卡 H100 节点组，两个模拟节点会获得与 GPU Operator 管理节点一致的 36 个 `nvidia.com/*` 标签（仅缺 `gfd.timestamp` 与 `gpu.machine`，见"边界"）：

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

Kasim 归属标签（`simulation.kasim.io/*`）与 `feature.node.cloud.xiaoshiai.cn/accelerator-model.name` 保持原样。

## 华为昇腾发现标签

同一机制覆盖华为昇腾设备插件。真实集群运行 MindCluster 的 Ascend Device Plugin，它会在每个 NPU 节点打芯片身份标签对；Kasim 从锚定的目录证据投射完全相同的键：

| 标签 | 各型号取值 | 来源 |
| --- | --- | --- |
| `node.kubernetes.io/npu.chip.name` | `310` / `310P` / `910A` / `910B`（Atlas A2） | MindCluster `ChipNameLabel`（A 级证据）；取值按芯片家族粒度（B 级证据） |
| `servertype` | `Ascend310-4` / `Ascend310P-8` / `Ascend910-32` / `Ascend910B-20` | MindCluster `ServerTypeLabelKey`（A 级证据）；AI Core 数量来自华为产品文档（B 级证据） |

证据锚定到 MindCluster
[v26.1.0 release](https://gitcode.com/ascend/mind-cluster/blob/v26.1.0/component/ascend-device-plugin/pkg/common/constants.go)——该正式版本的标签常量与已审计的 master 快照逐字一致。`huawei-atlas-a3` 不输出 vendor 标签：其芯片名与 AI Core 数量没有公开证据，目录不编造取值。

真实设备插件还会对特定推理板打 `accelerator-type=card-910b-infer`、`infer-card-type=card-300i-duo` 以及 `mind-cluster/npu-chip-memory`；它们取决于板卡身份而非芯片型号，Kasim 有意不输出（见"边界"）。

## 失败行为

- 型号在目录中没有节点标签证据时不输出 vendor 标签，场景仍可提交，该池只是不投射任何发现标签；可用 `kasim profile show` 查看型号证据。
- 用户在 Scenario 中提供的节点标签与 vendor 发现标签冲突时，编译失败并报 `reserved by vendor node discovery`；同型号多池之间的相同值合法，冲突值编译失败。
- 标签值纳入 `extended-resources` 保真面：reconcile 与 `kasim status` 会像容量一样逐节点校验标签，标签漂移会让 Snapshot 失败，而不是被静默重打。

## 边界

Kasim 有意不输出 `nvidia.com/gfd.timestamp`（破坏 Snapshot 确定性）与 `nvidia.com/gpu.machine`（主机特定，模拟节点无真实主机）。昇腾侧不输出 `accelerator-type`、`infer-card-type` 与 `mind-cluster/npu-chip-memory`，因为它们取决于实际插的板卡或芯片子型号而非所选模拟型号；`npu.chip.name` 按芯片家族粒度输出（`910B`，而非 `910B1`–`910B4`），子型号精度需要目录层拆分型号。CUDA runtime 版本按目录修订版锚定到最新 CUDA GA 版本，而非从节点 toolkit 安装探测；升级目录即可更新。这些标签描述的是模拟调度清单，永远不证明驱动、设备文件或加速计算的存在。
