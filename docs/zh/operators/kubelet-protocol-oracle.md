# Kubelet 协议基准

Kubelet 协议基准是发布证据，不是产品运行模式。它在一次性 kind 集群的真实 kubelet
上运行小型、仅用于测试的 Device Plugin。产品仍只有 `scheduling` 和
`dra-control-plane` 两种保真模式。

每周和手动触发的 `protocol-oracle.yml` 在 Linux 上验证发布所选 Kubernetes 1.30
下限和 1.36 上限。每个版本使用固定摘要的 kind 节点镜像，并从待测源码修订直接
构建基准镜像。

## 覆盖的证据

- 对真实 kubelet 执行 Device Plugin v1beta1 注册；
- 为两个仅测试设备发布 Node capacity 与 allocatable；
- `ListAndWatch` 在健康、不健康、恢复健康之间转换；
- 调度器放置及 kubelet `Allocate` 调用；
- 插件 Pod 替换和重新注册；
- 清理 DaemonSet、Pod、Namespace 和基准 Unix socket。

成功版本行上传独立的 `kasim.io/protocol-oracle-receipt/v1alpha1` JSON 回执，记录
来源修订、时间、Kubernetes/kubelet 版本、准确节点镜像、基准镜像 ID、Dockerfile
校验和、工具版本、耗时、结果和排除项。

## 明确排除项

该基准没有物理加速器、厂商驱动、宿主机设备挂载、CDI 注入或加速器计算。
`Allocate` 只返回一个包含确定性假设备 ID 的环境变量。测试 DaemonSet 需要挂载
kubelet Device Plugin socket，并因 socket 属于 root 而在一次性容器中使用 UID 0；
这些权限绝不能复制到产品控制器或运行时 Chart。

发布前手动触发 **Kubelet protocol oracle** 并保留两个版本回执。缺少回执、清理
断言失败或矩阵失败都会阻塞协议声明，不能静默降级为控制平面模拟证据。
