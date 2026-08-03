# ADR 0009: Model auxiliary scheduling signals as source-backed pools

Status: Accepted

Accelerator environments commonly expose related RDMA or SR-IOV scheduling resources, but Kubernetes provides no portable rule that pairs those resources with an Accelerator, and the published quantity does not prove a physical NIC, link, driver, CNI path, or working data plane. Kasim will model these surfaces as Auxiliary Device Pools rather than vendor-specific RDMA flags, arbitrary Node capacity entries, or Accelerator Pools.

An Auxiliary Device Pool belongs to one Node Group, repeats on every Synthetic Node, selects one source-backed Resource Contract through an immutable Profile reference, declares total quantity and schedulable availability, and names its associated Accelerator Pools explicitly. It advances and is cleaned up with the Scenario Revision. Scalar contracts project exact capacity and allocatable values; they do not create device rows or IDs. A future DRA auxiliary contract may create device evidence only when a pinned contract and supported native schema provide it.

The Profile catalog will bump to a new schema revision that classifies each Resource Contract as `accelerator` or `auxiliary`. Auxiliary contracts declare a source-backed category and whether the Kubernetes resource name is fixed or must be supplied exactly by the Scenario because the upstream plugin makes it configurable. Existing contracts migrate explicitly as accelerator contracts. Scenario documents gain an optional `auxiliaryDevicePools` collection; omitting it preserves existing canonical bytes and digests. Conflicting projected resource names, unresolved associations, duplicate pool names, unverifiable overrides, or availability above total fail compilation.

Availability describes the simulated scheduling surface and is not physical health. Receipts and status preserve the Profile, Resource Contract, exact emitted resource, associations, requested/observed quantity, schedulable availability, evidence class, and the unconditional scheduling-only fidelity boundary. The first built-in templates target the RDMA Shared Device Plugin and SR-IOV Network Device Plugin using their configurable resource-name contracts; they do not claim a universal resource name or automatic GPU-to-NIC association.

## Considered options

- A vendor-specific `rdma: true` field was rejected because resource names, prefixes, sharing semantics, and associations are administrator-configurable.
- Reusing arbitrary Node `capacity` was rejected because it loses provenance, unit, association, availability, receipt, and conflict semantics.
- Treating RDMA tokens as Accelerator Pool members was rejected because shared tokens, virtual functions, and network resources are not accelerator cards and must not affect card totals.
