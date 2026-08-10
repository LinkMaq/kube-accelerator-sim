# Accelerator telemetry metric evidence

Checked: 2026-08-10

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
| NVIDIA | `DCGM_FI_DEV_GPU_UTIL`, `DCGM_FI_DEV_MEM_COPY_UTIL`, `DCGM_FI_DEV_ENC_UTIL`, `DCGM_FI_DEV_DEC_UTIL` | gauge | percent | pinned exporter: `gpu`, `UUID`, `pci_bus_id`, `device`, `modelName`, optional lowercase `hostname`; the supplied compatibility sample instead has uppercase `Hostname` and `DCGM_FI_DRIVER_VERSION` | DCGM field support depends on GPU and driver. |
| NVIDIA | `DCGM_FI_DEV_FB_USED`, `DCGM_FI_DEV_FB_FREE`, `DCGM_FI_DEV_FB_RESERVED` | gauge | MiB | same as above | Preserve `used + free + reserved <= model capacity`; some fields can be unsupported. |
| NVIDIA | `DCGM_FI_DEV_GPU_TEMP`, `DCGM_FI_DEV_MEMORY_TEMP` | gauge | Celsius | same as above | Memory temperature is not available on every product. |
| NVIDIA | `DCGM_FI_DEV_POWER_USAGE` | gauge | watts | same as above | Device-dependent. |
| NVIDIA | `DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION` | counter | millijoules | same as above | Must be monotonic within one simulated exporter epoch. |
| NVIDIA | `DCGM_FI_DEV_SM_CLOCK`, `DCGM_FI_DEV_MEM_CLOCK` | gauge | MHz | same as above | Device-dependent. |
| NVIDIA | `DCGM_FI_DEV_PCIE_REPLAY_COUNTER`, row-remap counters, `DCGM_FI_DEV_XID_ERRORS`, `DCGM_FI_DEV_ROW_REMAP_FAILURE`, `DCGM_FI_DEV_VGPU_LICENSE_STATUS` | counter or gauge as declared below | counts/codes/states | same core labels; the supplied sample adds `err_code` and `err_msg` only to XID | Some fields are unsupported on some products. |
| NVIDIA | `DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` | gauge in pinned first-party CSV; counter in supplied runtime sample | vendor counter aggregate | same as above | The two evidence sources conflict; this family is not first-party exact until one contract is selected. |
| AMD | `gpu_gfx_activity`, `gpu_umc_activity` | gauge | percent, 0–100 | `gpu_id`, `card_model`, `gpu_partition_id`, `gpu_compute_partition_type`, `gpu_memory_partition_type`, `deployment_mode`, `serial_number`, `hostname`; optional workload labels | The optional `MetricsFieldPrefix` changes the exposed name; the official ConfigMap example uses `amd_`. |
| AMD | `gpu_used_vram`, `gpu_total_vram`, `gpu_free_vram` | gauge | MB | same as above | Partition mode and SR-IOV can change which physical values are available. |
| AMD | `gpu_power_usage` | gauge | watts | same as above | Product-dependent. |
| AMD | `gpu_edge_temperature` | gauge | Celsius | same as above | Product-dependent. |
| AMD | `gpu_clock` | gauge | MHz | same as above, plus `clock_index`, `clock_type` | Source-supported `clock_type` values include lowercase `data`, `system`, `memory`, `video`, and `soc`. |
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
| Iluvatar | `ix_sm_clock`, `ix_mem_clock` | gauge | MHz | same as above | Product-dependent. |
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

## Exact exposition audit of enabled verified contracts

This section audits every metric currently enabled by a `state: verified`
record in `telemetryprofiles/catalog.json`. It is stricter than the baseline
table above: a contract is exact only when its pinned first-party source proves
the family name, Prometheus `TYPE`, `HELP`, and applicable native label keys.

