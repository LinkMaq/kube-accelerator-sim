# 模拟厂商 Prometheus 遥测

Kasim 通过一个只读的集群内端点，为每个准确归属的 Synthetic Node 和模拟设备
提供有来源依据的 Prometheus 指标结构。指标名可以直接用于适配常见看板，但数值
由 Kasim 生成，并不是物理板卡、驱动或原厂 exporter 的测量结果。

## 启动与抓取

运行时 Chart 默认启用遥测，不需要修改 Scenario，也不会增加新的 `kasim` 生命周期命令：

```sh
helm upgrade --install kasim-runtime \
  oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.4.1 \
  --namespace kasim-system \
  --create-namespace

kubectl -n kasim-system port-forward \
  service/kasim-runtime-kasim-runtime-telemetry 9400:9400

curl --fail http://127.0.0.1:9400/metrics
```

ClusterIP Service 默认携带 Prometheus 抓取注解。如果集群安装了 Prometheus
Operator CRD，可以改用 ServiceMonitor；两种发现方式不要同时使用，以免重复抓取：

```sh
helm upgrade --install kasim-runtime \
  oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.4.1 \
  --namespace kasim-system \
  --set telemetry.serviceMonitor.enabled=true
```

如果集群没有 `monitoring.coreos.com/v1/ServiceMonitor`，该选项会明确失败。若完全
由外部抓取配置管理发现，请设置 `telemetry.service.prometheusScrape=false`，并保持
ServiceMonitor 关闭。

## 一条指标代表什么

H200 场景可能生成如下 NVIDIA 原生 family：

```text
DCGM_FI_DEV_GPU_UTIL{Hostname="kasim-node-...",gpu="0",UUID="kasim-...",device="kasim0",modelName="nvidia-h200",node="kasim-node-...",kasim_instance="h200-lab",kasim_node="kasim-node-...",kasim_pool="accelerators",kasim_profile="nvidia",kasim_model="nvidia-h200",kasim_device="kasim-...",kasim_simulated="true"} 72.4
```

原始 family 名和原生 label key 来自固定版本的 exporter 证据。额外的 `kasim_*`
标签是刻意保留的来源标识，让查询和告警始终能够证明数据是模拟值；
`kasim_value_model="correlated-v1"` 标识所用数值模型。每条设备指标还会携带兼容标签
`node=<Synthetic Node>`；它始终与 `kasim_node` 一致，不会使用承载集中式 telemetry Pod
的真实节点，因此 PromQL 不需要通过 `kube_pod_info` 推导设备归属。Synthetic Node 是
聚合端点中的 series 维度；Kasim 不会为每个节点伪造 exporter Pod 或 Service。

每个归属节点都会生成 `kasim_telemetry_node_info`，每个设备都会生成
`kasim_telemetry_device_contract_available`。证据不足的档案返回 `0`，而不是编造
厂商 family。目录、数据源和渲染状态可通过以下指标观察：

- `kasim_telemetry_catalog_info`；
- `kasim_telemetry_contract_available`；
- `kasim_telemetry_source_up`；
- `kasim_telemetry_render_errors_total`。

## 数值行为

Kasim 每 15 秒生成一次不可变快照。同一时间桶内重复抓取数值完全一致，进程重启
也不会改变该时间桶。稳定设备身份和时间桶共同产生一个关联负载状态：

- 利用率在契约范围内平滑变化；
- 已用和空闲显存保持非负，且不超过型号模拟边界；
- 功耗、温度、时钟和流量跟随同一负载，不会各自独立乱跳；
- 不健康模拟单元降低活动，只使用已有证据定义的健康状态；
- counter 从明确的模拟器 epoch 开始单调增长。

这些曲线适合验证 Prometheus 采集、看板、告警规则和平台适配，不可用于板卡选型、
性能评测、温控或功耗规划、硬件故障诊断及性能对比。

## 自 v0.4.0 起的覆盖范围

| 状态 | 档案 |
| --- | --- |
| 已启用原生 family | NVIDIA DCGM、AMD Device Metrics Exporter、Intel XPU Manager、Huawei Ascend npu-exporter、Cambricon mlu-exporter、Iluvatar ix-exporter、Enflame gcu-exporter、Furiosa metrics exporter、Prometheus node_exporter InfiniBand collector |
| 可发现但因 provisional 暂不启用 | Intel Gaudi、AWS Neuron、Google TPU provider telemetry、Moore Threads、Graphcore、MetaX |
| 明确 unavailable | Biren、Hygon DCU、Kunlunxin through HAMi、Vastai through HAMi、Qualcomm Cloud AI 100、SR-IOV Device Plugin 原生遥测 |

覆盖范围只由证据决定。支持调度资源并不等于支持遥测。精确指标名、类型、单位、
原生标签、来源版本和产品限制见[加速器遥测研究](../../research/accelerator-telemetry-metrics.md)。

## 配置与健康状态

```yaml
telemetry:
  enabled: true
  refreshInterval: 15s
  staleAfter: 45s
  service:
    port: 9400
    prometheusScrape: true
    annotations: {}
  serviceMonitor:
    enabled: false
    interval: 15s
    scrapeTimeout: 10s
    labels: {}
```

`/healthz` 表示进程存活；只有 Kubernetes 观察成功且渲染有效时，`/readyz` 才会
成功。Prometheus 抓取不会访问 Kubernetes API，只读取不可变编码缓冲区。刷新失败
后，最后一次成功缓冲区最多保留到 `staleAfter`；超时后会移除原生 series、让就绪
探针失败，并只保留 Kasim 自身诊断指标。

telemetry ServiceAccount 只能对 Scenario Instance 和 Node 执行 `get/list/watch`，
没有任何集群写权限；Pod 通过硬亲和规则只能运行在真实 Node 上。单个快照最多支持
1,000 个 Synthetic Node 和 8,000 个模拟设备。

## 故障排查

分别检查部署、就绪和指标三层：

```sh
kubectl -n kasim-system get deploy,pod,service \
  -l app.kubernetes.io/component=telemetry

kubectl -n kasim-system get --raw \
  /api/v1/namespaces/kasim-system/services/http:kasim-runtime-kasim-runtime-telemetry:9400/proxy/readyz

kubectl -n kasim-system get --raw \
  /api/v1/namespaces/kasim-system/services/http:kasim-runtime-kasim-runtime-telemetry:9400/proxy/metrics
```

如果节点只有 `kasim_telemetry_node_info`，请检查该档案的契约状态；`provisional`
或 `unavailable` 是证据结论，不是运行故障。如果 `/readyz` 失败，请查看 telemetry
Pod 日志，并确认 ClusterRole 可以读取 `scenarioinstances.simulation.kasim.io` 和
Kasim 归属的 Node。
