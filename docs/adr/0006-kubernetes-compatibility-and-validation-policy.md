# ADR 0006: Support Kubernetes 1.30+ through a release-frozen validation matrix

Status: Accepted

## Context

The product submits Scenario Revisions to an existing Kubernetes cluster and
depends on scheduler, Node, Lease, authorization, admission, CRD, KWOK, and
optionally DRA behavior. A server version string alone cannot prove that the
required APIs, feature gates, permissions, admission behavior, or installed
runtime are usable.

The requested compatibility floor is Kubernetes 1.30. At the decision date,
2026-07-30, Kubernetes 1.36 is the latest stable minor and the upstream project
maintains only 1.34, 1.35, and 1.36. Kubernetes 1.30 through 1.33 are already
end-of-life, but they remain common enough in platform environments to justify
a functional compatibility lane. Supporting them does not make their control
planes secure or upstream-maintained.

DRA does not have one portable contract across that entire range. Kubernetes
1.30 and 1.31 contain incompatible alpha-era APIs, 1.32 and 1.33 contain
feature-gated beta APIs, and the core structured DRA API is
`resource.k8s.io/v1` from 1.34. Carrying every historical DRA representation
would substantially increase the public compatibility surface without
improving the default scheduling simulation.

## Decision

### Support vocabulary

Each product release publishes three different facts:

1. **compatible minor**: a Kubernetes minor for which the release accepts
   `scheduling` Scenarios;
2. **validated patch**: the exact upstream patch, cluster image digest, KWOK
   digest, kind version, host architecture, and container provider used as
   evidence for that minor;
3. **upstream status**: whether Kubernetes still maintains that minor.

A compatible minor is a functional product statement, not an upstream security
or lifecycle statement. Validation evidence always records exact patches and
digests even though the user-facing compatibility range is expressed in
minors.

The first release has a compatibility floor of Kubernetes 1.30. Raising that
floor within the product's 1.x line requires a new explicit compatibility
decision and migration notice; it is never an incidental dependency update.

Each product release also freezes a maximum validated Kubernetes minor. The
initial decision baseline is 1.36. A server below 1.30 fails preflight with
`KubernetesVersionUnsupported`; a server newer than the release's recorded
maximum fails with `KubernetesVersionUntested`. The phrase “1.30+” therefore
means “from 1.30 through the maximum validated by this product release”, not an
unbounded promise about future Kubernetes versions.

`kasim version` and machine-readable preflight output include the floor, the
maximum validated minor, the exact test matrix revision, the bundled KWOK and
catalog digests, and Fidelity Mode support by Kubernetes minor.

### Initial Kubernetes and Fidelity Mode matrix

The implementation starts with this matrix:

| Kubernetes minor | Initial exact patch fixture | Upstream status on 2026-07-30 | `scheduling` | `dra-control-plane` | Classification |
| --- | --- | --- | --- | --- | --- |
| 1.30 | 1.30.14 | EOL | Required | Unsupported | legacy compatibility floor |
| 1.31 | 1.31.14 | EOL | Required | Unsupported | legacy compatibility |
| 1.32 | 1.32.13 | EOL | Required | Unsupported | legacy compatibility |
| 1.33 | 1.33.13 | EOL | Required | Unsupported | legacy compatibility |
| 1.34 | 1.34.10 | Maintained | Required | Required with `resource.k8s.io/v1` | current |
| 1.35 | 1.35.7 | Maintained | Required | Required with `resource.k8s.io/v1` | current |
| 1.36 | 1.36.3 | Maintained, latest stable minor | Required | Required with `resource.k8s.io/v1` | current ceiling |

The release process recalculates upstream lifecycle labels and the ceiling
without silently changing the 1.30 floor.

The first implementation deliberately does not support classic DRA
`v1alpha2`/`v1alpha3` on Kubernetes 1.30 or 1.31, nor the
`v1beta1`/`v1beta2` structured DRA variants on 1.32 or 1.33. Requesting
`dra-control-plane` there fails before revision acceptance with a typed
`FidelityUnsupported` diagnostic that identifies the required Kubernetes and
API version. It never falls back to `scheduling`.

On Kubernetes 1.34 and newer, `dra-control-plane` uses only discovered
`resource.k8s.io/v1` types. The server minor is necessary but insufficient:
discovery, exact resources, required fields, scheduler behavior, RBAC, and
server dry-run must also pass. Optional alpha or beta DRA extensions are
outside the first contract even when the server happens to expose them.

