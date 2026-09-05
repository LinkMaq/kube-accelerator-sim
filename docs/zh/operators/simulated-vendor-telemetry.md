# 模拟厂商 Prometheus 遥测

Kasim 通过一个只读的集群内端点，为每个准确归属的 Synthetic Node 和模拟设备
提供有来源依据的 Prometheus 指标结构。指标名可以直接用于适配常见看板，但数值
由 Kasim 生成，并不是物理板卡、驱动或原厂 exporter 的测量结果。

## 启动与抓取

运行时 Chart 默认启用遥测，不需要修改 Scenario，也不会增加新的 `kasim` 生命周期命令：

```sh
helm upgrade --install kasim-runtime \
  oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.5.3 \
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
  --version 0.5.3 \
  --namespace kasim-system \
  --set telemetry.serviceMonitor.enabled=true
```

如果集群没有 `monitoring.coreos.com/v1/ServiceMonitor`，该选项会明确失败。若完全
由外部抓取配置管理发现，请设置 `telemetry.service.prometheusScrape=false`，并保持
ServiceMonitor 关闭。
启用 ServiceMonitor 后，其 metric relabeling 会保留目标标签 `namespace`、`pod`，
并删除 `container`。保留前两个标签后，现有清单查询可以通过
`on(namespace, pod)` 将设备 series 与抓取目标元数据关联。它们只表示唯一的集中式
`kasim-system` telemetry Pod，不表示 Synthetic Node，也不表示拥有模拟设备的业务
工作负载。后端应把该目标识别为基础设施组件，并使用 `node` 及 exporter 原生节点
身份标签判断设备归属。

## 一条指标代表什么

H200 场景可能生成如下 NVIDIA 原生 family：

```text
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-...",pci_bus_id="00000000:af:00.0",device="nvidia0",modelName="nvidia-h200",Hostname="kasim-node-...",DCGM_FI_DRIVER_VERSION="580.126.16",node="kasim-node-...",model="nvidia-h200",uuid="...",vendor="NVIDIA"} 72.4
```

family 名、`TYPE` 和原生 label 来自所选 exporter 契约；除明确记录的 Kasim 兼容
数值约定外，`HELP` 也保留来源定义。Kasim 为每个逐设备 family 增加稳定身份层：
`node`、`device`、`model`、`uuid` 和 `vendor`。若 exporter 已定义同名 label，则保留
其原生值；否则分别绑定 Synthetic Node、设备序号、catalog 型号、确定性设备 UUID
和档案显示名。DCGM
`Hostname`、AMD `hostname`、Cambricon `node`、Iluvatar `node_name`、Enflame
`host` 等原生归属标签继续保留，且同样指向 Synthetic Node；厂商 family 不会携带
`kasim_*`。单 family 专属 label 也会保留，例如 `DCGM_FI_DEV_XID_ERRORS` 额外带
`err_code` 和 `err_msg`。

NVIDIA DCGM 契约锚定到 dcgm-exporter 4.6.0～4.8.3 版本线。该版本线内，上游将
NVLink 带宽 family 从 counter 修正为 gauge（#658）。自遥测目录修订
`2026-09-05.1` 起，`DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` 因此导出
`TYPE = GAUGE`，与锚定契约一致，而非旧版 counter 类型的抓取样本。

每个 Synthetic Node 都带
`feature.node.cloud.xiaoshiai.cn/accelerator-model.name=<catalog-model-id>`。
Telemetry Catalog 绑定的 exporter 型号标签使用同一个 catalog model ID，因此 NVIDIA
`modelName` 与节点标签逐字节一致。同一 Node Group 的多个池可以共享同一型号；若包含
不同加速器型号则明确拒绝，因为节点标签只能有一个值。同一张设备的全部指标及进程
重启前后，五个身份 label 与 `gpu`、`id`、`minor_number`、`device_id` 等原生选择器
始终一致。

