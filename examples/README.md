# Accelerator simulation examples

Every YAML file below is a complete Scenario. It compiles offline and submits
to an explicitly selected, already-existing Kubernetes cluster:

```sh
./dist/kasim apply -f ./examples/vendors/amd.yaml \
  --dry-run=client \
  -o json

./dist/kasim apply -f ./examples/vendors/amd.yaml \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

Change `replicas`, `count`, and `healthy` to move between single-Node,
multi-Accelerator, and multi-Node shapes. These files model
Kubernetes-visible scheduling signals only; they do not install a vendor
Device Plugin or provide accelerator computation.

## Vendor examples

Each file selects one representative mainstream model and its whole-device
Resource Contract.

| Ecosystem | Example | Representative model | Kubernetes resource | Profile class |
| --- | --- | --- | --- | --- |
| NVIDIA | [`nvidia.yaml`](vendors/nvidia.yaml) | H100 | `nvidia.com/gpu` | verified |
| AMD Instinct | [`amd.yaml`](vendors/amd.yaml) | MI300X | `amd.com/gpu` | verified |
| Intel Data Center GPU | [`intel-gpu.yaml`](vendors/intel-gpu.yaml) | Max 1550 | `gpu.intel.com/xe` | verified |
| Intel Gaudi | [`intel-gaudi.yaml`](vendors/intel-gaudi.yaml) | Gaudi3 | `habana.ai/gaudi` | verified |
| Huawei Ascend | [`huawei-ascend.yaml`](vendors/huawei-ascend.yaml) | Atlas A2 | `huawei.com/Ascend910` | verified |
| Cambricon MLU | [`cambricon.yaml`](vendors/cambricon.yaml) | MLU590 | `cambricon.com/mlu` | verified |
| Biren | [`biren.yaml`](vendors/biren.yaml) | BR100 | `birentech.com/gpu` | verified |
| Iluvatar CoreX | [`iluvatar.yaml`](vendors/iluvatar.yaml) | BI-V150S | `iluvatar.com/gpu` | verified |
| Enflame | [`enflame.yaml`](vendors/enflame.yaml) | S60 | `enflame.com/gcu` | verified |
| Moore Threads | [`moore-threads.yaml`](vendors/moore-threads.yaml) | MTT S3000 | `mthreads.com/gpu` | verified |
| FuriosaAI | [`furiosa.yaml`](vendors/furiosa.yaml) | RNGD | `furiosa.ai/rngd` | verified |
| Graphcore | [`graphcore.yaml`](vendors/graphcore.yaml) | GC200/C600 | `c600.graphcore.ai/ipu` | verified |
| AWS Neuron | [`aws-neuron.yaml`](vendors/aws-neuron.yaml) | Trainium2 | `aws.amazon.com/neuron` | verified, AWS-scoped |
| Google Cloud TPU | [`google-tpu.yaml`](vendors/google-tpu.yaml) | TPU v6e | `google.com/tpu` | verified, GKE-scoped |
| MetaX | [`metax.yaml`](vendors/metax.yaml) | C500 | `metax-tech.com/gpu` | verified |
| Hygon DCU | [`hygon.yaml`](vendors/hygon.yaml) | K100_AI | `hygon.com/dcu` | verified |
| Kunlunxin through HAMi | [`kunlunxin.yaml`](vendors/kunlunxin.yaml) | P800 | `kunlunxin.com/xpu` | provisional, HAMi integration |

The Kunlunxin file deliberately sets
`acceptance.provisionalProfiles: true`. Review its evidence receipts before
submitting them. A provider-scoped signal can still be projected into a test
cluster, but the simulator does not claim that the target has the provider's
real node runtime.

## Signal variants

[`signals/extended-resource-variants.yaml`](signals/extended-resource-variants.yaml)
collects the additional model-compatible scalar signals that do not fit the
one-whole-device-per-vendor examples:

| Signal kind | Kubernetes resources |
| --- | --- |
| Alternate device generations or drivers | `gpu.intel.com/i915`, `huawei.com/Ascend310`, `huawei.com/Ascend310P`, `cambricon.com/mlu370`, `iluvatar.ai/gpu` |
| Hardware partitions | `nvidia.com/mig-1g.10gb`, `nvidia.com/mig-2g.20gb`, `nvidia.com/mig-7g.80gb`, `amd.com/cpx_nps4`, `amd.com/spx_nps1`, `amd.com/cpx_nps1`, `cambricon.com/mlu370.mim-2m.8gb`, `birentech.com/1-4-gpu`, `birentech.com/1-2-gpu`, selected `hygon.com/dcu-share-*` profiles |
| Shared or virtual devices | `nvidia.com/gpu.shared`, `huawei.com/npu-core`, `cambricon.com/mlu370.share`, `enflame.com/shared-gcu`, `enflame.com/drs-gcu`, `mthreads.com/sgpu-core`, `mthreads.com/sgpu-memory`, `metax-tech.com/sgpu`, `kunlunxin.com/vxpu`, `kunlunxin.com/vxpu-memory` |
| Passthrough devices | `metax-tech.com/vfio-gpu` |
| Device cores | `aws.amazon.com/neuroncore` |

[`dra-control-plane.yaml`](dra-control-plane.yaml) covers all currently
selectable stable-DRA contracts: NVIDIA `gpu.nvidia.com`, AMD `gpu.amd.com`,
and AWS Neuron `neuron.aws.com`. DRA examples require Kubernetes 1.34–1.36;
the scalar examples support the bounded 1.30–1.36 range.

The catalog also records `enflame.com/gcu-count` and exact Hygon
`hygon.com/dcu-mig-*` profiles, but no bundled Accelerator Model currently has
a source-backed compatibility mapping to those aliases. Vastai through HAMi
and Qualcomm Cloud AI 100 likewise have no selectable bundled model. They are
intentionally not turned into runnable examples until the missing public
evidence exists.

## Topology examples

The root examples retain small topology-focused scenarios:

- [`single-node-single-accelerator.yaml`](single-node-single-accelerator.yaml)
- [`single-node-multi-accelerator.yaml`](single-node-multi-accelerator.yaml)
- [`multi-node-multi-accelerator.yaml`](multi-node-multi-accelerator.yaml)
- [`heterogeneous.yaml`](heterogeneous.yaml)

Repository contract tests recursively compile every YAML file under
`examples/` and verify that every model-compatible bundled resource signal is
represented at least once.
