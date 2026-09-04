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
            revision: 2026-09-04
            digest: sha256:75266b3202e76b55786a989bf920c1c4dc27f3e955e23b0db72fe2fabe94675e
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          count: 8
          healthy: 8
```

省略该字段时，规范场景字节与摘要保持不变；既有场景修订版不受影响。

## 输出的标签集

对一个 8 卡 H100 节点组，两个模拟节点会获得：

| 标签 | 值 | 来源 |
| --- | --- | --- |
| `nvidia.com/gpu.present` | `true` | 契约派生，静态值 |
| `nvidia.com/gpu.count` | `8` | 节点上加速器池总量之和 |
| `nvidia.com/gpu.product` | `NVIDIA-H100-80GB-HBM3` | 型号证据，GFD 规范化产品名 |
| `nvidia.com/gpu.family` | `hopper` | 型号证据 |
| `nvidia.com/gpu.compute.major` | `9` | 型号证据 |
| `nvidia.com/gpu.compute.minor` | `0` | 型号证据 |
| `nvidia.com/gpu.memory` | `81920` | 型号证据，MiB 内存包络 |

Kasim 归属标签（`simulation.kasim.io/*`）与 `feature.node.cloud.xiaoshiai.cn/accelerator-model.name` 保持原样。

## 失败行为

- 型号在目录中没有节点标签证据时不输出 vendor 标签，场景仍可提交，该池只是不投射任何发现标签；可用 `kasim profile show` 查看型号证据。
- 用户在 Scenario 中提供的节点标签与 vendor 发现标签冲突时，编译失败并报 `reserved by vendor node discovery`；同型号多池之间的相同值合法，冲突值编译失败。
- 标签值纳入 `extended-resources` 保真面：reconcile 与 `kasim status` 会像容量一样逐节点校验标签，标签漂移会让 Snapshot 失败，而不是被静默重打。

## 边界

Kasim 有意不输出 `nvidia.com/gfd.timestamp`（破坏 Snapshot 确定性）、`nvidia.com/gpu.machine`（主机特定）、`gpu.clique`（NVLink 拓扑超出保真边界）、MIG 策略标签（分区通过资源别名选择）以及 CUDA runtime 版本标签（不声明 CUDA runtime 保真）。这些标签描述的是模拟调度清单，永远不证明驱动、设备文件或加速计算的存在。