The `v1alpha2` catalog now stores `help`. This audit checks that each stored
sentence is the exact exposition text from its pinned source rather than a
Kasim-authored description. If exact text is not available, omit `HELP` or mark
the family provisional; do not construct vendor-sounding prose from `semantic`
and `unit`. A supplied runtime scrape can define an intentional compatibility
overlay, but it must be identified separately when it conflicts with the
pinned first-party implementation.

### Audit summary

| Profile currently marked verified | Exact result at the catalog revision | Recommendation |
| --- | --- | --- |
| NVIDIA | Nineteen of 20 catalog families match the pinned CSV's names, types, and HELP. `DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` conflicts: pinned first-party type is gauge, supplied sample type is counter. Core labels also mix the sample's uppercase `Hostname` and XID detail labels with a newer pinned renderer. | Treat the sample-only differences as an explicit compatibility overlay, or change NVLink to gauge and the schema to pinned-source labels before calling the whole profile first-party exact. |
| AMD | All eight catalog names, gauge types, HELP strings, label keys, and selected `clock_type="system"` value match the pinned source. | May remain verified. |
| Intel GPU | All seven names, types, HELP strings, required selected-device label keys, and `src="direct"` match the pinned source; additional labels remain conditional. | May remain verified for the selected device-level shape. |
| Huawei Ascend | All four families, gauge type, HELP strings, and seven labels are exact. | May remain verified. |
| Cambricon | All five families, gauge type, HELP strings, and family-specific `vf` labels are exact under the official `mlu` prefix. | May remain verified. |
| Iluvatar | All eight families, gauge types, HELP strings, and base labels are exact; Kubernetes workload labels are conditional. | May remain verified for the base schema. |
| Enflame | All six names, gauge types, HELP strings, and the health-specific `healthmsg` label are exact. | May remain verified. |
| Furiosa | All six families, types, HELP strings, required labels, and evidence links are exact. | May remain verified. |
| RDMA / InfiniBand | All seven families, types, HELP strings, and labels are exact. | May remain verified. |

### NVIDIA DCGM Exporter

