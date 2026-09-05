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
            revision: 2026-09-05
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

## 失败行为

- 型号在目录中没有节点标签证据时不输出 vendor 标签，场景仍可提交，该池只是不投射任何发现标签；可用 `kasim profile show` 查看型号证据。
- 用户在 Scenario 中提供的节点标签与 vendor 发现标签冲突时，编译失败并报 `reserved by vendor node discovery`；同型号多池之间的相同值合法，冲突值编译失败。
- 标签值纳入 `extended-resources` 保真面：reconcile 与 `kasim status` 会像容量一样逐节点校验标签，标签漂移会让 Snapshot 失败，而不是被静默重打。

## 边界

Kasim 有意不输出 `nvidia.com/gfd.timestamp`（破坏 Snapshot 确定性）与 `nvidia.com/gpu.machine`（主机特定，模拟节点无真实主机）。CUDA runtime 版本按目录修订版锚定到最新 CUDA GA 版本，而非从节点 toolkit 安装探测；升级目录即可更新。这些标签描述的是模拟调度清单，永远不证明驱动、设备文件或加速计算的存在。
