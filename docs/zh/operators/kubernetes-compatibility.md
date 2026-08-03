# Kubernetes 兼容性

0.1 版本把产品兼容性固定在 Kubernetes 1.30–1.36。这是有边界的功能声明，
不是无限期 `1.30+` 承诺，也不是对已停止维护版本的安全维护声明。

| Kubernetes | 截至 2026-07-30 的上游状态 | `scheduling` | `dra-control-plane` |
| --- | --- | --- | --- |
| 1.30.14 | EOL | 已验证 | 不支持 |
| 1.31.14 | EOL | 已验证 | 不支持 |
| 1.32.13 | EOL | 已验证 | 不支持 |
| 1.33.13 | EOL | 已验证 | 不支持 |
| 1.34.10 | 活跃 | 已验证 | 稳定版 `resource.k8s.io/v1` |
| 1.35.7 | 活跃 | 已验证 | 稳定版 `resource.k8s.io/v1` |
| 1.36.3 | 活跃 | 已验证 | 稳定版 `resource.k8s.io/v1` |

不可变的机器可读输入是
[`release/compatibility-lock.json`](../../../release/compatibility-lock.json)，其中记录准确
kind 版本、节点镜像摘要、Kubernetes 制品校验和、主机架构和检查日期。

## 验证频率

PR 与 `main` 在 1.30 下限、最早活跃版本 1.34 和 1.36 上限运行调度套件；稳定版
DRA 在 1.34 和 1.36 运行。夜间和手动发布验证覆盖全部七个调度版本，以及
1.34/1.35/1.36 的稳定版 DRA。失败版本不会被静默跳过。

成功的调度行上传 `kasim.io/compatibility-receipt/v1alpha1` JSON 回执，覆盖权限拒绝、
准入拒绝、服务端 dry-run、所有权冲突、真实 Node/Lease 安全、调度放置与耗尽、
健康度下降、超分配、扩缩容、控制器恢复、context 重定向、外部 Pod 阻塞清理和
零遗留所属对象。DRA 回执覆盖发现、清单、分配、预留、Pod 绑定、设备复用和清理。

## 真实性边界

产品 CLI 只向显式 kubeconfig/context 指向的已有集群提交场景。一次性 kind 集群的
创建与删除只存在于 `test/e2e`。

`scheduling` 证明 Kubernetes 控制平面放置和标量资源统计，不证明 Pod 执行、物理
设备访问、device-plugin gRPC、CDI 注入或 DRA 节点准备。稳定版 DRA 套件只证明
控制平面分配；独立的[Kubelet 协议基准](kubelet-protocol-oracle.md)验证节点运行时
协议行为，但不会把结果扩大为物理硬件声明。
