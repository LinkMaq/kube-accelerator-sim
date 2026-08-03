# `kasim ui` 集群模拟清单提案

状态：**已完成设计，当前版本尚未实现。**

本文把 [Wayfinder 决策地图](https://github.com/LinkMaq/kube-accelerator-sim/issues/34)、[Kubernetes 清单研究](../../research/kasim-ui-kubernetes-inventory.md)和[加速器与 RDMA 信号研究](../../research/kasim-ui-accelerator-rdma-signals.md)收敛为可直接实施的产品契约。相关领域词汇见 [`CONTEXT.md`](../../../CONTEXT.md)，架构决策见 [ADR 0008](../../adr/0008-stream-cluster-simulation-inventory-snapshots.md)、[ADR 0009](../../adr/0009-model-auxiliary-device-pools.md)和 [ADR 0010](../../adr/0010-embed-authenticated-loopback-ui.md)。

## 目标

`kasim ui` 为一个显式选择的 Simulation Target 启动临时、只读的本地网页。首页直接回答：

1. 哪些是 Kasim Synthetic Node，哪些节点不归 Kasim 管理？
2. Kubernetes 当前暴露了哪些加速器与辅助设备信号？
3. 哪些只是标量数量，哪些是拥有可验证原生标识的 DRA 设备？
4. 容量、可分配量、已观测 Pod 请求、DRA 分配和健康证据分别是什么？
5. 数据是实时、过期、不完整、无权限还是 API 不支持，原因是什么？

UI 不创建、修改、扩缩、恢复或删除 Scenario，不管理 Kubernetes 集群生命周期，也不宣称具备真实加速计算或 RDMA 能力。

## 命令契约

```text
kasim ui \
  --kubeconfig /absolute/path/to/config \
  --context exact-context-name \
  [--port 8080] \
  [--open]
```

- 两个目标参数都必须显式提供，不读取 current-context、`KUBECONFIG` 或集群内配置。
- 只能监听 `127.0.0.1`，不提供 `--address`。
- 默认端口为 `8080`，`--port` 接受 `1..65535`。
- 命令输出 context、脱敏目标指纹、数据新鲜度和访问 URL。
- `--open` 只在监听器和目标身份就绪后打开浏览器；打开失败只告警，因为 URL 仍可手动访问。
- 端口冲突、目标无效、DNS/TLS/认证失败、Kubernetes 版本不兼容或目标指纹冲突会返回稳定启动诊断并关闭监听器。
- `SIGINT` 和 `SIGTERM` 取消同步、停止接收请求，并在五秒内优雅退出。
- 单个数据源缺权或缺少可选 API 不阻止启动，页面进入部分可用或仅诊断状态。

命令遵循每个 release 有界的 Kubernetes 兼容矩阵，当前为 1.30–1.36；1.30 是最低版本，并不代表对尚未验证的新版本无限承诺。

## 本地安全契约

每次启动至少生成 256 位随机能力 token，URL 使用 fragment：

```text
http://127.0.0.1:8080/#token=<base64url-capability>
```

Fragment 不会发送到 HTTP 请求、服务端日志或 referrer。前端只把 token 保存在内存中，并在所有数据请求中发送 `Authorization: Bearer`；不写入 local storage、Cookie、日志或页面正文。

本地服务必须：

- 校验准确的回环 Host；
- 没有 token 时不返回集群数据；
- 只接受 `GET` 和 `HEAD`，不存在修改接口；
- 不发送宽松 CORS；
- 设置严格 CSP、`frame-ancestors 'none'`、`object-src 'none'`、`base-uri 'none'`、同源资源策略和 `nosniff`；
- 对 HTML 和数据设置 `Cache-Control: no-store`；
- 不使用统计分析、Service Worker、远程字体、CDN 或第三方请求；
- 只提供同一 `kasim` 版本内部使用的版本化 JSON/Fetch 流，不承诺远程公共 API。

优先使用能携带 Bearer token 的 fetch streaming，失败时回退为有界鉴权轮询。禁止把 token 放进 query string，也不引入 Cookie 登录流程。

## 首页与交互

[证据优先清单 Variant A](https://github.com/LinkMaq/kube-accelerator-sim/tree/prototype/kasim-ui-options/prototypes/kasim-ui) 是胜出原型；该分支保存桌面和移动端截图作为设计依据。

生产首页顺序为：

1. 目标集群、连接状态、实时/过期/部分状态和更新时间。
2. 缺失或过期证据的显著诊断。
3. 移动端先展示设备与信号清单；桌面端先放紧凑汇总条，紧接清单。
4. 按单位和表现形式严格分开的汇总值。
5. 全部节点清单，Kasim Synthetic Node 优先。
6. 借鉴原型 Variant B 的节点/证据详情抽屉。

无需 hover 就能看到：

- 节点和 Kasim 所有权；
- 只有来源可验证时才显示的厂商和型号；
- 加速器、辅助设备或未分类信号；
- 标量扩展资源或原生 DRA 设备；
- 准确资源名或 DRA 原生标识；
- 容量、可分配量、已观测 Pod 请求和分配阶段；
- 有证据的健康状态，或“未知/未报告”；
- Profile/Resource Contract 或 Kubernetes 数据来源；
- Auxiliary Device Pool 关联和“仅调度信号”边界。

搜索以及来源、Scenario、厂商、型号、信号角色、表现形式、健康度、数据源状态筛选都写入 URL。浏览器前进/后退恢复相同页面状态；能力 token 始终留在 fragment，不进入查询参数。

内置英文和简体中文，根据浏览器语言首次选择，并允许页面切换。Kubernetes 字段名、准确资源名、对象名、驱动 ID 和厂商标识保持原文。

## 视觉与可访问性

这是运维清单，不是装饰性图表面板。生产实现使用语义化 HTML 表格、直接标签、紧凑汇总、分隔线和证据抽屉，不需要图表库、Canvas、WebGL、动画或生成式图片。

颜色始终带文字和形状冗余：Kasim 使用青色和 `Kasim`，非 Kasim 节点使用中性灰和 `Non-Kasim`，辅助信号使用紫色和 `Auxiliary`；绿色只用于有证据的正常/报告/模拟可用状态；黄色配合未知、部分或不支持；红色配合过期、离线或终止诊断。

键盘可以操作筛选、清单行、详情抽屉、语言切换和关闭/重置。焦点始终可见，抽屉关闭后回到原行，Escape 可关闭；关键值不能只靠 hover。实时更新使用克制的 live region，不逐条朗读 watch 事件。

在 360–430 CSS 像素下，准确设备/信号清单位于汇总之前；表格转为带字段标签的记录；筛选折叠后仍显示生效值；详情抽屉变成全宽面板；主要触控目标至少 44 CSS 像素。

JavaScript 禁用或加载失败时，静态页不暴露任何集群数据，只说明本地实时清单需要 JavaScript；CLI 输出是恢复入口。

## Cluster Simulation Inventory 深模块

UI 只消费独立深模块，不调用 client-go、不解析 CRD、不解释 condition，也不处理 Kubernetes watch cursor。

```go
type Module struct { /* private implementation */ }

func (m *Module) Open(
    context.Context,
    OpenRequest,
) (SnapshotStream, error)

type SnapshotStream interface {
    Next(context.Context) (Snapshot, error)
    Close() error
}
```

`OpenRequest` 只包含显式 `cluster.TargetSelection`。`Open` 固定目标身份后返回；第一次 `Next` 返回 revision 1，通常为 loading 或 partial。之后每个值都是完整、不可变的替换快照，本地 revision 单调递增。慢消费者只保留最新待发快照。

临时数据源错误不终止流。只有 context 取消、流关闭、目标冲突或内部不变量破坏才由 `Next` 返回终止错误。`Close` 可重复、并发安全，并等待模块创建的 watch、重试、计时器和 goroutine 全部停止。

私有 Kubernetes collection seam 具有 client-go 生产 Adapter 和 deterministic recording/in-memory 测试 Adapter，封闭支持 Nodes、Pods、Scenario Instances、ResourceSlices、ResourceClaims 和 DeviceClasses。读取模型不得包含 Kubernetes runtime object、unstructured payload、GVR、kubeconfig 内容、凭据、Secret、Pod 日志、容器环境变量或未经脱敏的服务端错误。

## 可信读取模型

每个数量、身份、分类、分配和健康字段都属于带证据的 Fact：

```text
Fact 状态：known | unknown | unavailable | incomplete
证据类型：observed | derived | kasim-simulated | unavailable
完整度：loading | complete | partial | diagnostics-only
新鲜度：loading | fresh | reconnecting | stale
```

只有 `state=known, value=0` 才是已验证的零；禁止用零替代无权限、不支持、不完整、过期或未知。

快照包含目标和本地 revision、数据源状态、有限诊断、单位安全汇总、Scenario、Node、标量资源信号、原生 DRA Device、DRA pool 完整性、ResourceClaim 分配/Pod 关联以及省略数量报告。

解释规则：

- `Kasim Synthetic` 必须通过 managed-by、Instance UID 和 Scenario Instance 的准确联合验证。其他节点标为 `Non-Kasim`，这不代表已证明物理硬件。
- 扩展资源只产生一条标量信号，不生成虚构设备行或设备 ID。
- 稳定版 DRA 设备保留 `(driver, pool, device)`，厂商 UUID 只是另一项属性。
- capacity 和 allocatable 来自 Node status，都不是利用率。
- `requestedFromObservedPods` 是调度预留估算，`allocatable - requested` 是剩余估算，不是物理空闲量。
- DRA 分配、reservation、已观测调度消费者和运行时使用是不同状态；仅通过 API Server 时，运行时使用保持未知。
- Node Ready、allocatable、Claim Ready 和设备健康不能互相替代。
- 未知资源名原样展示并保持未分类，不能根据域名片段猜厂商或型号。
- 共享令牌、内存、分区、核心和虚拟功能不能计入物理卡总数。
- 辅助信号不证明网卡、链路、驱动、CNI、fabric、GPUDirect 或网络数据面。

## Auxiliary Device Pool 契约

Scenario 在 `acceleratorPools` 同级增加可选集合：

```yaml
auxiliaryDevicePools:
  - name: rdma-a
    profile:
      id: rdma-shared-device-plugin
      revision: pinned-revision
      digest: sha256:pinned-profile-digest
    contract: shared-hca
    resource: shared-token
    resourceName: rdma/rdma_shared_device_a
    count: 8
    available: 8
    associatedAcceleratorPools: [h100]
```

新 Catalog schema 把 Resource Contract 明确分类为 `accelerator` 或 `auxiliary`。辅助契约还声明来源可验证的类别与资源命名策略：Profile 固定准确全限定名，或因为上游插件允许配置而要求 Scenario 显式给出准确名称。

编译器只允许对 `scenario-required` 契约提供名称，要求至少关联一个当前 Node Group 内的 Accelerator Pool，并拒绝重复池名、无法解析的关联、available 大于 count、与 Node capacity 或其他池冲突、Fidelity 不支持以及无来源的契约。没有辅助池的旧 Scenario 保持原有 canonical bytes 和 digest。

`count` 与 `available` 只描述模拟容量和可调度性，不代表物理健康。辅助池跟随 Node Group 的 revision、scale、receipt、status、ownership 和 cleanup。首批内置模板覆盖 RDMA Shared Device Plugin 与 SR-IOV Network Device Plugin 有来源依据的可配置资源契约。

## Kubernetes 兼容与刷新状态机

兼容矩阵内所有版本使用稳定 Node、Pod 与 Kasim Scenario Instance API。只有发现预期 `resource.k8s.io/v1` 资源和字段时才解释稳定 DRA，目前对应 Kubernetes 1.34–1.36。1.30–1.33 的旧 DRA 或未知 schema 标记为 `unsupported-schema`，禁止按字段猜解，也不能提高 Kasim Fidelity。

每个数据源分别记录 availability、mode 和 freshness：

```text
availability: available | forbidden | unsupported | unsupported-schema | failed
mode: initializing | live | polling | snapshot-only | unavailable
freshness: fresh | reconnecting | stale | resyncing | incomplete
```

同步流程为 discovery → 分页 list → 从 list resourceVersion 开始 watch。默认分页 500；Pod 只进行一次跨 namespace 集群级同步；事件突发先 debounce 250ms，最长两秒必须发布；断线使用 250ms 到 30s 的 full-jitter 指数退避；断线立即标记 reconnecting，15 秒后标记 stale；`410 Gone` 只重建对应数据源；list 可用但 watch 被拒绝时每 30 秒重新 list 并标记 polling；任何重连或局部失败期间都保留最后一次成功数据。

安全上限沿用当前项目基线：16,384 个 collection 对象、65,536 Pods、65,536 Claims、每个 ResourceSlice 128 个设备。超过上限的数据源必须标记 incomplete，相关总数和派生值不能显示为准确值。输出稳定排序，默认每页 100、最大 500，诊断和省略数量都有边界。

## 只读权限

完整清单只需要 Nodes、Pods、ScenarioInstances、ResourceSlices、ResourceClaims、DeviceClasses 的 `list` 和 `watch`。不得读取 Secret、日志、metrics、`nodes/proxy`，也不得请求 impersonation、token 创建或任何写操作。SelfSubjectAccessReview 只能作为可选诊断，数据路径不能依赖其 `create` 权限。

Pods 无权限时仅移除 requested，不移除 capacity；Claims 无权限时仅移除 allocation，不移除 DRA publication；没有 DRA 时仍显示扩展资源；Nodes 无权限时显示 Scenario-only 或 diagnostics-only，不能谎报“空集群”。

## 前端与包体预算

生产代码使用标准语义 HTML、CSS 和 JavaScript module，通过 Go `embed` 嵌入。没有前端框架、图表库、远程资产、运行时 Node.js 或外部静态目录。允许可复现的构建期压缩，但源代码必须可审阅。

Release gate：静态资源总计不超过 256 KiB 未压缩和 96 KiB gzip；UI 导致的压缩 release binary 增量不超过 1 MiB，并逐平台报告；页面不能发起跨域请求；可见页最多渲染 100 行，不能为缓存中的每个 Pod 创建 DOM；1,000 Node 清单仍可搜索和打开详情。

throwaway 原型的 HTML、JavaScript 和 CSS 总计 33,721 字节；其中 JavaScript 和 CSS gzip 后约为 5.8 KiB 与 3.6 KiB。这只证明无框架路线可行，不是生产基线。

## 验证矩阵

| 层级 | 必须提供的证据 |
| --- | --- |
| Domain/unit | Fact 状态、known zero、单位安全汇总、Kasim 所有权、禁止厂商/健康猜测、Pod request、DRA identity、辅助池数量/可用性/关联校验。 |
| Module Interface | loading 首帧、完整替换、单调 revision、慢消费者合并、partial/stale、目标冲突、幂等关闭且无 goroutine 泄漏。 |
| Kubernetes Adapter | discovery、分页 list/watch、bookmark、断线、410、list-only polling、403、GVR/schema 不支持、稳定 DRA v1、禁止泄漏 raw object。 |
| HTTP/security | 仅回环、准确 Host、token 三态、token 不进日志/referrer/cache、GET/HEAD、无 CORS、CSP、安全头、信号优雅退出。 |
| Browser/component | 中英文、URL 筛选、键盘/焦点/抽屉、无 hover-only、390px/桌面、partial/stale/offline/empty、screen reader、无 JS 回退。 |
| Visual regression | 证据清单桌面、中文部分权限手机、详情抽屉、长资源名、未知健康、Kasim 与 Non-Kasim 混合。 |
| E2E scheduling | Kubernetes 1.30 floor 与当前 ceiling、多 Vendor Profile、标量信号、Pod 调度/request、非 Kasim 节点、重连和部分 RBAC。 |
| E2E DRA | 所有稳定 DRA 版本、完整/不完整多 Slice pool、原生 ID、Claim 分配/reservation/Pod join、runtime unknown、旧 schema 不支持。 |
| Auxiliary E2E | 两个 Synthetic Node，同时包含加速器与可配置 RDMA token pool；准确 Node resource；显式关联；scale/revision/status/cleanup；不宣称物理网络。 |
| Scale/performance | 1,000 Nodes、65,536 Pod/Claim fixture、事件突发、慢浏览器、有界诊断/payload/DOM、包体预算。 |
| Release/docs | 跨平台二进制嵌入资产；校验和/签名可复现；Docker/Helm 仍有效；示例、agent skill、中英文档同步。 |

## 验收演示

候选 release 必须在一个已有集群中同时展示：至少一个 Non-Kasim Node；三个以上 Kasim Synthetic Node 且覆盖至少三种加速器生态；一个标量 Accelerator Pool；一个含原生设备 ID 和 Claim 分配的完整稳定 DRA pool；一个显式关联加速器的 RDMA Auxiliary Device Pool；Pod 请求；一个保持未知的健康字段；一个只影响相关字段的无权限或不支持数据源；一次保留旧数据的断线重连；英文桌面与中文移动端。

成功标准不是网页能打开，而是信息真实且可导航：标量资源没有虚构设备，共享 token 不进入卡数，数据缺口清晰可见，CLI 退出后没有残留进程或集群修改。

## 明确不做

- Kubernetes 集群安装或生命周期；
- 远程或长期运行的 Dashboard；
- 多集群、账号或远程鉴权；
- 从 UI 修改 Scenario；
- metrics-server 或厂商遥测；
- kubelet PodResources、`nodes/proxy`、日志、Secret 或容器环境；
- 真实设备、驱动、固件、CUDA/ROCm/CANN、CNI、RDMA fabric 或网络数据面；
- 猜测厂商、型号、拓扑、设备 ID 或健康；
- 稳定公开的浏览器 API。