Pinned first-party evidence: [`default-counters.csv` at `181290c`](https://github.com/NVIDIA/dcgm-exporter/blob/181290c399d46a9b905e083d0204348be63cb436/etc/default-counters.csv), the [CSV parser](https://github.com/NVIDIA/dcgm-exporter/blob/181290c399d46a9b905e083d0204348be63cb436/internal/pkg/counters/counter_config.go#L168-L187), and the [Prometheus renderer](https://github.com/NVIDIA/dcgm-exporter/blob/181290c399d46a9b905e083d0204348be63cb436/internal/pkg/rendermetrics/render_metrics.go#L192-L227).

| Family | Pinned first-party TYPE | Exact first-party HELP | Catalog/sample verdict |
| --- | --- | --- | --- |
| `DCGM_FI_DEV_SM_CLOCK` | gauge | `SM clock frequency (in MHz).` | exact |
| `DCGM_FI_DEV_MEM_CLOCK` | gauge | `Memory clock frequency (in MHz).` | exact |
| `DCGM_FI_DEV_MEMORY_TEMP` | gauge | `Memory temperature (in C).` | exact |
| `DCGM_FI_DEV_GPU_TEMP` | gauge | `GPU temperature (in C).` | exact |
| `DCGM_FI_DEV_POWER_USAGE` | gauge | `Power draw (in W).` | exact |
| `DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION` | counter | `Total energy consumption since boot (in mJ).` | exact |
| `DCGM_FI_DEV_PCIE_REPLAY_COUNTER` | counter | `Total number of PCIe retries.` | exact |
| `DCGM_FI_DEV_GPU_UTIL` | gauge | `GPU utilization (in %).` | exact |
| `DCGM_FI_DEV_MEM_COPY_UTIL` | gauge | `Memory utilization (in %).` | exact |
| `DCGM_FI_DEV_ENC_UTIL` | gauge | `Encoder utilization (in %).` | exact |
| `DCGM_FI_DEV_DEC_UTIL` | gauge | `Decoder utilization (in %).` | exact |
| `DCGM_FI_DEV_XID_ERRORS` | gauge | `Value of the last XID error encountered.` | name/type/HELP exact; `err_code` and `err_msg` are sample-only labels |
| `DCGM_FI_DEV_FB_FREE` | gauge | `Framebuffer memory free (in MiB).` | exact |
| `DCGM_FI_DEV_FB_USED` | gauge | `Framebuffer memory used (in MiB).` | exact |
| `DCGM_FI_DEV_FB_RESERVED` | gauge | `Framebuffer memory reserved (in MiB).` | exact |
| `DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS` | counter | `Number of remapped rows for uncorrectable errors` | exact |
| `DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS` | counter | `Number of remapped rows for correctable errors` | exact |
| `DCGM_FI_DEV_ROW_REMAP_FAILURE` | gauge | `Whether remapping of rows has failed` | exact |
| `DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` | **gauge** | `Total number of NVLink bandwidth counters for all lanes.` | conflict: supplied sample and catalog declare counter |
| `DCGM_FI_DEV_VGPU_LICENSE_STATUS` | gauge | `vGPU License status` | exact |

The pinned [GPU label renderer](https://github.com/NVIDIA/dcgm-exporter/blob/181290c399d46a9b905e083d0204348be63cb436/internal/pkg/rendermetrics/render_metrics.go#L308-L352)
uses core labels `gpu`, normally `UUID`, `pci_bus_id`, `device`, and
`modelName`, plus optional lowercase `hostname`. Kubernetes enrichment can add
`pod`, `namespace`, and `container`; MIG can add `GPU_I_PROFILE` and `GPU_I_ID`;
configured label fields such as `DCGM_FI_DRIVER_VERSION` can add further keys.
The catalog includes `pci_bus_id` but uses the supplied sample's uppercase
`Hostname`, which does not match this pinned revision. NVIDIA changed the native
key to lowercase in
[`d5e5f510`](https://github.com/NVIDIA/dcgm-exporter/commit/d5e5f510a1b6b393f39a43293ccd9dc985defc79),
an ancestor of `181290c`.

The supplied compatibility scrape is preserved as
[`internal/telemetry/testdata/dcgm-exporter-runtime.prom`](../../internal/telemetry/testdata/dcgm-exporter-runtime.prom).
It proves the intended sample contract, including uppercase `Hostname`,
`DCGM_FI_DRIVER_VERSION`, and XID-only `err_code`/`err_msg`. It does **not** turn
those keys into first-party facts for `181290c`. It also declares
`DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL` as counter while the pinned first-party CSV
declares gauge. Kasim must label this as a sample-compatibility overlay or choose
one source of truth; it cannot describe both schemas as one exact exporter
revision.

### AMD Device Metrics Exporter

Pinned first-party evidence: [gauge declarations at `4642bb4`](https://github.com/ROCm/device-metrics-exporter/blob/4642bb460926b531cefed17b5ad997be81b891f2/pkg/amdgpu/gpuagent/gpuagent_gpu_metrics.go#L779-L945) and [label configuration](https://github.com/ROCm/device-metrics-exporter/blob/4642bb460926b531cefed17b5ad997be81b891f2/docs/configuration/configmap.md).

| Catalog family | TYPE | Exact first-party HELP | Verdict |
| --- | --- | --- | --- |
| `gpu_gfx_activity` | gauge | `Graphics engine usage in Percentage (0-100)` | exact |
| `gpu_umc_activity` | gauge | `Memory engine usage in Percentage (0-100)` | exact |
| `gpu_used_vram` | gauge | `Used VRAM memory of the GPU (in MB)` | exact |
| `gpu_total_vram` | gauge | `Total VRAM memory of the GPU (in MB)` | exact |
| `gpu_free_vram` | gauge | `Free VRAM memory of the GPU (in MB)` | exact |
| `gpu_power_usage` | gauge | `GPU Power usage in Watts` | exact |
| `gpu_edge_temperature` | gauge | `Current edge temperature in Celsius` | exact |
| `gpu_clock` | gauge | `List of current GPU clock frequencies in MHz` | exact, including `clock_index` and selected `clock_type="system"` |

The pinned exporter adds `clock_index` and `clock_type` to `gpu_clock`.
Documented `clock_type` values are lowercase `data`, `system`, `memory`,
`video`, and `soc`; the catalog selects the supported `system` value.

The exact default mandatory label set, lowercased at exposition, is
`gpu_id`, `card_model`, `gpu_partition_id`,
`gpu_compute_partition_type`, `gpu_memory_partition_type`, `deployment_mode`,
`serial_number`, `pod`, `namespace`, `container`, `job_id`, `job_user`,
`job_partition`, and `hostname`. The catalog now contains this mandatory set.
Optional `gpu_uuid`, `pod_uuid`, process, custom, and extra Pod labels can add
keys. `MetricsFieldPrefix` can also change every metric name, so the unprefixed
names are exact only for an empty prefix.

### Intel XPU Manager

Pinned first-party evidence: [metric declarations at `57e44f5`](https://github.com/intel/xpumanager/blob/57e44f558a3c3f4e7ec3cdfae6ccd8739ffb3be5/rest/prometheus_exporter/prometheus_exporter_types.py) and the [exporter label/type implementation](https://github.com/intel/xpumanager/blob/57e44f558a3c3f4e7ec3cdfae6ccd8739ffb3be5/rest/prometheus_exporter/prometheus_exporter.py#L335-L479).

| Family | TYPE | Exact first-party HELP | Family labels beyond common labels |
| --- | --- | --- | --- |
| `xpum_engine_group_ratio` | gauge | `Avg utilization of engine group (in %), per GPU tile` | `type` |
| `xpum_memory_used_bytes` | gauge | `Used GPU memory (in bytes), per GPU tile` | none |
| `xpum_memory_ratio` | gauge | `Used GPU memory / Total used GPU memory (in %), per GPU tile` | none |
| `xpum_power_watts` | gauge | `Avg GPU power (in watts), per GPU and per card` | none |
| `xpum_temperature_celsius` | gauge | `Avg GPU temperature (in Celsius degree), per tile` | `location` |
| `xpum_frequency_mhz` | gauge | `Avg (GPU) frequency (in MHz), per GPU tile` | `location`, `type` |
| `xpum_energy_joules` | counter | `Total GPU energy consumption since boot (in Joules), per GPU` | none |

Common labels are `uuid`, `dev_name`, `pci_dev`, `vendor`, and `pci_bdf`, with
conditional `dev_file`, `node`, `kube_pod`, `kube_namespace`,
`kube_container`, `sub_dev`, and `card`. The exporter also appends `src` to
every selected family. The catalog now contains the base keys, `src`, and the
family-specific `type`/`location` keys; omitting conditional keys is faithful
for a device-level sample when their runtime conditions are absent. The catalog
uses the source-supported `src="direct"`; aggregated samples would instead use
the actual aggregation-function name. The source scales percentage inputs by
`0.01`; consequently the
`*_ratio` sample is a ratio even though the original HELP text says `%`.

### Huawei Ascend npu-exporter

Pinned first-party evidence: the [four descriptors at `97641a5`](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/metrics/collector_for_npu.go#L43-L51), [common label descriptor](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/common/metrics_collector.go#L30-L53), and [common gauge emission](https://gitee.com/ascend/mind-cluster/blob/97641a5566914158b9c0eb227c05a223d275e68d/component/npu-exporter/collector/metrics/common_utils.go#L70-L105).

| Family | TYPE | Exact first-party HELP |
| --- | --- | --- |
| `npu_chip_info_utilization` | gauge | `the ai core utilization` |
| `npu_chip_info_temperature` | gauge | `the npu temperature` |
| `npu_chip_info_power` | gauge | `the npu power` |
| `npu_chip_info_aicore_current_freq` | gauge | `the npu ai core current frequency, unit is 'MHz'` |

All four use exactly `id`, `model_name`, `vdie_id`, `pcie_bus_info`,
`namespace`, `pod_name`, and `container_name`. The catalog matches this schema.

### Cambricon mlu-exporter

Pinned first-party evidence: [metric configuration at `613459d`](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/examples/metrics.yaml), [descriptor/prefix construction](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/pkg/metrics/metrics.go#L45-L68), [gauge emission](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/pkg/collector/cndev.go), and the official [Kubernetes prefix argument](https://github.com/Cambricon/mlu-exporter/blob/613459d6b730cad3caf4c08aa3dcf28f523bf1c1/depolys/helm/mlu-exporter/values.yaml#L61).

| Family with official `mlu` prefix | TYPE | Exact first-party HELP | Native labels |
| --- | --- | --- | --- |
| `mlu_utilization` | gauge | `The utilization of Cambricon MLU, unit is '%'` | common plus `vf` |
| `mlu_memory_used` | gauge | `The used physical memory of Cambricon MLU, unit is 'B'` | common |
| `mlu_memory_total` | gauge | `The total physical memory of Cambricon MLU, unit is 'B'` | common |
| `mlu_power_usage` | gauge | `The power usage of Cambricon MLU, unit is 'w'` | common plus `vf` |
| `mlu_temperature` | gauge | `The board temperature of Cambricon MLU, unit is 'celsius'` | common |

The common label set is `driver`, `mcu`, `mlu`, `model`, `node`, `node_ip`,
`sn`, `type`, and `uuid`. The catalog matches it and now adds `vf` to
utilization and power. Metric names and labels are configuration-driven; the
bare executable has no fixed prefix, while the official Kubernetes deployment
uses `mlu`.

### Iluvatar / DeepSpark ix-exporter

Pinned first-party evidence: [`metrics.yaml` at `7f169d7`](https://gitee.com/deep-spark/ix-exporter/blob/7f169d7f1c0b66cc809ecba28f6d520e8f28ff2c/etc/metrics.yaml) and [gauge emission](https://gitee.com/deep-spark/ix-exporter/blob/7f169d7f1c0b66cc809ecba28f6d520e8f28ff2c/pkg/collector/collector.go#L78-L122).

| Catalog family | TYPE | Exact first-party HELP | Verdict |
| --- | --- | --- | --- |
| `ix_gpu_utilization` | gauge | `Utilization of iluvatar GPU (%).` | exact |
| `ix_mem_total` | gauge | `Total physical memory of iluvatar GPU (MiB).` | exact |
| `ix_mem_used` | gauge | `Used physical memory of iluvatar GPU (MiB).` | exact |
| `ix_mem_free` | gauge | `Free physical memory of iluvatar GPU (MiB).` | exact |
| `ix_power_usage` | gauge | `Power usage of iluvatar GPU (W).` | exact |
| `ix_gpu_temperature` | gauge | `GPU temperature of iluvatar GPU (C).` | exact |
| `ix_sm_clock` | gauge | `Sm clock of iluvatar GPU (MHz).` | exact |
| `ix_mem_clock` | gauge | `Mem clock of iluvatar GPU (MHz).` | exact |

Base labels are exactly `name`, `gpu`, `uuid`, `driver`, `ixml`, `serial`, and
`node_name`. When Kubernetes enrichment is enabled, the same configuration adds
`namespace`, `pod`, and `container`. The catalog accurately represents the base
set; omission of conditional Kubernetes keys is exact when that enrichment is
disabled.

### Enflame gcu-exporter

Pinned first-party evidence: the per-family collectors at `0e6e15c` for
[`usage`](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_usage.go),
[`memory used`](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_memory_used_bytes.go),
[`memory total`](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_memory_total_bytes.go),
[`power`](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_power_usage.go),
[`temperature`](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_tempertures.go), and
[`health`](https://github.com/EnflameTechnology/gcu-exporter/blob/0e6e15c9cb8034e85b70959cc30f702ac56114ed/collector/gcu_health.go).

| Family | TYPE | Exact first-party HELP | Native labels |
| --- | --- | --- | --- |
| `enflame_gcu_usage` | gauge | `Gcu usage as reported by the device, -1 means not supported` | common |
| `enflame_gcu_memory_used_bytes` | gauge | `Memory used size as reported by the device` | common |
| `enflame_gcu_memory_total_bytes` | gauge | `Total memory size as reorted by the device` | common; preserve the source typo if exact HELP is required |
| `enflame_gcu_power_usage` | gauge | `Power usage as reported by the device, -1 means not supported` | common |
| `enflame_gcu_temperatures` | gauge | `Temperature as reported by the device, -1 means not supported` | common |
| `enflame_gcu_health` | gauge | `Gcu health as reported by the device (2:healthy,1:unhealthy,0:unknown)` | common plus `healthmsg` |

Common labels are exactly `host`, `minor_number`, `uuid`, `busid`, `slot`,
`name`, `pod_name`, `pod_namespace`, and `container_name`. The catalog matches
them and now includes the health-only `healthmsg`. Its evidence points to the
pinned first-party `collector` tree containing the individual sources above.

### Furiosa metrics exporter

Pinned first-party evidence: the `e24b600` collectors for
[`alive`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/liveness.go),
[`utilization`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/core_utilization.go),
[`frequency`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/frequency.go),
[`memory`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/memory.go), and
[`cycles`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/cycle.go), plus its [label filter](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/internal/collector/label_filter_collector.go).

| Family | TYPE | Exact first-party HELP |
| --- | --- | --- |
| `furiosa_npu_alive` | gauge | `The liveness of NPU device` |
| `furiosa_npu_core_utilization` | gauge | `The current core utilization of NPU device` |
| `furiosa_npu_core_frequency` | gauge | `The current core frequency of NPU device (MHz)` |
| `furiosa_npu_dram_total` | gauge | `The total dram of NPU device (Bytes)` |
| `furiosa_npu_dram_usage` | gauge | `The current used dram of NPU device (Bytes)` |
| `furiosa_npu_total_cycle_count` | counter | `The current total cycle count of NPU device` |

Required non-empty labels are `arch`, `core`, `device`, `uuid`, `pci_bus_id`,
`firmware_version`, and `driver_version`, matching the catalog. The exporter can
also populate `hostname`, `namespace`, `pod`, and `container`; its label filter
omits those keys when their values are empty. The catalog now cites the pinned
[`README.rst`](https://github.com/furiosa-ai/furiosa-metrics-exporter/blob/e24b60086ea42d81ebf92adbabd5f595ac4ecdab/README.rst).

### Prometheus node_exporter InfiniBand collector

Pinned first-party evidence: the [descriptor map and emission code at `ac83e37`](https://github.com/prometheus/node_exporter/blob/ac83e377f04d53fd2683480337a0283d46204a33/collector/infiniband_linux.go#L50-L225).

| Family | TYPE | Exact first-party HELP | Labels |
| --- | --- | --- | --- |
| `node_infiniband_port_data_received_bytes_total` | counter | `Number of data octets received on all links` | `device`, `port` |
| `node_infiniband_port_data_transmitted_bytes_total` | counter | `Number of data octets transmitted on all links` | `device`, `port` |
| `node_infiniband_port_packets_received_total` | counter | `Number of packets received on all VLs by this port (including errors)` | `device`, `port` |
| `node_infiniband_port_packets_transmitted_total` | counter | `Number of packets transmitted on all VLs from this port (including errors)` | `device`, `port` |
| `node_infiniband_rate_bytes_per_second` | gauge | `Maximum signal transfer rate` | `device`, `port` |
| `node_infiniband_state_id` | gauge | `State of the InfiniBand port (0: no change, 1: down, 2: init, 3: armed, 4: active, 5: act defer)` | `device`, `port` |
| `node_infiniband_physical_state_id` | gauge | `Physical state of the InfiniBand port (0: no change, 1: sleep, 2: polling, 3: disable, 4: shift, 5: link up, 6: link error recover, 7: phytest)` | `device`, `port` |

All seven catalog declarations match the pinned upstream collector. This
profile remains vendor-neutral host telemetry rather than an RDMA Device Plugin
metric namespace.

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
