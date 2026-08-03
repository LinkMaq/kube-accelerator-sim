# 场景示例

每个示例都是规范化、版本化的 Scenario 文档。写入集群前先离线校验：

```sh
./dist/kasim apply -f ./examples/heterogeneous.yaml \
  --dry-run=client \
  -o json
```

联网提交始终显式指定已有目标：

```sh
./dist/kasim apply -f ./examples/heterogeneous.yaml \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

| 场景 | 文件 | 预期控制平面形态 |
| --- | --- | --- |
| 单机单卡 | [single-node-single-accelerator.yaml](../../../examples/single-node-single-accelerator.yaml) | 一个 Synthetic Node，包含一个 `nvidia.com/gpu` 单位 |
| 单机多卡 | [single-node-multi-accelerator.yaml](../../../examples/single-node-multi-accelerator.yaml) | 一个 Synthetic Node，包含八个单位 |
| 多机多卡 | [multi-node-multi-accelerator.yaml](../../../examples/multi-node-multi-accelerator.yaml) | 四个同构 Synthetic Node，每个八个单位 |
| 异构节点组 | [heterogeneous.yaml](../../../examples/heterogeneous.yaml) | 分离的 NVIDIA H100 与华为 Atlas A2 节点组 |
| 厂商预设 | [examples/vendors](../../../examples/vendors) | 17 个可选厂商生态各自可直接提交的 Scenario |
| 扩展资源变体 | [extended-resource-variants.yaml](../../../examples/signals/extended-resource-variants.yaml) | 替代、切分、共享、虚拟、显存和核心资源信号 |
| 稳定版 DRA | [dra-control-plane.yaml](../../../examples/dra-control-plane.yaml) | Kubernetes 1.34–1.36 上的 NVIDIA、AMD、AWS Neuron `resource.k8s.io/v1` 清单 |
| 参考规模 | [reference-scale.yaml](../../../test/e2e/testdata/reference-scale.yaml) | 1,000 个 Synthetic Node、8,000 个单位的发布门禁 |

完整厂商/资源矩阵及其证据性省略项见[示例索引](../../../examples/README.md)。例如：

```sh
./dist/kasim apply -f ./examples/vendors/hygon.yaml \
  --dry-run=client \
  -o json

./dist/kasim apply -f ./examples/signals/extended-resource-variants.yaml \
  --dry-run=client \
  -o json
```

昆仑芯示例会显式接受临时的 `kunlunxin-hami` 档案。CLI 回执会报告其证据等级、
HAMi 适用范围和依据，不会把它静默提升为已验证厂商默认值。

## 同构演示快捷命令

快捷命令与 Scenario 文件共用同一套编译和生命周期路径：

```sh
./dist/kasim apply demo \
  --profile nvidia \
  --model nvidia-h100 \
  --contract device-plugin \
  --resource gpu \
  --nodes 2 \
  --accelerators-per-node 8 \
  --healthy-per-node 8 \
  --dry-run=client \
  -o json
```

去掉 `--dry-run=client` 并增加 `--kubeconfig` 与 `--context` 才会提交。异构配置和
多资源池配置必须使用 Scenario 文档，不能使用快捷命令。

## 健康、容量与扩缩容

示例按 Synthetic Node 设置 `count`（容量）和 `healthy`（可分配数量）。
`healthy < count` 表示部分设备不健康，但场景仍可处于 Ready。`health` 只修订这个
类型化字段；`scale` 修改一个 Node Group 的副本数。缩容按稳定副本索引从高到低
处理。容量减少可能报告 `Overcommitted`，但不会移动现有 Pod。

## DRA 门禁

`dra-control-plane` 要求稳定版 `resource.k8s.io/v1`，因此低于 1.34 或超出
1.34–1.36 时预检会拒绝。它模拟 DRA 清单、Claim、分配、调度器预留和清理，
不执行节点准备或容器设备注入。

所有示例只模拟 Kubernetes 可见表面：不提供设备访问、加速器计算、厂商驱动或
遥测，不模拟 NUMA，也不注入 CDI 设备。