不要把承载集中式 telemetry Pod 的真实节点写进 `node`，也不要通过 `kube_pod_info`
覆盖它。Synthetic Node 仍是聚合端点中的 series 维度；Kasim 不会为每个节点伪造
exporter Pod 或 Service。

使用 ServiceMonitor 发现时，下游清单查询可以通过 `on(namespace, pod)` 关联抓取
目标元数据，同时不改变设备归属。这类 join 只能识别 Kasim telemetry 抓取目标；
它不能证明该 Pod 拥有、预留或正在使用这些设备。

## 华为昇腾与海光 DCU 契约

华为契约按设备分别生成 series。基础 family 覆盖 AI Core 使用率、温度、功耗、
HBM 已用量与总量、HBM 使用率、健康状态和错误码。自目录修订 `2026-09-05.1`
起，新增 25 个 npu-exporter family，全部锚定到 MindCluster `v26.1.0` 来源
（见[厂商档案证据](profile-evidence.md)）：

| 含义 | Family | 单位或约定 |
| --- | --- | --- |
| 整卡 / 向量使用率 | `npu_chip_info_overall_utilization`、`npu_chip_info_vector_utilization` | percent，`0`～`100` |
| 电压 | `npu_chip_info_voltage` | 伏特，生成区间 `0.78`～`1.00` |
| DDR 显存 | `npu_chip_info_total_memory`、`npu_chip_info_used_memory` | MB |
| HBM 温度 / 带宽使用率 | `npu_chip_info_hbm_temperature`、`npu_chip_info_hbm_bandwidth_utilization` | 摄氏度 / percent |
| HBM ECC | `npu_chip_info_hbm_ecc_enable_flag`、`npu_chip_info_hbm_ecc_single_bit_error_cnt`、`npu_chip_info_hbm_ecc_double_bit_error_cnt`、`npu_chip_info_hbm_ecc_total_single_bit_error_cnt`、`npu_chip_info_hbm_ecc_total_double_bit_error_cnt`、`npu_chip_info_hbm_ecc_single_bit_isolated_pages_cnt`、`npu_chip_info_hbm_ecc_double_bit_isolated_pages_cnt` | 状态位 / 错误计数 |
| 网络 / 链路状态 | `npu_chip_info_network_status`、`npu_chip_info_link_status` | `1=正常`，`0=异常` |
| RoCE 带宽 | `npu_chip_info_bandwidth_rx`、`npu_chip_info_bandwidth_tx` | MB/s |
| PCIe 带宽 | `npu_chip_info_pcie_rx_p_bw`、`npu_chip_info_pcie_rx_np_bw`、`npu_chip_info_pcie_rx_cpl_bw`、`npu_chip_info_pcie_tx_p_bw`、`npu_chip_info_pcie_tx_np_bw`、`npu_chip_info_pcie_tx_cpl_bw` | MB/ms |
| 芯片标识 | `npu_chip_info_name` | 按 exporter 原生约定恒为 `1` |

原生设备 label 为 `id`、`model_name`、`vdie_id`、
`pcie_bus_info`、`namespace`、`pod_name` 和 `container_name`。兼容层 `uuid`
是稳定的模拟设备身份，原生 `vdie_id` 保持 exporter 兼容的虚拟 die 值；三个工作
负载 label 保持空值，因为集中式 telemetry Pod 不是模拟业务负载。
`npu_chip_info_health_status` 遵循原生约定：`1=健康`、`0=故障`；
`npu_chip_info_error_code` 在健康时为零、故障时为非零。

海光契约遵循 DCU-Exporter 的名称和 gauge 类型：

| 含义 | Family | 单位 |
| --- | --- | --- |
| 核心使用率 | `dcu_utilizationrate` | percent |
| 显存已用量 | `dcu_usedmemory_bytes` | bytes |
| 显存总量 | `dcu_memorycap_bytes` | bytes |
| 显存剩余量 | `dcu_memory_remaining` | bytes |
| 实时功耗 | `dcu_power_usage` | watts |
| 温度 | `dcu_temp` | Celsius |
| 可恢复错误 | `dcu_ce_count` | count |
| 不可恢复错误 | `dcu_ue_count` | count |