### Stable API baseline

The portable `scheduling` path is restricted to APIs stable before the 1.30
floor:

- `core/v1` for Nodes, Node status, Pods, Namespaces, and Events;
- `coordination.k8s.io/v1` for Leases;
- `authorization.k8s.io/v1` for SelfSubjectAccessReviews;
- `rbac.authorization.k8s.io/v1` for installed roles and bindings;
- `apiextensions.k8s.io/v1` for the product CRD;
- the product's own explicitly versioned API.

Admission preflight uses Kubernetes server dry-run, and optimistic mutation
uses API resource versions, UID preconditions, and the Scenario Instance
generation contract. No removed beta Kubernetes API is kept as a fallback.

The implementation pins one `client-go` minor aligned with the release's
maximum validated Kubernetes minor. It may use typed clients for the stable
baseline and dynamic discovery for version-gated surfaces. `client-go`'s broad
backward interoperability is not accepted as compatibility evidence by
itself; every advertised server minor must pass the project matrix.

Vendor-specific server suffixes such as managed-service build identifiers are
parsed by Kubernetes semantic version rules. A distribution is accepted when
its upstream minor is in range and every capability probe passes. The initial
release does not certify individual managed distributions and does not bypass
a failed capability because the reported minor is otherwise supported.

### Runtime and harness pins

KWOK remains an internal runtime dependency. A release pins:

- the KWOK release and controller image by digest;
- the exact minimal KWOK configuration and Stage revisions;
- all installed CRD and RBAC manifest digests;
- the Kubernetes minors against which that exact combination passed.

KWOK v0.8.0 is the initial runtime baseline. Its source records Kubernetes
1.30.14 through 1.36.1 among its release inputs, which makes the requested
floor plausible but does not replace product end-to-end testing.

kind remains test infrastructure, not a product dependency. The test lock
records the release-paired tuple:

```text
kind version
kindest/node tag and sha256 digest
Kubernetes GitVersion
host architecture
container provider and version
feature-gate configuration
KWOK image and manifest digests
```

The compatibility matrix tests the final patch of every EOL minor and the
current patch of every maintained minor. When the pinned kind release does not
publish that exact Node image, the harness builds it from the immutable
Kubernetes tag through a pinned kind build recipe, publishes it to the project
CI registry, and records its provenance and digest. Such an image is labelled
`project-built`, never `kind release-paired`.

Official release-paired kind images remain protocol-oracle fixtures and
bootstrap references. The harness does not assume that an arbitrary kind
binary is compatible with a Node image from another kind release. Every
project-built or release-paired image is pinned by digest, and replacing a row
requires the complete row to pass again.

### Capability preflight

Every cluster mutation performs the ADR 0005 preflight and additionally
records:

- parsed server version and the release matrix row;
- discovery results for every required GroupVersionResource and subresource;
- whether dry-run, status subresources, UID/resourceVersion preconditions, and
  watch semantics behave as required;
- exact SelfSubjectAccessReview results for the caller;
- installed product API, controller, and KWOK compatibility revisions;
- requested Fidelity Mode capabilities;
- admission rejections and warnings.

Capability absence fails before accepting a Scenario Revision. A successful
version check never suppresses a failed API, authorization, admission, runtime,
or fidelity probe.

### RBAC boundary

Installation, submission, reconciliation, KWOK lifecycle, DRA projection, and
test workloads are different personas:

1. The **installer** creates and upgrades CRDs, namespaces, service accounts,
   roles, bindings, admission policy, and controllers. Product CLI commands do
   not inherit or request these privileges.
2. A **Scenario submitter** may create, get, watch, and conditionally update or
   delete product Scenario Instance resources; read the `kube-system`
   Namespace UID for target fingerprinting; discover APIs; and create
   SelfSubjectAccessReviews. It does not mutate Nodes, Leases, Pods, DRA
   inventory, RBAC, CRDs, webhooks, Namespaces, or Secrets.
3. The **Scenario reconciler** manages the product API, exact owned Synthetic
   Nodes and their status, and exact owned Leases. It may read Pods bound to
   those Nodes but may not delete or evict them.
