# Accelerator telemetry metric evidence

Checked: 2026-08-07

## Question and evidence rule

Kasim needs Prometheus-format telemetry for every Synthetic Node while preserving
the metric-family names used by the corresponding hardware vendor. Which metric
names, Prometheus types, units, labels, and availability constraints can be
implemented without inventing a vendor contract?

This note uses only first-party vendor documentation or source code and upstream
Prometheus source. A name is not enough to infer a Prometheus type or unit:
suffixes such as `_total`, `_bytes`, or `_ratio` are recorded as evidence only
when the source explicitly declares or emits the corresponding type/unit. A
profile being `verified` for scheduling does **not** verify its telemetry
contract. Sources without an immutable revision are marked accordingly.

The evidence supports three telemetry states:

- **verified**: public first-party evidence fixes the metric name and the facts
  Kasim plans to emit (at minimum type, with units/labels recorded where known);
- **provisional**: first-party evidence proves an exporter or metric name, but
  leaves type, labels, unit, release scope, or source revision incomplete;
- **unavailable**: no public first-party Prometheus contract was found. Kasim
  must not derive a metric name from a scheduler resource or another vendor.

## Implementable verified metric families

The table is deliberately a small, useful baseline rather than a transcription
of every exporter field. “Gauge” and “counter” below are the types actually
declared/emitted by the cited implementation.

