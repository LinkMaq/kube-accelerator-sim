# v1 最终需求审计

规范来源是英文 [v1 产品规范](/spec/v1)。
[`release/traceability.json`](../../../release/traceability.json) 为每个规范 ID 保存一条
机器可读记录；生成的[需求追踪表](../../operators/requirement-traceability.md)提供相同映射。

## 审计结果

| 领域 | 可评审结果 |
| --- | --- |
| 产品边界 | CLI 只操作显式已有目标；集群生命周期只属于测试基础设施 |
| 保真度 | `scheduling` 覆盖 1.30–1.36；稳定版 DRA 控制平面投射限于 1.34–1.36 |
| 目录 | verified、provisional、custom、catalog-only 语义分离；写入前解析准确修订与摘要 |
| 场景 | 单机单卡、单机多卡、多机多卡、异构、健康、扩缩容、DRA 和阻塞删除均有版本化示例与可执行契约 |
| 生命周期安全 | UID/修订/目标前置条件、准确所有权、真实 Node 保护、外部 Pod 阻塞和零泄漏均经过测试 |
| 操作流程 | 安装、提交、回执、状态、修订、安全删除、卸载、升级、回滚和排障都有文档 |
| 证据 | CI、七版本调度、三版本稳定 DRA、协议基准、两轮 1,000 Node 规模及发布工作流均生成来源绑定回执 |
| 发布 | 五种 CLI、控制器镜像、OCI/TGZ Chart、校验和、SBOM、来源证明、依赖锁和签名均受门禁约束 |

## 文档验证

`test/contract/documentation_test.go` 检查规范 ID 映射、所有场景的真实 CLI dry-run、
操作文档本地链接，以及显式目标、版本边界、集群生命周期分离和保真排除项。
`make verify` 还会检查生成引用漂移、格式、vet、单元/集成/race、架构和 Helm。

最终发布声明有意保持狭窄：模拟器只证明 Kubernetes 控制平面行为，不提供设备访问、
加速器计算、厂商驱动/遥测、NUMA 模拟或 CDI 注入。
