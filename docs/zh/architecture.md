# 架构导读

Kasim 是一个面向 Kubernetes 控制平面的加速器容量模拟器。它把声明式
Scenario 编译成可审计的资源计划，再由集群内共享运行时调谐 Synthetic
Node、Lease 以及可选的稳定版 DRA 资源。

## 产品边界

- CLI 只向用户明确指定的已有集群提交场景，不创建、升级或删除集群。
- `scheduling` 模式在 Kubernetes 1.30–1.36 上投射扩展资源容量。
- `dra-control-plane` 模式在 Kubernetes 1.34–1.36 上投射稳定版
  `resource.k8s.io/v1` 清单和分配过程。
- 运行时只管理带有准确实例 UID 和所有权标签的对象。
- 独立只读遥测进程可保留有来源依据的原厂 Prometheus 结构，但所有值都明确标记为模拟。
- 本项目不提供驱动、物理设备、容器设备注入或加速器计算能力。

## 主要模块

| 模块 | 责任 |
| --- | --- |
| Scenario 编译器 | 校验声明、解析 Vendor Profile、生成规范摘要和编译回执 |
| CLI | 离线检查、显式目标预检、提交、状态、健康度、扩缩容和安全删除 |
| API | 保存带修订号、目标指纹和生命周期状态的 Scenario Instance |
| Controller | 调谐该实例准确拥有的 Kubernetes 控制平面对象 |
| Vendor Profile 目录 | 保存不可变的厂商资源契约、型号、证据等级和摘要 |
| Simulated Vendor Telemetry | 从准确归属清单生成确定性关联指标、不可变抓取缓冲区和过期诊断 |
| Telemetry Contract 目录 | 独立保存原厂指标名、类型、单位、原生标签、证据状态和版本摘要 |
| Helm Chart | 在已有集群安装共享运行时、KWOK 依赖和最小权限 RBAC |
| 验证体系 | 对兼容版本、协议基准、规模和发布制品生成可追溯回执 |

## 生命周期

```text
Scenario
   │ compile + digest
   ▼
Scenario Instance ── reconcile ──► Synthetic Nodes / Leases / DRA inventory
   │                                      │
   └──────────── status receipt ◄─────────┘
                                          │ read-only observation
                                          ▼
                                kasim-telemetry /metrics
```

`health` 和 `scale` 会创建类型化修订，而不是直接修改 Node。删除操作要求准确的
实例 UID 和期望修订号，并在遇到绑定到 Synthetic Node 的非所属 Pod 时返回
`CleanupBlocked`，不会强制驱逐用户工作负载。

## 权威设计记录

架构决策和规范以英文原文为权威来源：

- [v1 产品规范](/spec/v1)
- [保真模式与模拟后端](/adr/0001-fidelity-modes-and-simulation-backends)
- [Vendor Profile 与型号契约](/adr/0002-vendor-profile-and-model-contract)
- [修订化 Scenario Instance 契约](/adr/0003-revisioned-scenario-instance-contract)
- [显式目标与回执驱动 CLI](/adr/0005-explicit-target-receipt-driven-cli)
- [深模块与扩展边界](/adr/0007-deep-modules-and-extension-seams)
- [模拟厂商遥测](/adr/0008-simulated-vendor-telemetry)

中文操作文档会随产品行为同步更新；改变规范或架构时，必须先更新英文权威记录，
并同步修订本导读中受影响的边界。
