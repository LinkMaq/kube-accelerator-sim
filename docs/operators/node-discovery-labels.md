# Node discovery labels

Real GPU clusters run the NVIDIA GPU Operator, whose GPU Feature Discovery
(GFD) component stamps an evidence-rich `nvidia.com/*` label set on every GPU
Node. Workloads commonly select those labels with `nodeSelector` or affinity,
for example `nvidia.com/gpu.product: NVIDIA-H100-80GB-HBM3`. Kasim can project
the same scheduling-visible label set onto Synthetic Nodes so placement tests
exercise the identical selection path.

This feature is evidence-backed and opt-in. It never claims drivers, device
files, CUDA, or real hardware; it projects the exact label keys and values
that GFD emits, sourced from the pinned
[k8s-device-plugin GFD documentation](https://github.com/NVIDIA/k8s-device-plugin/blob/3c6be400411aad793892e976824910d0880dd3a8/docs/gpu-feature-discovery/README.md)
and the catalog's per-model evidence.

## Enable discovery labels

Set `discoveryLabels: true` on the Node of a Node Group, or pass
`--discovery-labels` to the `apply demo` shortcut:

```yaml
metadata:
  name: discovery-labels
spec:
  fidelity: scheduling
  nodeGroups:
    - name: workers
      replicas: 2
      node:
        discoveryLabels: true
      acceleratorPools:
        - name: accelerator
          profile:
            id: nvidia
            revision: 2026-09-04
            digest: sha256:75266b3202e76b55786a989bf920c1c4dc27f3e955e23b0db72fe2fabe94675e
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          count: 8
          healthy: 8
```

Omitting the field preserves the exact prior canonical Scenario bytes and
digests; existing revisions are unaffected.

## Emitted label set

For one eight-card H100 Node Group the two Synthetic Nodes receive:

| Label | Value | Source |
| --- | --- | --- |
| `nvidia.com/gpu.present` | `true` | Contract-derived, static |
| `nvidia.com/gpu.count` | `8` | Sum of Accelerator Pool totals on the Node |
| `nvidia.com/gpu.product` | `NVIDIA-H100-80GB-HBM3` | Model evidence, GFD-normalized product name |
| `nvidia.com/gpu.family` | `hopper` | Model evidence |
| `nvidia.com/gpu.compute.major` | `9` | Model evidence |
| `nvidia.com/gpu.compute.minor` | `0` | Model evidence |
| `nvidia.com/gpu.memory` | `81920` | Model evidence, MiB envelope |

Kasim ownership labels (`simulation.kasim.io/*`) and
`feature.node.cloud.xiaoshiai.cn/accelerator-model.name` remain present and
unchanged.

## Failure behavior

- A model without catalog node-label evidence emits no vendor labels; the
  Scenario still applies, and the discovery request simply projects nothing
  for that pool. Check `kasim profile show` for the model's evidence.
- A Scenario-provided Node label that collides with a vendor discovery label
  fails compilation with `reserved by vendor node discovery`. Identical
  repeated values across pools of the same model remain legal; conflicting
  values across pools fail compilation.
- The values ride the `extended-resources` fidelity surface: reconciliation
  and `kasim status` verify the labels on every Synthetic Node exactly like
  capacity, so a drifted label fails the Snapshot instead of being silently
  reapplied.

## Boundaries

Kasim deliberately does not emit `nvidia.com/gfd.timestamp` (breaks Snapshot
determinism), `nvidia.com/gpu.machine` (host-specific), `gpu.clique`
(NVLink fabric is outside the fidelity boundary), MIG strategy labels
(partitioning is selected per resource alias instead), or CUDA runtime
version labels (Kasim does not claim CUDA runtime fidelity). The labels
describe simulated scheduling inventory; they never prove drivers, device
files, or accelerated computation.
