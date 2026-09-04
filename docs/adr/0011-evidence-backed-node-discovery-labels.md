# ADR 0011: Evidence-backed node discovery labels

Status: Accepted

GPU clusters managed by the NVIDIA GPU Operator carry a `nvidia.com/*` node
label set produced by GPU Feature Discovery. Platform workloads select nodes
through these labels, so placement, inventory, and admission tests against
Kasim must exercise the same selection path. Kasim will project the
scheduling-visible GFD label set onto Synthetic Nodes, but only through
catalog evidence, never by deriving vendor values from model IDs.

The Profile catalog schema gains an optional per-model `nodeLabels` collection
whose exact values carry `gfd` evidence references, and an optional
`derivedNodeLabels` collection on Resource Contracts whose values derive from
the resolved Scenario shape (`static` literals and `pool-total-capacity`).
Scenario documents gain an optional `node.discoveryLabels` opt-in flag and the
demo shortcut gains `--discovery-labels`; omitting the flag preserves existing
canonical Scenario bytes and digests, so accepted revisions remain immutable.
When enabled, evidenced model values and contract-derived values merge into
the extended-resource projection's identity labels: they reach real Node
objects through the existing reconcile merge, participate in the
`extended-resources` fidelity surface, and fail closed on conflicts between
pools or against Scenario-provided labels.

The emitted set covers `gpu.present`, `gpu.count`, `gpu.product`,
`gpu.family`, `gpu.compute.major`, `gpu.compute.minor`, and `gpu.memory`.
Kasim deliberately excludes `gfd.timestamp` (Snapshot determinism),
`gpu.machine` (host-specific), `gpu.clique` (NVLink fabric is outside the
fidelity boundary), MIG strategy labels (partitioning is selected through
resource aliases), and CUDA runtime version labels (no CUDA runtime fidelity
claim). The labels describe simulated scheduling inventory only; they do not
claim drivers, device files, telemetry authenticity, or accelerator compute.

## Considered options

- Always-on emission was rejected because fresh applies of existing Scenario
  documents would change rendered Node state and violate revision stability.
- A user-supplied label workaround was rejected because it invites invented
  vendor values without provenance and drifts from the catalog.
- Emitting values derived from model IDs (for example the catalog model ID as
  `gpu.product`) was rejected because GFD values are NVML product strings and
  a substituted ID silently breaks real workload selectors.
