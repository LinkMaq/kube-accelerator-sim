# 厂商档案证据与支持等级

Vendor Profile 是描述准确 Kubernetes 可见契约的不可变数据记录。它不是可执行的
厂商适配器，也不会安装 device plugin 或驱动。

## 证据等级

| 等级或状态 | 含义 | 选择行为 |
| --- | --- | --- |
| `verified` | 准确公开契约有 A 级上游来源支撑 | 型号、契约、资源和保真模式能够解析时可直接选择 |
| `provisional` | 契约可表达，但公开证据存在实质限制 | 必须显式设置 `provisionalProfiles: true` 或使用 `--accept-provisional` |
| `custom` | 操作者提供并通过相同 schema、冲突、证据和摘要校验 | 必须声明 `custom`，并固定准确修订和摘要 |
| `catalog-only` | 已记录生态或型号，但证据不足以形成可选契约 | `apply` 拒绝，绝不编造资源名 |

证据等级反映证据质量，不代表市场份额、硬件质量或厂商偏好。型号生命周期是单独字段。

## 内置生态覆盖

目录修订为 `2026-07-31`。使用 `kasim profile show <id> -o json` 查看准确来源、
修订、检查日期、资源契约、型号、限制和摘要。

| 生态 | Profile ID | 等级 | 代表性可选型号或状态 |
| --- | --- | --- | --- |
| NVIDIA | `nvidia` | verified | A100、H100、H200、L40S、B200/B300、A800、H800、H20 |
| AMD Instinct | `amd` | verified | MI210、MI250X、MI300A/X、MI325X、MI350X、MI355X |
| Intel 数据中心 GPU | `intel-gpu` | verified | Flex 140/170/170V、Max 1100/1550 |
| Intel Gaudi | `intel-gaudi` | verified | Gaudi2、Gaudi3 |
| 华为昇腾 | `huawei-ascend` | verified | Ascend 310/310P/910、Atlas A2/A3 |
| 寒武纪 | `cambricon` | verified | MLU270/290/370/590 |
| 壁仞 | `biren` | verified | BR100 系列 |
| 天数智芯 | `iluvatar` | verified | BI-V150、BI-V150S |
| 燧原 | `enflame` | verified | S60、S60G |
| 摩尔线程 | `moore-threads` | verified | MTT S80/S2000/S3000/S4000 |
| FuriosaAI | `furiosa` | verified | RNGD |
| Graphcore | `graphcore` | verified | GC200/C600 及保留型号 |
| AWS Neuron | `aws-neuron` | verified | Inferentia、Trainium 系列 |
| Google Cloud TPU | `google-tpu` | verified | TPU v4、v5e、v5p、v6e、TPU7x |
| 沐曦 | `metax` | verified | C500/C500-P/C500X、C280/C290/C550/C600、N260 |
| 海光 DCU | `hygon` | verified | K100_AI、BW200、BW1000、Z100L、BW1100 |
| 昆仑芯（HAMi） | `kunlunxin-hami` | provisional | P800、R480 |
| Vastai（HAMi） | `vastai-hami` | provisional/catalog-only | 无内置可选型号种子 |
| Qualcomm Cloud AI 100 | `qualcomm-cloud-ai-100` | provisional/catalog-only | 记录型号族，暂无完整可调度契约 |

当前研究输入见[加速器厂商信号与型号](../../research/accelerator-vendor-signals-and-models.md)，
准确发布输入为 [`profiles/catalog.json`](../../../profiles/catalog.json)。

## 使用前检查

以下命令完全离线：

```sh
./dist/kasim profile list -o json
./dist/kasim profile show nvidia -o json
./dist/kasim profile show huawei-ascend -o json
./dist/kasim profile show kunlunxin-hami -o json
```

选择 provisional 档案前必须阅读证据限制。流行度不能替代准确的 Kubernetes 契约。
任何档案都不声明物理硬件行为或计算能力。