| Profile | Exact metric family | Type | Unit/value contract | Native labels relevant to a Synthetic Node | Constraints |
| --- | --- | --- | --- | --- | --- |
| NVIDIA | `DCGM_FI_DEV_GPU_UTIL` | gauge | percent | `gpu`, `UUID`, `device`, `modelName`, `Hostname`; Kubernetes enrichment can add `container`, `namespace`, `pod` | DCGM field support depends on GPU and driver. |
| NVIDIA | `DCGM_FI_DEV_FB_USED`, `DCGM_FI_DEV_FB_FREE`, `DCGM_FI_DEV_FB_RESERVED` | gauge | MiB | same as above | Preserve `used + free + reserved <= model capacity`; some fields can be unsupported. |
| NVIDIA | `DCGM_FI_DEV_GPU_TEMP`, `DCGM_FI_DEV_MEMORY_TEMP` | gauge | Celsius | same as above | Memory temperature is not available on every product. |
| NVIDIA | `DCGM_FI_DEV_POWER_USAGE` | gauge | watts | same as above | Device-dependent. |
| NVIDIA | `DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION` | counter | millijoules | same as above | Must be monotonic within one simulated exporter epoch. |
| NVIDIA | `DCGM_FI_DEV_SM_CLOCK`, `DCGM_FI_DEV_MEM_CLOCK` | gauge | MHz | same as above | Device-dependent. |
| NVIDIA | `DCGM_FI_PROF_PCIE_RX_BYTES`, `DCGM_FI_PROF_PCIE_TX_BYTES` | gauge | bytes/second | same as above | Profiling fields have DCGM/device prerequisites. |
| AMD | `gpu_gfx_activity`, `gpu_umc_activity` | gauge | percent, 0–100 | `gpu_id`, `card_model`, `gpu_partition_id`, `gpu_compute_partition_type`, `gpu_memory_partition_type`, `deployment_mode`, `serial_number`, `hostname`; optional workload labels | The optional `MetricsFieldPrefix` changes the exposed name; the official ConfigMap example uses `amd_`. |
| AMD | `gpu_used_vram`, `gpu_total_vram`, `gpu_free_vram` | gauge | MB | same as above | Partition mode and SR-IOV can change which physical values are available. |
| AMD | `gpu_power_usage`, `gpu_package_power` | gauge | watts | same as above | Some fields are MI2xx/MI3xx-specific. |
| AMD | `gpu_temperature`, `gpu_junction_temperature`, `gpu_memory_temperature` | gauge | Celsius | same as above | Product-dependent. |
| AMD | `gpu_gfx_clock`, `gpu_memory_clock` | gauge | MHz | same as above | Product-dependent. |
| Intel GPU (XPU Manager) | `xpum_engine_ratio`, `xpum_engine_group_ratio` | gauge | ratio, exporter scales percentage by `0.01`; group label `type` identifies engine group | `uuid`, `dev_name`, `pci_dev`, `vendor`, `pci_bdf`; optional `dev_file`, `node`, `kube_pod`, `kube_namespace`, `kube_container`, `sub_dev`, `card` | Evidence is from the maintained `master` exporter source; pin before catalog import. |
| Intel GPU (XPU Manager) | `xpum_memory_used_bytes` | gauge | bytes | same as above | Device/tile scope comes from labels. |
| Intel GPU (XPU Manager) | `xpum_memory_ratio` | gauge | ratio | same as above | Same ratio scaling applies. |
| Intel GPU (XPU Manager) | `xpum_power_watts`, `xpum_temperature_celsius`, `xpum_frequency_mhz` | gauge | watts, Celsius, MHz | temperature `location`; frequency `location` and `type` | Product-dependent. |
| Intel GPU (XPU Manager) | `xpum_energy_joules` | counter | joules | base labels above | Monotonic within one simulated exporter epoch. |
| Intel GPU (XPU Manager) | `xpum_pcie_read_bytes`, `xpum_pcie_write_bytes`, `xpum_fabric_tx_bytes` | counter | bytes | base labels; fabric metrics include source/destination identity | Monotonic within one simulated exporter epoch. |
| Huawei Ascend | `npu_chip_info_utilization`, `npu_chip_info_overall_utilization`, `npu_chip_info_vector_utilization` | gauge | source describes utilization but does not fix an exposition unit in the collector | `id`, `model_name`, `vdie_id`, `pcie_bus_info`, `namespace`, `pod_name`, `container_name` | vNPU support differs; failed reads use vendor failure values. |
| Huawei Ascend | `npu_chip_info_temperature`, `npu_chip_info_power`, `npu_chip_info_aicore_current_freq` | gauge | frequency is MHz; temperature and power units are not fixed in the cited collector help | same as above | Device-dependent. |
| Huawei Ascend | `npu_chip_info_hbm_used_memory`, `npu_chip_info_hbm_total_memory`, `npu_chip_info_hbm_utilization`, `npu_chip_info_hbm_temperature`, `npu_chip_info_hbm_bandwidth_utilization` | gauge | collector help does not fix memory unit | same as above | HBM collector is restricted to supported Ascend 910-family devices; vNPU limitations apply. |
| Huawei Ascend | `container_npu_utilization`, `container_npu_total_memory`, `container_npu_used_memory` | gauge | percent and MB | same as above | Workload attribution must exist; do not fabricate pod identity. |
| Cambricon | `mlu_utilization` | gauge | percent | official Kubernetes configuration uses `driver`, `mcu`, `mlu`, `model`, `node`, `node_ip`, `sn`, `type`, `uuid`; some metrics include `vf` | Metric names, labels, and prefix are configuration-driven; official Kubernetes deployment uses prefix `mlu`. |
| Cambricon | `mlu_memory_used`, `mlu_memory_total` | gauge | bytes | same as above | Product-dependent. |
| Cambricon | `mlu_power_usage`, `mlu_temperature` | gauge | watts, Celsius | same as above | MLU370 cluster temperature is reported as unsupported/zero. |
| Iluvatar | `ix_gpu_utilization` | gauge | percent | `name`, `gpu`, `uuid`, `driver`, `ixml`, `serial`, `node_name`; Kubernetes enrichment adds `namespace`, `pod`, `container` | Metric list and labels are YAML-driven; unsupported fields are omitted. |
| Iluvatar | `ix_mem_total`, `ix_mem_used`, `ix_mem_free` | gauge | MiB | same as above | Preserve memory arithmetic. |
| Iluvatar | `ix_power_usage`, `ix_gpu_temperature`, `ix_mem_temperature`, `ix_fan_speed` | gauge | watts, Celsius, RPM | same as above | Product-dependent. |
| Iluvatar | `ix_gpu_clock`, `ix_mem_clock`, `ix_pcie_throughput` | gauge | MHz; PCIe throughput KB/s | same as above | Exporter delays briefly to obtain profiling metrics. |
| Enflame | `enflame_gcu_usage` | gauge | exporter help does not fix the unit | `host`, `minor_number`, `uuid`, `busid`, `slot`, `name`, `pod_name`, `pod_namespace`, `container_name` | `-1` means unsupported. |
| Enflame | `enflame_gcu_memory_used_bytes`, `enflame_gcu_memory_total_bytes` | gauge | bytes | same as above | Physical collectors emit no samples when virtual devices are present. |
| Enflame | `enflame_gcu_power_usage`, `enflame_gcu_temperatures` | gauge | units not fixed in cited help | same as above | `-1` means unsupported. |
| Enflame | `enflame_gcu_health` | gauge | `2` healthy, `1` unhealthy, `0` unknown | base labels plus `healthmsg` | Use only documented status values. |
| Furiosa | `furiosa_npu_alive`, `furiosa_npu_core_utilization` | gauge | alive state; utilization unit is not fixed in README | `arch`, `core`, `device`, `uuid`, `pci_bus_id`, `firmware_version`, `driver_version`; optional host/Kubernetes labels | Kubernetes labels require the kubelet PodResources API. |
| Furiosa | `furiosa_npu_hw_temperature`, `furiosa_npu_hw_power` | gauge | units not fixed in README; labels distinguish temperature/power sensor | same as above; `ambient`/`peak` temperature and `rms` power labels | Product-dependent. |
| Furiosa | `furiosa_npu_core_frequency` | gauge | MHz | same as above | Product-dependent. |
| Furiosa | `furiosa_npu_total_cycle_count`, `furiosa_npu_task_execution_cycle` | counter | cycles | same as above | Monotonic within one simulated exporter epoch. |
| Furiosa | `furiosa_npu_dram_total`, `furiosa_npu_dram_usage` | gauge | bytes | same as above | Preserve capacity bounds. |
| RDMA / InfiniBand | `node_infiniband_port_data_received_bytes_total`, `node_infiniband_port_data_transmitted_bytes_total` | counter | bytes | `device`, `port` | Upstream node_exporter reads Linux sysfs; absent optional counters are omitted. |
| RDMA / InfiniBand | `node_infiniband_port_packets_received_total`, `node_infiniband_port_packets_transmitted_total`, error/discard families | counter | packets/events | `device`, `port` | Available fields depend on the driver/device sysfs tree. |
| RDMA / InfiniBand | `node_infiniband_rate_bytes_per_second` | gauge | bytes/second | `device`, `port` | Represents link rate, not current traffic. |
| RDMA / InfiniBand | `node_infiniband_state_id`, `node_infiniband_physical_state_id` | gauge | numeric state ID | `device`, `port` | State identifiers must come from the documented/sysfs state domain. |
| RDMA / InfiniBand | `node_infiniband_info` | gauge | constant information value | `device`, `board_id`, `firmware_version`, `hca_type` | Linux only in node_exporter. |