海光原生 label 为 `device_id`、`minor_number`、`name`、`node`、
`pcieBus_number`、`dcu_pod_namespace`、`dcu_pod_name` 和 `container`，错误 family
还包含 `block_type`。`device_id`、`pcieBus_number`、身份层及 `0` 到 `N-1` 的设备
序号均为确定值，工作负载 label 保持空值。物理设备 exporter 并未提供独立的
`dcu_health` family 或物理设备显存百分比 family，因此必须保留原始契约：由监控规则
或消费端根据可恢复/不可恢复错误信号判断健康状态，并按
`dcu_usedmemory_bytes / dcu_memorycap_bytes * 100` 计算显存使用率。

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
也不会改变该时间桶。稳定设备身份和时间桶共同产生一个关联负载状态。八卡池中的设备
序号会确定性覆盖空闲、持续任务、显存高负载和突发任务，`kasim health` 控制故障与恢复：

- 利用率在闭区间 `0`～`100` 内平滑变化；需要比例值的消费者再转换成 `0`～`1`；
- 已用、空闲以及 exporter 定义的预留显存保持非负，且不超过型号模拟边界；
- 功耗、温度、时钟和流量跟随同一负载，不会各自独立乱跳；
- 健康值保留厂商约定；正常设备的错误信号为零、故障设备为非零，不健康设备同时降低活动；
- counter 从明确的模拟器 epoch 开始单调增长。

这些曲线适合验证 Prometheus 采集、看板、告警规则和平台适配，不可用于板卡选型、
性能评测、温控或功耗规划、硬件故障诊断及性能对比。

## 自 v0.4.0 起的覆盖范围

| 状态 | 档案 |
| --- | --- |
| 已启用原生 family | NVIDIA DCGM、AMD Device Metrics Exporter、Intel XPU Manager、Huawei Ascend npu-exporter、Hygon DCU-Exporter、Cambricon mlu-exporter、Iluvatar ix-exporter、Enflame gcu-exporter、Furiosa metrics exporter、Prometheus node_exporter InfiniBand collector |
| 可发现但因 provisional 暂不启用 | Intel Gaudi、AWS Neuron、Google TPU provider telemetry、Moore Threads、Graphcore、MetaX |
| 明确 unavailable | Biren、Kunlunxin through HAMi、Vastai through HAMi、Qualcomm Cloud AI 100、SR-IOV Device Plugin 原生遥测 |

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
ServiceMonitor 模式会在抓取后保留 `namespace`、`pod` 并删除 `container`。若通过
注解或外部配置抓取，且查询需要关联目标元数据，应保留等价的目标身份。无论采用哪种
交付方式，后端都应把 `kasim-system` telemetry 目标识别为基础设施组件，不能根据其
Pod 标签推断设备归属。

## 验收查询

对于一台八卡华为节点，以下即时查询均应返回 `8`：

```promql
count(npu_chip_info_utilization)
count(npu_chip_info_hbm_used_memory)
count(count by (node, device, uuid) (npu_chip_info_health_status))
```

对于一台八卡海光节点，使用：

```promql
count(dcu_utilizationrate)
count(dcu_memorycap_bytes)
count(count by (node, device, uuid) (dcu_ue_count))
```

至少完成两次抓取后，以下范围查询的每个结果都应大于一：

```promql
count_over_time(npu_chip_info_utilization[5m])
count_over_time(dcu_utilizationrate[5m])
```

使用 `count(count by (uuid) (<family>))` 检查 UUID 唯一性，并与 Node 容量对比；对每个
family 按 `(node, device, model, uuid, vendor)` 分组检查身份一致性。华为和海光不应匹配
`{__name__=~"DCGM_FI_DEV_.*"}`。

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
