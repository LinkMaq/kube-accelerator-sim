# Vendor profile evidence and support classes

Vendor Profiles are immutable data records describing exact
Kubernetes-visible contracts. They are not executable vendor adapters and do
not install a device plugin or driver.

## Evidence classes

| Class or state | Meaning | Selection behavior |
| --- | --- | --- |
| `verified` | Exact public contract backed by grade-A upstream source evidence | Selectable when the model, contract, resource, and Fidelity Mode resolve |
| `provisional` | Representable contract with a material public-evidence limitation | Rejected unless the Scenario explicitly sets `provisionalProfiles: true` or the shortcut uses `--accept-provisional` |
| `custom` | Operator-supplied profile validated through the same schema, collision, evidence, and digest path | Must declare class `custom` and be pinned by exact revision and digest |
| `catalog-only` | Ecosystem or model is recorded for visibility but lacks enough evidence for a selectable contract | Rejected by `apply`; never converted into an invented resource name |

Profile class reflects evidence quality, not market share, hardware quality,
or vendor preference. Model lifecycle such as `current-product`,
`k8s-identified`, `deployed-retention`, or `catalog-only` is a separate field.

## Bundled ecosystem coverage

The catalog revision is `2026-08-03`. Use `kasim profile show <id> -o json` for
the exact source URLs, immutable revisions, checked dates, contract spellings,
models, limitations, and digest.

| Ecosystem | Profile ID | Class | Representative selectable models or state |
| --- | --- | --- | --- |
| NVIDIA | `nvidia` | verified | A100 40/80GB, H100, H200, L40S, B200, B300, A800, H800, H20 |
| AMD Instinct | `amd` | verified | MI210, MI250X, MI300A/X, MI325X, MI350X, MI355X |
| Intel Data Center GPU | `intel-gpu` | verified | Flex 140/170/170V, Max 1100/1550 |
| Intel Gaudi | `intel-gaudi` | verified | Gaudi2, Gaudi3 |
| Huawei Ascend | `huawei-ascend` | verified | Ascend 310/310P/910, Atlas A2/A3 |
| Cambricon | `cambricon` | verified | MLU370, MLU590, MLU270, MLU290 |
| Biren | `biren` | verified | BR100 family |
| Iluvatar CoreX | `iluvatar` | verified | BI-V150, BI-V150S |
| Enflame | `enflame` | verified | S60, S60G |
| Moore Threads | `moore-threads` | verified | MTT S3000, S80, S2000, S4000 |
| FuriosaAI | `furiosa` | verified | RNGD |
| Graphcore | `graphcore` | verified | GC200/C600 and retention models |
| AWS Neuron | `aws-neuron` | verified | Inferentia and Trainium families |
| Google Cloud TPU | `google-tpu` | verified | TPU v4, v5e, v5p, v6e, TPU7x |
| MetaX | `metax` | verified | C500/C500-P/C500X, C280/C290/C550/C600, N260 |
| Hygon DCU | `hygon` | verified | K100_AI, BW200, BW1000, Z100L, BW1100 |
| Kunlunxin through HAMi | `kunlunxin-hami` | provisional | P800, R480 |
| Vastai through HAMi | `vastai-hami` | provisional/catalog-only | No built-in selectable model seed |
| Qualcomm Cloud AI 100 | `qualcomm-cloud-ai-100` | provisional/catalog-only | Recorded model family; no selectable fully-qualified schedulable contract |

The current evidence review is in
[Accelerator vendor signals and model seeds](../research/accelerator-vendor-signals-and-models.md).
The earlier
[Vendor and model evidence catalog](../research/vendor-model-evidence-catalog.md)
is retained as the pre-2026-07-31 snapshot. Their source links are research
inputs; the bundled
[`profiles/catalog.json`](../../profiles/catalog.json) is the exact validated
release input.

## Inspect before use

The following commands are offline:

```sh
./dist/kasim profile list -o json
./dist/kasim profile show nvidia -o json
./dist/kasim profile show huawei-ascend -o json
./dist/kasim profile show kunlunxin-hami -o json
```

For provisional profiles, review the evidence limitation before opting in.
Popularity is not a substitute for an exact Kubernetes contract.

No scheduling profile asserts physical hardware behavior. A separately
versioned Telemetry Contract may preserve source-backed Prometheus schemas and
generate explicitly simulated values, but the product does not observe
physical vendor telemetry, provide device access, execute accelerator compute,
install vendor drivers, simulate NUMA topology, or inject CDI devices. See
[Simulated vendor Prometheus telemetry](simulated-vendor-telemetry.md).
