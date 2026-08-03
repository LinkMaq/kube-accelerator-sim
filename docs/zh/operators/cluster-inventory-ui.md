# 只读集群清单 UI

`kasim ui` 为一个 Kubernetes 集群启动临时、证据优先的加速卡与辅助信号视图。
默认使用与 kubectl 相同的 kubeconfig 和当前 context。它会读取 Kasim
Synthetic Node 和其他真实节点，但始终把 Kasim 节点放在前面并显著区分。

```sh
./dist/kasim ui --port 8080 --open
```

标准 client-go 加载规则会读取 `KUBECONFIG`；未设置时读取 `~/.kube/config`，
并选择 current-context。需要时可只覆盖其中一项，或同时准确指定两项：

```sh
./dist/kasim ui --context target
./dist/kasim ui --kubeconfig ./target.kubeconfig
./dist/kasim ui --kubeconfig ./target.kubeconfig --context target
```

CLI 会先打印解析后的 context，再打印访问 URL，并在进程生命周期内冻结该目标。
如果不是预期集群，应立即停止。这个默认行为只属于只读 UI；生命周期命令仍要求
同时显式提供 `--kubeconfig` 和 `--context`。

该命令支持 Kubernetes 1.30–1.36，不负责集群生命周期。`--open` 是可选项；
即使打开浏览器失败，终端打印的 URL 仍可使用。监听地址固定为
`127.0.0.1`，不会提供 `--address` 参数。

## 首页展示内容

首页首先展示设备与信号账本，而不是装饰性图表。它包含：

- 精确的节点所有权和 Scenario 关联；
- 有来源依据时才显示的厂商和型号；
- 标量扩展资源的容量、可分配量和已观测 Pod 请求，不虚构单设备 ID；
- 原生 DRA `(driver, pool, device)` 标识、允许解释的属性，以及彼此独立的
  已分配、已预留和已观察调度消费者证据；
- RDMA、SR-IOV 等辅助调度令牌及其关联的 Accelerator Device Pool；
- 各数据源的权限、watch/轮询模式、新鲜度和有界诊断。

未知信息始终保持未知。Node Ready 不等于设备健康，可分配量不等于利用率，
辅助令牌也不能证明物理网卡、链路、CNI、网络、GPUDirect 或运行时数据通路
可用。

节点来源、Scenario、厂商、型号、角色、表示方式、健康和数据源状态筛选会写入
查询字符串，因此浏览器前进/后退可以恢复视图。访问能力只保留在 URL fragment
中，不会复制到筛选参数。中英文资源都嵌入同一个二进制。

## 本地安全

每次启动都会生成新的 256 位临时访问能力。CLI 会打印类似下面的 URL：

```text
http://127.0.0.1:8080/#token=<临时访问能力>
```

fragment 不会随初始 HTTP 请求发送。前端只在内存中保存它，并在读取清单时作为
Bearer 凭据发送。服务端只接受 `GET` 和 `HEAD`，校验准确的回环 Host，不提供
写接口或宽松 CORS，禁止缓存，并且只提供内嵌的 HTML、CSS 和 JavaScript。
按 `Ctrl+C` 停止；关闭过程会取消 watch，并在五秒内结束。

终端打印的 URL 应按临时密钥处理。在进程结束前，同一机器上能使用该 URL 的人
可以读取同样的集群元数据。

## 只读权限与部分视图

完整视图需要对 Node、Pod、Scenario Instance、ResourceSlice、ResourceClaim
和 DeviceClass 执行集群级 `list` 与 `watch`。运行时 Chart 会给控制器授予这些
读取权限；本地用户的 kubeconfig 仍须独立拥有相应权限。

每个数据源独立退化。如果允许 list 但禁止 watch，Kasim 会把该源标记为
`polling`，每 30 秒刷新一次。watch 临时失败时会保留最后成功数据并标记
`reconnecting`，断开 15 秒后标记 `stale`，再用有上限的随机退避重试。
Kubernetes 1.34 以下缺少稳定 DRA API 时会显示 `unsupported-schema`，标量
Node 清单仍可使用。

解释总数前应先查看数据源诊断。`partial` 或 `diagnostics-only` 页面不会被冒充成
完整证据。

## 辅助信号示例

提交前先离线编译内置的 H100/RDMA 与 MI300X/SR-IOV 示例：

```sh
./dist/kasim apply \
  -f ./examples/signals/auxiliary-rdma-sriov.yaml \
  --dry-run=client \
  -o json
```

这两个辅助契约的上游资源名都可配置，因此必须由 Scenario 提供准确的完整扩展
资源名，而不是由目录猜测。完整拓扑见[场景示例](scenario-examples.md)。

## 复现浏览器与包体门禁

发布测试使用固定版本的 Playwright Chromium 和确定性的 1,001 Node fixture，验证
桌面与 390 px 布局、中英文、URL 历史、键盘与焦点、部分数据、同源请求、fragment
令牌隔离、无 JavaScript fallback，以及 100 行 DOM 上限。CI 会保留三张截图作为
视觉证据：

```sh
npm ci
npx playwright install chromium
npm run test:ui
```

`go test ./internal/ui` 会强制执行静态资源原始 256 KiB 和 gzip 96 KiB 上限。
证据门禁发布构建器还会逐平台与仅用于测量的无 UI 构建比较，压缩增量超过 1 MiB
即拒绝发布；具体数值写入 `release-receipt.json`。
