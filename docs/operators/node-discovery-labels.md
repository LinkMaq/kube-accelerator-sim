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
            revision: 2026-09-05
            digest: sha256:a04f86407d43998b667d61e6d2bbbc91a3ef004634e776dfc2fe330716a2a479
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          count: 8
          healthy: 8
```

Omitting the field preserves the exact prior canonical Scenario bytes and
digests; existing revisions are unaffected.

## Emitted label set

For one eight-card H100 Node Group the two Synthetic Nodes receive the same
36-label `nvidia.com/*` set a GPU Operator-managed node carries (minus
`gfd.timestamp` and `gpu.machine`, see Boundaries):

| Label | Value | Source |
| --- | --- | --- |
| `nvidia.com/gpu.present` | `true` | Contract-derived, static |
| `nvidia.com/gpu.count` | `8` | Sum of Accelerator Pool totals on the Node |
| `nvidia.com/gpu.product` | `NVIDIA-H100-80GB-HBM3` | Model evidence, GFD-normalized product name |
| `nvidia.com/gpu.family` | `hopper` | Model evidence |
| `nvidia.com/gpu.compute.major` | `9` | Model evidence |
| `nvidia.com/gpu.compute.minor` | `0` | Model evidence |
| `nvidia.com/gpu.memory` | `81920` | Model evidence, MiB envelope |
| `nvidia.com/gpu.mode` | `compute` | Model evidence, PCI-class mode of data-center boards |
| `nvidia.com/mig.capable` | `true` | Model evidence (`false` for L40S) |
| `nvidia.com/mig.strategy` | `single` | Contract-derived, gpu-operator default |
| `nvidia.com/gpu.replicas` | `1` | Contract-derived, no sharing configured |
| `nvidia.com/gpu.sharing-strategy` | `none` | Contract-derived, no sharing configured |
| `nvidia.com/mps.capable` | `false` | Contract-derived, no MPS configured |
| `nvidia.com/vgpu.present` | `false` | Contract-derived, no vGPU manager |
| `nvidia.com/cuda.driver-version.full` | `580.126.16` | Contract-derived, driver evidence shared with DCGM telemetry |
| `nvidia.com/cuda.driver-version.major` | `580` | Contract-derived |
| `nvidia.com/cuda.driver-version.minor` | `126` | Contract-derived |
| `nvidia.com/cuda.driver-version.revision` | `16` | Contract-derived |
| `nvidia.com/cuda.driver.major` | `580` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.driver.minor` | `126` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.driver.rev` | `16` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.runtime-version.full` | `13.3.1` | Contract-derived, latest CUDA GA release |
| `nvidia.com/cuda.runtime-version.major` | `13` | Contract-derived |
| `nvidia.com/cuda.runtime-version.minor` | `3` | Contract-derived |
| `nvidia.com/cuda.runtime.major` | `13` | Contract-derived, deprecated GFD key |
| `nvidia.com/cuda.runtime.minor` | `3` | Contract-derived, deprecated GFD key |
| `nvidia.com/gpu-driver-upgrade-state` | `upgrade-done` | Contract-derived, operator upgrade lifecycle |
| `nvidia.com/gpu.deploy.container-toolkit` | `true` | Contract-derived, operator component state |
| `nvidia.com/gpu.deploy.dcgm` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.dcgm-exporter` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.device-plugin` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.driver` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.gpu-feature-discovery` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.node-status-exporter` | `true` | Contract-derived |
| `nvidia.com/gpu.deploy.nvsm` | `` (empty) | Contract-derived, NVSM not deployed |
| `nvidia.com/gpu.deploy.operator-validator` | `true` | Contract-derived |

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
determinism) or `nvidia.com/gpu.machine` (host-specific, no simulated host
exists). The CUDA runtime version is pinned per catalog revision to the latest
CUDA GA release rather than detected from a node toolkit install; upgrade the
catalog to move it. The labels describe simulated scheduling inventory; they
never prove drivers, device files, or accelerated computation.
