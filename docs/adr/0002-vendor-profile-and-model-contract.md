# ADR 0002: Separate Vendor Profiles, Resource Contracts, and Accelerator Models

Status: Accepted

## Context

Accelerator ecosystems usually register one Kubernetes resource for many board models, while some integrations expose versioned partition or sharing variants. Product names alone are not evidence for a Kubernetes resource name, and community integrations must not be presented as vendor defaults. Combining vendor, model, resource name, and evidence into one record would either duplicate data or encourage invented contracts.

## Decision

The catalog has three independent domain records:

1. A **Vendor Profile** identifies one Accelerator ecosystem or explicitly named integration and contains immutable, source-pinned revisions.
2. A **Resource Contract** records one exact Kubernetes-facing surface within that profile. Its kind is `extended-resource` or `dra`; the kind-specific payload contains exact resource or driver names, identity signals, supported variants, provider scope, and capability evidence.
3. An **Accelerator Model** identifies one commercially distinct board or SKU. It references compatible resources and exact identity-signal values when those mappings are evidenced. Model aliases are alternative names for the same SKU, never regional variants or merely related products.

Every rendered fact is traceable to an Evidence Record containing an evidence grade, source URI, immutable source revision when available, and checked date. The effective evidence grade of a Resource Contract is the weakest evidence required to render it.

Profile revisions are immutable. A Scenario Instance records the resolved profile ID, revision, and content digest. Reapplying a Scenario does not silently upgrade an existing instance to a newer profile revision.

### Profile classes

- `verified`: bundled and selectable by default. Every emitted vendor or provider key and every default mapping needed for rendering has Grade A evidence.
- `provisional`: bundled only when an exact usable contract is public but some material evidence is Grade B or C. Selection requires explicit acceptance, and reports always expose the integration owner, evidence grade, source, and revision. A community-backed profile uses an integration-specific ID such as `kunlunxin-hami`; it is never called the vendor default.
- `custom`: supplied by the user and accepted only after schema and Kubernetes safety validation. It may define arbitrary valid fully qualified resource names, but results always identify it as unverified and it cannot shadow a bundled profile ID.

Product-only Grade D records remain discovery catalog entries. They are not renderable built-in profiles because no exact public Kubernetes contract is known. A user may make such an ecosystem schedulable only by supplying an explicit custom profile.

Evidence grade, Profile Class, and model lifecycle are separate axes. Model lifecycle uses `k8s-identified`, `current-product`, or `deployed-retention`; none of those labels upgrades a Resource Contract.

### Resource and identity rules

- A model name never creates or changes an extended-resource name, DRA driver, DeviceClass, attribute, or vendor label.
- Resource-name derivation is allowed only through a closed, source-backed rule in the selected Resource Contract, with enumerated inputs and validation. MIG and documented partition variants are examples.
- Current and legacy resource names are separate contract variants tied to their source revisions. They are never advertised together unless the Scenario explicitly requests a migration test.
- Exact case is preserved. Similar spellings are not aliases without evidence.
- Scalar extended resources and DRA remain different Resource Contracts even when they refer to the same Accelerator Model.
- Capability evidence is tri-state: `verified`, `not-public`, or `not-applicable`. Missing evidence is never converted to `false` or to an implementation claim.
- Provider-managed contracts such as AWS Neuron and GKE TPU declare their provider scope.
- Simulator-owned normalized vendor and model identity may be added for ownership and portable test selection, but it is namespaced as simulator metadata and never reported as a vendor-provided label.

### Minimum record shape

The storage format may evolve, but it must preserve these semantics:

```yaml
profile:
  id: nvidia
  class: verified
  revision: 2026-07-29
  digest: sha256:...
  contracts:
    - id: device-plugin
      kind: extended-resource
      evidence:
        grade: A
        source: https://github.com/NVIDIA/k8s-device-plugin
        revision: <tag-or-commit>
        checkedAt: 2026-07-29
      resources:
        - id: gpu
          name: nvidia.com/gpu
          unit: device
      identitySignals:
        - kind: node-label
          key: nvidia.com/gpu.product
models:
  - id: nvidia-h100
    canonicalName: NVIDIA H100
    aliases: [H100]
    lifecycle: k8s-identified
    resourceRefs: [gpu]
    identityBindings:
      - signal: nvidia.com/gpu.product
        value: NVIDIA-H100-80GB-HBM3
```

## Initial coverage

The first verified catalog covers source-backed contracts for NVIDIA, AMD Instinct, Intel Data Center GPU, Intel Gaudi, Huawei Ascend, Cambricon, Biren, Iluvatar CoreX, Enflame, Moore Threads, FuriosaAI, Graphcore, AWS Neuron, and Google Cloud TPU, including the mainstream model seed established by the vendor evidence research.

The first provisional catalog includes exact, integration-named contracts for MetaX, Hygon DCU, Kunlunxin through HAMi, and Vastai through HAMi. Qualcomm Cloud AI 100 and product-only ecosystems remain catalog-only until a complete fully qualified resource or DRA contract is public. This limitation is evidence-driven; the project does not invent missing vendor names to create superficial coverage.

## Consequences

- Adding most vendors and models changes catalog data rather than core control flow.
- Mainstream boards can share one Kubernetes resource without losing model identity.
- Provenance and uncertainty remain visible in every Scenario Instance report.
- Provisional and custom profiles are usable without being confused with verified vendor contracts.
- Evidence drift can be checked independently of the Scenario and backend implementation.
