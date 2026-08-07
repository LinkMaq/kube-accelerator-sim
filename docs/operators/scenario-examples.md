# Scenario examples

Every example is a canonical, versioned Scenario document. Validate it
offline before any cluster write:

```sh
./dist/kasim apply -f ./examples/heterogeneous.yaml \
  --dry-run=client \
  -o json
```

Connected apply always names the existing target explicitly:

```sh
./dist/kasim apply -f ./examples/heterogeneous.yaml \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

| Scenario | File | Intended control-plane shape |
| --- | --- | --- |
| Single Synthetic Node, single Accelerator | [single-node-single-accelerator.yaml](../../examples/single-node-single-accelerator.yaml) | One Synthetic Node with one `nvidia.com/gpu` unit |
| Single Synthetic Node, multiple Accelerators | [single-node-multi-accelerator.yaml](../../examples/single-node-multi-accelerator.yaml) | One Synthetic Node with eight units |
| Multiple Synthetic Nodes, multiple Accelerators | [multi-node-multi-accelerator.yaml](../../examples/multi-node-multi-accelerator.yaml) | Four homogeneous Synthetic Nodes with eight units each |
| Heterogeneous Node Groups | [heterogeneous.yaml](../../examples/heterogeneous.yaml) | Separate NVIDIA H100 and Huawei Atlas A2 Node Groups |
| Per-vendor presets | [examples/vendors](../../examples/vendors) | One directly applicable Scenario for each of 17 selectable vendor ecosystems |
| Extended-resource variants | [extended-resource-variants.yaml](../../examples/signals/extended-resource-variants.yaml) | Alternate, partitioned, shared, virtual, memory, and core resource signals |
| Stable DRA control plane | [dra-control-plane.yaml](../../examples/dra-control-plane.yaml) | NVIDIA, AMD, and AWS Neuron `resource.k8s.io/v1` inventory on Kubernetes 1.34–1.36 |
| Auxiliary RDMA and SR-IOV signals | [auxiliary-rdma-sriov.yaml](../../examples/signals/auxiliary-rdma-sriov.yaml) | H100 plus configurable RDMA tokens and MI300X plus configurable SR-IOV virtual-function tokens |
| Reference scale | [reference-scale.yaml](../../test/e2e/testdata/reference-scale.yaml) | Release gate with 1,000 Synthetic Nodes and 8,000 units |

The complete vendor/resource matrix, including exact resource names and
evidence-driven omissions, is documented in the
[examples index](../../examples/README.md). For example:

```sh
./dist/kasim apply -f ./examples/vendors/hygon.yaml \
  --dry-run=client \
  -o json

./dist/kasim apply -f ./examples/signals/extended-resource-variants.yaml \
  --dry-run=client \
  -o json

./dist/kasim apply -f ./examples/signals/auxiliary-rdma-sriov.yaml \
  --dry-run=client \
  -o json
```

The Kunlunxin example opts in to the provisional, integration-specific
`kunlunxin-hami` profile explicitly. The CLI receipt reports that class,
HAMi provider scope, and its evidence; it is not silently promoted to a
verified vendor default.

The file stores exact profile revisions and digests. If catalog evidence is
revised, select the new profile intentionally and review the resulting
canonical digest. Do not mechanically edit a digest to bypass compilation.

## Homogeneous demo shortcut

For a quick homogeneous scheduling projection, the shortcut uses the same
compiler and lifecycle path:

```sh
./dist/kasim apply demo \
  --profile nvidia \
  --model nvidia-h100 \
  --contract device-plugin \
  --resource gpu \
  --nodes 2 \
  --accelerators-per-node 8 \
  --healthy-per-node 8 \
  --dry-run=client \
  -o json
```

Remove `--dry-run=client` and add both explicit target flags to submit:

```sh
./dist/kasim apply demo \
  --profile nvidia \
  --model nvidia-h100 \
  --contract device-plugin \
  --resource gpu \
  --nodes 2 \
  --accelerators-per-node 8 \
  --healthy-per-node 8 \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

Heterogeneous and multi-pool configurations use a Scenario document, not the
shortcut.

## Health, capacity, and scale

The example files set `count` (capacity) and `healthy` (allocatable) per
Synthetic Node.
A smaller `healthy` value represents partial health while remaining a valid
Ready Scenario. The CLI `health` command revises only that typed field.

The CLI `scale` command changes one Node Group replica count. Scale-down uses
the highest stable replica indices first. A capacity reduction can report
`Overcommitted`; existing bound Pods remain untouched.

## DRA gating

`dra-control-plane` requires stable `resource.k8s.io/v1`, so preflight rejects
it below Kubernetes 1.34 and outside the supported 1.34–1.36 DRA range. It
simulates DRA inventory, claims, allocation, scheduler reservation, and
cleanup. It does not perform node preparation or container device injection.

All examples model Kubernetes-visible surfaces. The project does not provide
device access, execute accelerator compute, install vendor drivers, observe
physical vendor telemetry, simulate NUMA topology, or inject CDI devices.
Source-backed Prometheus schemas contain explicitly simulated values only.