4. The **KWOK controller** uses a separate pinned service account with only the
   Node, Pod status/lifecycle, Lease, Event, and selected KWOK API verbs needed
   by the maintained Stages. Its selectors require both the simulator
   managed-by identity and Scenario Instance identity.
5. The optional **DRA projector** role is bound only in installations that
   enable that Fidelity Mode. It manages the simulator's owned DeviceClasses
   and ResourceSlices and observes claims and Pods. Namespaced test claims and
   Pods are created by the End-to-End Test Harness under a separate identity.

Runtime roles contain no wildcard API groups, resources, or verbs.
`resourceNames` constraints are used where object names are static; dynamic
owned Node names remain protected by application ownership invariants. Runtime
roles have no access to Secrets, service-account token creation,
impersonation, TokenReviews, SubjectAccessReviews for other users, Pod
eviction, CRDs, RBAC, webhooks, or Namespace mutation.

Kubernetes RBAC cannot constrain Node or Pod operations by label. Selector and
ownership checks are therefore application invariants, not claims delegated to
RBAC. Every release safety job captures API audit evidence and before/after
snapshots proving that no product controller mutates a pre-existing real Node
or its Lease, and that cleanup never deletes or evicts an unowned Pod.

### Validation layers

#### Every pull request

The required fast path includes:

- unit tests for canonical Scenario parsing, defaulting, validation, digests,
  revision concurrency, output schemas, and typed diagnostics;
- schema and golden projection tests for every bundled Vendor Profile,
  Resource Contract, and Accelerator Model;
- deterministic reconciliation tests for retry, partial failure, ownership
  conflict, target mismatch, cleanup blocking, pagination, and bounded status;
- generated RBAC comparison and forbidden-verb assertions;
- race tests, static analysis, dependency policy, and reproducible manifest
  generation;
- `scheduling` end-to-end smoke tests on the 1.30 floor and current validated
  ceiling;
- `scheduling` end-to-end smoke tests on the oldest still upstream-maintained
  minor when that is neither the floor nor the ceiling;
- `dra-control-plane` smoke tests on the 1.34 DRA floor and current validated
  ceiling;
- negative tests for missing APIs, denied verbs, dry-run rejection, unowned
  objects, and an unavailable runtime.

#### Nightly

The nightly matrix runs `scheduling` against every compatible minor from 1.30
through the current ceiling. Each row exercises:

- client and server dry-run with zero persistent objects;
- single-Node/single-Accelerator, single-Node/multi-Accelerator, and
  multi-Node/multi-Accelerator Scenarios;
- heterogeneous Node Groups and at least two non-conflicting Resource
  Contracts;
- scheduler fit, exhaustion, affinity, taints, and unschedulable results;
- aggregate health loss and recovery;
- scale up and scale down through generation-guarded revisions;
- controller interruption and idempotent recovery;
- target, UID, generation, ownership, and real-Node safety conflicts;
- exact cleanup and absence of owned live objects.

Every supported DRA minor runs a separate `resource.k8s.io/v1` matrix covering
DeviceClass selection, ResourceSlice pool generation, claim allocation,
reservation and reuse, Pod placement, inventory change, deletion, and truthful
exclusion of node preparation and CDI.

Failures are not allowed to become a permanent `continue-on-error` row.
Quarantined flakes remove that row from the advertised matrix until fixed and
revalidated.

#### Release candidate

A release candidate requires:

- all pull-request and nightly rows green at the candidate commit;
- the real-kubelet Device Plugin protocol harness on the 1.30 floor and current
  ceiling, including registration, re-registration, ListAndWatch health,
  Allocate, and CDI response where the Kubernetes version supports it;
- the DRA node-runtime oracle on the 1.34 DRA floor and current ceiling, kept
  separate from the product's DRA control-plane claim;
- the two consecutive 1,000-Node/8,000-Accelerator scale trials from ADR 0004
  on the current ceiling;
- real-Node, foreign-Pod, target-fingerprint, interrupted-cleanup, and
  least-privilege safety audits;
- from the second product release onward, upgrade tests from the previous
  release with an existing Ready and a partially reconciling Scenario
  Instance;
- immutable CI validation receipts with provenance, recording inputs, digests,
  counts, durations, achieved fidelity, exclusions, and cleanup results.

The scale gate runs on the current ceiling rather than multiplying the full
1,000-Node profile across every historical minor. The all-minor nightly smoke
matrix provides the cross-version lifecycle evidence.

