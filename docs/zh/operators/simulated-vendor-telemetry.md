# 模拟厂商 Prometheus 遥测

Kasim 通过一个只读的集群内端点，为每个准确归属的 Synthetic Node 和模拟设备
提供有来源依据的 Prometheus 指标结构。指标名可以直接用于适配常见看板，但数值
由 Kasim 生成，并不是物理板卡、驱动或原厂 exporter 的测量结果。

## 启动与抓取

运行时 Chart 默认启用遥测，不需要修改 Scenario，也不会增加新的 `kasim` 生命周期命令：

```sh
helm upgrade --install kasim-runtime \
  oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.5.1 \
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
  --version 0.5.1 \
  --namespace kasim-system \
  --set telemetry.serviceMonitor.enabled=true
```

如果集群没有 `monitoring.coreos.com/v1/ServiceMonitor`，该选项会明确失败。若完全
由外部抓取配置管理发现，请设置 `telemetry.service.prometheusScrape=false`，并保持
ServiceMonitor 关闭。
启用 ServiceMonitor 后，其 metric relabeling 会删除目标标签 `namespace`、`pod` 和
`container`，避免集中式 `kasim-system` telemetry Pod 被误识别为业务工作负载。

## 一条指标代表什么

H200 场景可能生成如下 NVIDIA 原生 family：

```text
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-...",pci_bus_id="00000000:af:00.0",device="nvidia0",modelName="nvidia-h200",Hostname="kasim-node-...",DCGM_FI_DRIVER_VERSION="580.126.16",node="kasim-node-..."} 72.4
```

family 名、`TYPE` 和原生 label 来自所选 exporter 契约；除明确记录的 Kasim 兼容
数值约定外，`HELP` 也保留来源定义。Kasim 为每个逐设备 family 增加唯一的兼容标签
`node`，其值始终是该指标描述的 Synthetic Node。DCGM
`Hostname`、AMD `hostname`、Cambricon `node`、Iluvatar `node_name`、Enflame
`host` 等原生归属标签继续保留，且同样指向 Synthetic Node；厂商 family 不会携带
`kasim_*`。单 family 专属 label 也会保留，例如 `DCGM_FI_DEV_XID_ERRORS` 额外带
`err_code` 和 `err_msg`。

每个 Synthetic Node 都带
`feature.node.cloud.xiaoshiai.cn/accelerator-model.name=<catalog-model-id>`。
Telemetry Catalog 绑定的 exporter 型号标签使用同一个 catalog model ID，因此 NVIDIA
`modelName` 与节点标签逐字节一致。同一 Node Group 的多个池可以共享同一型号；若包含
不同加速器型号则明确拒绝，因为节点标签只能有一个值。同一张设备的所有指标始终使用
相同的 `gpu` 和 `UUID`。

不要把承载集中式 telemetry Pod 的真实节点写进 `node`，也不要通过 `kube_pod_info`
覆盖它。Synthetic Node 仍是聚合端点中的 series 维度；Kasim 不会为每个节点伪造
exporter Pod 或 Service。

每个归属节点都会生成 `kasim_telemetry_node_info`，每个设备都会生成
`kasim_telemetry_device_contract_available`。证据不足的档案返回 `0`，而不是编造
厂商 family。目录、数据源和渲染状态可通过以下指标观察：

- `kasim_telemetry_catalog_info`；
- `kasim_telemetry_contract_available`；
- `kasim_telemetry_source_up`；
- `kasim_telemetry_render_errors_total`。

这些独立的 `kasim_telemetry_*` family 承担 Kasim 来源标识，因此无需污染厂商原生
family。抓取目标配置也应明确标识 Kasim telemetry Service。

## 数值行为

Kasim 每 15 秒生成一次不可变快照。同一时间桶内重复抓取数值完全一致，进程重启
也不会改变该时间桶。稳定设备身份和时间桶共同产生一个关联负载状态：

- 利用率在闭区间 `0`～`100` 内平滑变化；需要比例值的消费者再转换成 `0`～`1`；
- 已用、空闲以及 exporter 定义的预留显存保持非负，且不超过型号模拟边界；
- 功耗、温度、时钟和流量跟随同一负载，不会各自独立乱跳；
- 健康类数值使用 `0` 表示正常、非零表示故障；不健康模拟单元同时降低活动；
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
ServiceMonitor 模式会在抓取后删除 `namespace`、`pod` 和 `container`；若使用注解
抓取或外部抓取配置，应应用同样的 relabeling，或在后端把 `kasim-system` telemetry
目标识别为基础设施组件。

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