### Primary sources for the verified table

- NVIDIA: [`default-counters.csv` at `181290c`](https://github.com/NVIDIA/dcgm-exporter/blob/181290c399d46a9b905e083d0204348be63cb436/etc/default-counters.csv) and the [official exporter README at the same revision](https://github.com/NVIDIA/dcgm-exporter/blob/181290c399d46a9b905e083d0204348be63cb436/README.md).
- AMD: the [official metric list](https://github.com/ROCm/device-metrics-exporter/blob/4642bb460926b531cefed17b5ad997be81b891f2/docs/configuration/metricslist.md), [Prometheus declarations](https://github.com/ROCm/device-metrics-exporter/blob/4642bb460926b531cefed17b5ad997be81b891f2/pkg/amdgpu/gpuagent/gpuagent_gpu_metrics.go), and [prefix/label configuration](https://github.com/ROCm/device-metrics-exporter/blob/4642bb460926b531cefed17b5ad997be81b891f2/docs/configuration/configmap.md).
- Intel GPU: XPU Manager's [metric catalog](https://github.com/intel/xpumanager/blob/57e44f558a3c3f4e7ec3cdfae6ccd8739ffb3be5/doc/Prometheus_Exported_Metrics.csv), [metric mapping and types](https://github.com/intel/xpumanager/blob/57e44f558a3c3f4e7ec3cdfae6ccd8739ffb3be5/rest/prometheus_exporter/prometheus_exporter_types.py), and [label/scaling implementation](https://github.com/intel/xpumanager/blob/57e44f558a3c3f4e7ec3cdfae6ccd8739ffb3be5/rest/prometheus_exporter/prometheus_exporter.py).
- Huawei Ascend: the pinned MindCluster collectors for [NPU](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/metrics/collector_for_npu.go), [HBM](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/metrics/collector_for_hbm.go), [RoCE](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/metrics/collector_for_roce.go), and the [common gauge emission path](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/common/metrics_collector.go).
- Cambricon: [official metric configuration](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/examples/metrics.yaml), [collector](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/pkg/collector/cndev.go), and [Prometheus emission code](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/pkg/metrics/metrics.go).
- Iluvatar: the official DeepSpark [`metrics.yaml` at `7f169d7`](https://gitee.com/deep-spark/ix-exporter/blob/7f169d7f1c0b66cc809ecba28f6d520e8f28ff2c/etc/metrics.yaml) and [gauge emission implementation](https://gitee.com/deep-spark/ix-exporter/blob/7f169d7f1c0b66cc809ecba28f6d520e8f28ff2c/pkg/collector/collector.go).
- Enflame: the official [namespace/typed emission code](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/collector.go), [usage collector](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_usage.go), and [repository README](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/README.md) at `0e6e15c`.
- Furiosa: the [official metric/type/label table at `e24b600`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/README.rst), plus the pinned [frequency](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/frequency.go) and [DRAM](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/memory.go) collectors for units.
- RDMA/InfiniBand: upstream Prometheus [node_exporter InfiniBand collector at `ac83e37`](https://github.com/prometheus/node_exporter/blob/ac83e377f04d53fd2683480337a0283d46204a33/collector/infiniband_linux.go). This is vendor-neutral host telemetry, not a Device Plugin metric contract.

## First-party names that remain provisional

These can be represented in the catalog for discovery, but must not be enabled
as a verified built-in simulation until their missing contract fields are
resolved.

| Profile | First-party evidence | What is known | Missing contract |
| --- | --- | --- | --- |
| Intel Gaudi | [Gaudi Prometheus Metric Exporter](https://docs.habana.ai/en/latest/Orchestration/Prometheus_Metric_Exporter.html) | `/metrics`, default port `41611`, recommended 30-second scrape; names include `habanalabs_utilization`, `habanalabs_memory_free_bytes`, `habanalabs_memory_used_bytes`, `habanalabs_memory_total_bytes`, `habanalabs_power_mW`, `habanalabs_temperature_onchip`, and `habanalabs_temperature_onboard` | Public table does not declare Prometheus types or a complete label schema; source revision is not pinned. |
| AWS Neuron | [neuron-monitor guide](https://github.com/aws-neuron/aws-neuron-sdk/blob/6bff96a45b2b0559ecf9fc470038ca54aee5c116/tools/neuron-sys-tools/neuron-monitor-user-guide.rst), [official Grafana dashboard](https://github.com/aws-neuron/aws-neuron-sdk/blob/6bff96a45b2b0559ecf9fc470038ca54aee5c116/src/examples/neuron-monitor/neuron-monitor-grafana.json), and [DaemonSet](https://github.com/aws-neuron/aws-neuron-sdk/blob/6bff96a45b2b0559ecf9fc470038ca54aee5c116/src/k8/k8s-neuron-monitor-daemonset.yml) | Dashboard queries prove names including `neuroncore_utilization_ratio`, `neuron_runtime_memory_used_bytes`, `neuron_runtime_vcpu_usage_ratio`, `execution_latency_seconds`, `execution_status_total`, and `instance_info`; labels include `neuroncore`, `memory_location`, `instance_id`, `percentile`, `usage_type`, and `status_type` | `neuron-monitor` emits JSON; a packaged companion converts it to Prometheus, but that converter is not in the public source and the dashboard does not declare metric types. |
| Google TPU | [Cloud TPU monitoring guide](https://docs.cloud.google.com/tpu/docs/troubleshooting/tpu-vm-monitoring), [Cloud Monitoring metric catalog](https://docs.cloud.google.com/monitoring/api/metrics_gcp_p_z), and [GKE PromQL example](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/machine-learning/inference/autoscaling-tpu) | Cloud Monitoring defines `accelerator/duty_cycle`, `accelerator/memory_total`, `accelerator/memory_used`, and `accelerator/memory_bandwidth_utilization` as gauges; official PromQL examples show `kubernetes_io:node_accelerator_memory_used` and `kubernetes_io:node_accelerator_memory_total` | This is provider telemetry, not a first-party TPU `/metrics` exporter. The Cloud Monitoring names containing `/` are not Prometheus exposition names, and no official Prometheus mapping for every family was found. Metrics also have runtime/PJRT and 60-second sampling constraints. |
| Moore Threads | [official Cloud Native install guide](https://docs.mthreads.com/en/cloud-native/cloud-native-doc-online/install_guide/) | Examples prove `DCGM_FI_DEV_GPU_UTIL`, `DCGM_FI_DEV_FB_TOTAL`, and `DCGM_FI_DEV_XID_ERRORS` as gauges and show labels including `gpu`, `UUID`, `device`, `modelName`, `Hostname`, driver/version labels, workload labels, and XID detail labels; endpoint port is `9400` | Public exporter source and a revisioned complete counter catalog were not found. Do not copy NVIDIA's entire DCGM list merely because some names overlap. |
| Graphcore | [official V-IPU Prometheus guide](https://docs.graphcore.ai/projects/vipu-admin/en/latest/vipu-prometheus.html) | V-IPU exposes OpenMetrics on port `2112`; documented names include `chassis_fan`, `chassis_power`, `chassis_temperature`, `exporter_ticks`, `gcipuinfo_clock_frequency`, `ipu_attached`, `ipu_link_err_cnt`, `ipu_tile_clk_speed`, and `ipum_hardware_info` | The per-machine table does not completely declare types/labels, and this legacy V-IPU/IPU-Machine scope is not proven equivalent to the current Kubernetes device-plugin profile. |
| MetaX | Official [mx-exporter installation](https://developer.metax-tech.com/api/client/document/preview/930/split_files/mx_exporter%E9%83%A8%E7%BD%B2.html), [metric-display guide](https://developer.metax-tech.com/api/client/document/preview/930/split_files/gpu%E6%80%A7%E8%83%BD%E6%8C%87%E6%A0%87%E5%B1%95%E7%A4%BA.html), and [troubleshooting page](https://developer.metax-tech.com/api/client/document/preview/930/split_files/%E5%B8%B8%E8%A7%81%E9%97%AE%E9%A2%98.html) | Confirms a per-node `/metrics` exporter, configurable `default-counters.csv`, a default 10-second collection interval, Kubernetes workload labels, and the exact example name `gpu_usage` | The accessible HTML does not publish the CSV contents, Prometheus type, unit, or complete label schema. The package/download is not a public revisioned source. |

## Catalog coverage with no safe native metric mapping

The following current Kasim profiles were checked, but no public first-party,
revisionable Prometheus metric contract with exact names and types was found:

| Profile | Result for this iteration |
| --- | --- |
| Biren | unavailable; the [official Device Plugin repository](https://gitee.com/BirenTechnology/k8s-device-plugin/tree/a9984054f975d3430c61cd1f068691b7137da9a6) does not expose a hardware Prometheus exporter contract. |
| Hygon DCU | unavailable; the [official ecosystem](https://developer.sourcefind.cn/servicelist) advertises `dcu-exporter`, but no accessible, revisioned first-party metric-name/type catalog was found. |
| Kunlunxin (HAMi integration) | unavailable; the [HAMi scheduling integration](https://github.com/Project-HAMi/HAMi/tree/e831337db299f331b170a46d6ca3dba256b9d6f1) is not evidence of a Kunlunxin-native telemetry namespace. |
| Vastai (HAMi integration) | unavailable; the [HAMi scheduling integration](https://github.com/Project-HAMi/HAMi/tree/e831337db299f331b170a46d6ca3dba256b9d6f1) is not evidence of a Vastai-native telemetry namespace. |
| Qualcomm Cloud AI 100 | unavailable; the [official Kubernetes deployment documentation](https://quic.github.io/cloud-ai-sdk-pages/1.20/Getting-Started/Installation/Docker/k8s/index.html) does not define a Prometheus exporter contract with exact metric names/types. |
| SR-IOV Network Device Plugin | unavailable as a device-pool telemetry contract; the [official Device Plugin](https://github.com/k8snetworkplumbingwg/sriov-network-device-plugin/blob/efe22f8722ceae918c6703830107b3e82b089ef1/README.md) exposes allocatable resources, not per-VF hardware metrics. Generic host/network metrics must not be presented as the plugin's native names. |

“Unavailable” is a safe implementation result, not a claim that no proprietary
exporter exists. A later version can promote one of these profiles after adding
first-party, revision-pinned evidence to the telemetry catalog.

## Findings that constrain implementation

1. **Vendor coverage is data, not code branches.** Exporters vary in names,
   labels, types, prefixes, unsupported sentinels, and product gates. These facts
   belong in revisioned telemetry records; a per-vendor Go adapter would encode
   evidence as control flow and make promotion hard to audit.
2. **Never randomize on scrape.** Real exporters sample on an interval. A
   simulated gauge should stay constant within a time bucket and evolve at the
   next tick; counters must remain monotonic within an exporter epoch. This also
   makes tests and incident reproduction deterministic.
3. **Values must be coupled.** Memory used/free/total, utilization, power,
   temperature, clocks, traffic, and energy are not independent random numbers.
   The generator needs shared per-device latent state, capacity bounds, and
   counter integration.
4. **Unsupported is part of the contract.** Depending on the cited exporter,
   an unsupported field may be omitted, use a documented sentinel, or be
   restricted to particular products/partition modes. Kasim must record that
   policy per family instead of emitting a plausible-looking zero.
5. **Names can collide.** NVIDIA and Moore Threads intentionally expose some
   identical DCGM-family names, while AMD and Cambricon allow configured
   prefixes. An aggregate endpoint must reject incompatible `TYPE`/`HELP`
   definitions for one family. Kasim therefore validates the catalog globally
   and fails closed before serving its aggregate scrape surface; a per-node
   endpoint remains an alternative if future verified contracts conflict.
6. **Synthetic telemetry must remain visibly synthetic.** Native metric names
   improve dashboard compatibility, but they do not mean the original exporter
   or hardware ran. Kasim-owned metadata/receipts must state Synthetic Node,
   Scenario Instance, telemetry class, evidence revision, seed, and generation
   interval without renaming the native metric families.

## Recommended first release boundary

Ship verified, device-level baselines for NVIDIA, AMD, Intel GPU, Huawei Ascend,
Cambricon, Iluvatar, Enflame, Furiosa, and RDMA/InfiniBand. Keep Intel Gaudi,
AWS Neuron, Google TPU, Moore Threads, Graphcore, and MetaX discoverable but
disabled unless the user explicitly opts into provisional telemetry. Report all
remaining catalog profiles as unavailable. Workload-level labels should remain
empty unless an actual bound Pod can be attributed from the cluster; no
synthetic process or Pod label should be invented merely to make a dashboard
look busy.