### Vendor Profile validation

Catalog breadth does not multiply the cluster matrix by every commercial
Accelerator Model. Models are data; distinct Kubernetes-visible Resource
Contract semantics are the behavioral units.

Every catalog change must:

- retain an Evidence Record with source URI, immutable revision or content
  digest, evidence grade, integration owner, and checked date;
- keep every default `verified` contract at Grade A;
- compile every Profile, contract variant, model binding, alias, and identity
  signal through schema and golden projection tests;
- reject duplicate or conflicting Kubernetes resource names, labels,
  attributes, variants, and model identities;
- exercise at least one end-to-end fixture for each distinct projection
  behavior, rather than one fixture per marketing model;
- keep provisional integrations opt-in and visibly attributed;
- leave source-unverified product names non-renderable.

The release freezes one catalog digest. Verified and provisional contract
evidence must have been reviewed within 180 days of the release candidate.
Failure to refresh a material contract blocks its inclusion in that release;
it does not silently invent a replacement or upgrade its evidence class.

### Adding a Kubernetes release

When Kubernetes publishes a new minor:

1. create a non-blocking candidate lane during upstream release candidates;
2. audit removed APIs, changed fields, scheduler behavior, DRA maturity,
   `client-go`, KWOK, and kind;
3. pin release-paired images and manifests by digest;
4. run all scheduling, DRA where applicable, safety, protocol, and scale gates;
5. update the compatibility lock and public matrix in a product release.

Only step 5 makes the new minor supported. A new Kubernetes minor does not
silently expand an already published product release.

## Consequences

- Kubernetes 1.30 remains a real, continuously tested scheduling compatibility
  floor even though it is no longer upstream-maintained.
- Users can distinguish “works with this product” from “receives Kubernetes
  security fixes”.
- The default scheduling implementation stays on long-stable APIs across the
  entire range.
- DRA support begins at its stable `resource.k8s.io/v1` contract instead of
  carrying four historical API adapters.
- Version discovery, capability discovery, permissions, admission, and runtime
  compatibility all fail closed before revision acceptance.
- Exact harness tuples and receipts make compatibility claims reproducible.
- Cross-version cost remains bounded: every minor gets lifecycle coverage,
  while the expensive reference scale profile runs on the current ceiling.
- Vendor catalog breadth is validated exhaustively as data and by distinct
  contract behavior, not by a wasteful model-by-version Cartesian product.

## Evidence

- [Kubernetes version skew and maintained-release policy](https://kubernetes.io/releases/version-skew-policy/)
- [Kubernetes releases](https://github.com/kubernetes/kubernetes/releases)
- [Kubernetes v1.30 resource APIs](https://github.com/kubernetes/kubernetes/tree/v1.30.14/staging/src/k8s.io/api/resource)
- [Kubernetes v1.31 resource APIs](https://github.com/kubernetes/kubernetes/tree/v1.31.14/staging/src/k8s.io/api/resource)
- [Kubernetes v1.32 resource APIs](https://github.com/kubernetes/kubernetes/tree/v1.32.13/staging/src/k8s.io/api/resource)
- [Kubernetes v1.33 resource APIs](https://github.com/kubernetes/kubernetes/tree/v1.33.13/staging/src/k8s.io/api/resource)
- [Kubernetes v1.34 stable resource APIs](https://github.com/kubernetes/kubernetes/tree/v1.34.10/staging/src/k8s.io/api/resource/v1)
- [Kubernetes API dry-run contract](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run)
- [Kubernetes SelfSubjectAccessReview](https://kubernetes.io/docs/reference/kubernetes-api/authorization-resources/self-subject-access-review-v1/)
- [client-go compatibility policy](https://github.com/kubernetes/client-go#compatibility-client-go---kubernetes-clusters)
- [KWOK v0.8.0 release](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0)
- [KWOK v0.8.0 recorded Kubernetes releases](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/supported_releases.txt)
- [kind v0.32.0 release images](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0)
- [Primary-source compatibility research](../research/kubernetes-compatibility-policy.md)
- [Compatibility and validation decision](https://github.com/LinkMaq/kube-accelerator-sim/issues/9)
- [Reference scale release gate](https://github.com/LinkMaq/kube-accelerator-sim/blob/main/docs/adr/0004-reference-scale-profile.md)
