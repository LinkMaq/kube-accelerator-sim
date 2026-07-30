# Kubernetes Compatibility and Validation Policy

Research date: 2026-07-30

Decision ticket: [Wayfinder issue #9](https://github.com/LinkMaq/kube-accelerator-sim/issues/9)

## Executive recommendation

Adopt a two-axis policy instead of treating “Kubernetes 1.30+” as one undifferentiated support claim:

1. **Product compatibility**
   - The `scheduling` Fidelity Mode supports Kubernetes minors 1.30 through 1.36, with the final or current patch of every minor used as the release-validation fixture.
   - The `dra-control-plane` Fidelity Mode supports Kubernetes 1.34 through 1.36 and only the stable `resource.k8s.io/v1` API.
   - Kubernetes versions newer than the maximum release-tested minor fail closed until a project release validates them. Versions older than 1.30 are unsupported.
2. **Upstream maintenance**
   - As of the research date, Kubernetes only maintains 1.34, 1.35, and 1.36. Kubernetes 1.30 through 1.33 are legacy compatibility targets that no longer receive normal upstream security or bug fixes. The CLI and release notes must say so explicitly. Kubernetes maintains only its three newest minor release branches and gives Kubernetes 1.19+ approximately one year of patch support. ([version-skew policy](https://kubernetes.io/releases/version-skew-policy/), [patch-release history](https://kubernetes.io/releases/patch-releases/))

Pin KWOK v0.8.0 and its installation artifacts by digest. Use kind v0.32.0 only in the End-to-End Test Harness, pairing each kind binary with a release-listed or project-built, digest-pinned node image. Do not fetch floating `latest` assets in installation or CI.

Use `client-go` from the newest project-validated Kubernetes minor, initially v0.36.x, while keeping all `k8s.io/*` modules on one patch line. The official client-go matrix says cross-version clients can use shared APIs but may contain objects absent from an older server, and alpha APIs may change or disappear in one release; therefore discovery and the project test matrix, not the library version alone, determine compatibility. ([client-go compatibility](https://github.com/kubernetes/client-go/blob/v0.36.3/README.md#compatibility-client-go---kubernetes-clusters))

## Compatibility snapshot

The current Kubernetes patch tags below are immutable upstream releases as of 2026-07-30. The DRA API column comes from each tagged `staging/src/k8s.io/api/resource` directory, not from an inferred semantic-version rule.

| Kubernetes minor | Current/final patch | Upstream state on 2026-07-30 | Resource API packages present | Project policy |
| --- | --- | --- | --- | --- |
| 1.30 | 1.30.14 | EOL 2025-07-15 | `v1alpha2` | `scheduling`; legacy/EOL warning |
| 1.31 | 1.31.14 | EOL 2025-11-11 | `v1alpha3` | `scheduling`; legacy/EOL warning |
| 1.32 | 1.32.13 | EOL 2026-02-28 | `v1alpha3`, `v1beta1` | `scheduling`; legacy/EOL warning |
| 1.33 | 1.33.13 | EOL 2026-06-28 | `v1alpha3`, `v1beta1`, `v1beta2` | `scheduling`; legacy/EOL warning |
| 1.34 | 1.34.10 | Active; EOL 2026-10-27 | `v1`, `v1alpha3`, `v1beta1`, `v1beta2` | `scheduling` and `dra-control-plane` through `v1` |
| 1.35 | 1.35.7 | Active; EOL 2027-02-28 | `v1`, `v1alpha3`, `v1beta1`, `v1beta2` | `scheduling` and `dra-control-plane` through `v1` |
| 1.36 | 1.36.3 | Active; EOL 2027-06-28 | `v1`, `v1alpha3`, `v1beta1`, `v1beta2` | `scheduling` and `dra-control-plane` through `v1` |

Sources:

- Patch lifecycle and EOL dates: [Kubernetes patch releases](https://kubernetes.io/releases/patch-releases/).
- Current patch tags: [v1.34.10](https://github.com/kubernetes/kubernetes/releases/tag/v1.34.10), [v1.35.7](https://github.com/kubernetes/kubernetes/releases/tag/v1.35.7), and [v1.36.3](https://github.com/kubernetes/kubernetes/releases/tag/v1.36.3).
- Tagged resource API trees: [v1.30.14](https://github.com/kubernetes/kubernetes/tree/v1.30.14/staging/src/k8s.io/api/resource), [v1.31.14](https://github.com/kubernetes/kubernetes/tree/v1.31.14/staging/src/k8s.io/api/resource), [v1.32.13](https://github.com/kubernetes/kubernetes/tree/v1.32.13/staging/src/k8s.io/api/resource), [v1.33.13](https://github.com/kubernetes/kubernetes/tree/v1.33.13/staging/src/k8s.io/api/resource), [v1.34.10](https://github.com/kubernetes/kubernetes/tree/v1.34.10/staging/src/k8s.io/api/resource), [v1.35.7](https://github.com/kubernetes/kubernetes/tree/v1.35.7/staging/src/k8s.io/api/resource), and [v1.36.3](https://github.com/kubernetes/kubernetes/tree/v1.36.3/staging/src/k8s.io/api/resource).

“Compatible with 1.30” must not be presented as “Kubernetes 1.30 is secure or upstream-supported.” The project can validate its own behavior, but it cannot backport Kubernetes control-plane or kubelet fixes. Release metadata should expose both `projectTested` and `upstreamState`.

The test fixture is the latest/final patch of each minor. Other patch releases in a claimed minor are not rejected solely by patch number when discovery succeeds, but they are reported as `not-project-tested`; the CLI should recommend the tested patch and may deny a patch with a documented incompatibility.

## client-go policy

The Kubernetes component version-skew policy does not define a support window for arbitrary custom clients; its explicit skew rules cover Kubernetes components and `kubectl`. A simulator built with client-go therefore needs its own compatibility evidence. ([Kubernetes version-skew policy](https://kubernetes.io/releases/version-skew-policy/))

Use these rules:

- Pin `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go`, and other Kubernetes modules to one v0.36.x patch line; initially use the patch corresponding to the newest release-tested 1.36 patch.
- Upgrade that dependency line only with a full compatibility-matrix run.
- Use typed stable clients for `core/v1`, `coordination.k8s.io/v1`, and `authorization.k8s.io/v1`.
- Use only typed `resource.k8s.io/v1` objects for `dra-control-plane`. Do not compile product behavior against the legacy alpha/beta DRA packages.
- Discover served resources and verbs before choosing behavior. A type being compiled into client-go does not prove that the Simulation Target serves or enables it.
- Keep protobuf/JSON negotiation on client-go defaults for stable built-in types, but run all preflight and behavior tests against real API servers; client-go fake clients do not exercise admission, authorization, scheduler behavior, Server-Side Apply, or subresource semantics.

The client-go README defines an exact-version checkmark only when client and cluster have the same API objects. Its `+` and `-` cells mean common APIs generally work while one side may have additional or removed objects, with a specific warning that alpha APIs can change significantly in a single release. It also states that the Kubernetes tag does not itself claim backward compatibility. ([v0.36.3 README](https://github.com/kubernetes/client-go/blob/v0.36.3/README.md))

KWOK v0.8.0 independently builds against `k8s.io/*` v0.36.1. That fact is a dependency baseline, not proof of every older server combination. ([KWOK v0.8.0 `go.mod`](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/go.mod))

## `scheduling` baseline from Kubernetes 1.30 onward

The default Fidelity Mode needs only APIs that are stable and served throughout the claimed range:

| Surface | Resource or endpoint | Required behavior |
| --- | --- | --- |
| Synthetic Node identity and scheduling gate | `core/v1`, `nodes` | Create, read, watch, patch/update, and ownership-bounded delete of Synthetic Nodes. |
| Accelerator capacity and health aggregate | `core/v1`, `nodes/status` | Patch both `status.capacity` and `status.allocatable`; a Synthetic Node has no kubelet to copy capacity into allocatable. |
| Ready and node status | `core/v1`, `nodes/status` | Observe Ready and the fields needed by scheduler/controller behavior. |
| Heartbeat | `coordination.k8s.io/v1`, `leases` in `kube-node-lease` | Create and renew the Synthetic Node's same-name Lease. |
| Scheduler observation | `core/v1`, `pods`, plus owned test Pod status when applicable | Observe binding, Pending reasons, resource exhaustion, and KWOK-simulated lifecycle without claiming a real kubelet. |
| Authorization preflight | `authorization.k8s.io/v1`, `selfsubjectaccessreviews` | Check each exact operation before dry-run or persistence. |
| Product and KWOK installation | `apiextensions.k8s.io/v1`, `apps/v1`, `rbac.authorization.k8s.io/v1`; KWOK v0.8.0 also installs `flowcontrol.apiserver.k8s.io/v1` | Installation workflow only; `kasim apply` does not install these implicitly. |

Kubernetes documents Node capacity and allocatable as Node status, and documents Node status plus a same-name Lease in `kube-node-lease` as the two heartbeat forms. Lease updates are independent and are the lower-cost heartbeat path. ([Node status](https://kubernetes.io/docs/reference/node/node-status/), [Lease behavior](https://kubernetes.io/docs/concepts/architecture/leases/))

Kubernetes also documents direct `PATCH /api/v1/nodes/{name}/status` as the way to advertise an extended resource. Extended resources are opaque, integer-valued, and non-overcommittable; the scheduler accounts for them from Pod requests. ([advertising an extended resource](https://kubernetes.io/docs/tasks/administer-cluster/extended-resource-node/), [resource accounting](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources), [Node status operations](https://kubernetes.io/docs/reference/kubernetes-api/core-resources/node-v1/#patch-status))

No Device Plugin, CDI, DRA, kubelet PodResources, or node-runtime API is part of the `scheduling` compatibility floor.

## DRA evolution and product gate

### Upstream evolution

- Kubernetes 1.30 served `resource.k8s.io/v1alpha2`; structured parameters were introduced as alpha work, while the original/classic DRA design was still present. ([v1.30 resource source](https://github.com/kubernetes/kubernetes/tree/v1.30.14/staging/src/k8s.io/api/resource/v1alpha2), [1.30 structured-parameters announcement](https://kubernetes.io/blog/2024/03/12/kubernetes-1-30-upcoming-changes/#structured-parameters-for-dynamic-resource-allocation-kep-4381))
- Kubernetes 1.31 introduced the redesigned `v1alpha3` API. Classic allocation moved behind a separate `DRAControlPlaneController` gate; the structured scheduler allocation remained alpha and disabled by default. ([1.31 release announcement](https://kubernetes.io/blog/2024/08/13/kubernetes-v1-31-release/#new-dra-apis-for-better-accelerators-and-other-hardware-management), [v1.31 feature definitions](https://github.com/kubernetes/kubernetes/blob/v1.31.14/pkg/features/kube_features.go), [v1alpha3 source](https://github.com/kubernetes/kubernetes/tree/v1.31.14/staging/src/k8s.io/api/resource/v1alpha3))
- Kubernetes 1.32 promoted the redesigned core to beta as `v1beta1`, still disabled by default. The old classic implementation was withdrawn; `v1alpha3` remained for other compatibility/feature surfaces. ([1.32 DRA documentation](https://v1-32.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/), [1.32 release announcement](https://kubernetes.io/blog/2024/12/11/kubernetes-v1-32-release/#structured-parameter-support), [v1.32 source tree](https://github.com/kubernetes/kubernetes/tree/v1.32.13/staging/src/k8s.io/api/resource))
- Kubernetes 1.33 kept core DRA beta. The tagged API source contains `v1beta1` and `v1beta2` plus `v1alpha3`; additional features had independent maturity and gates. ([1.33 DRA update](https://kubernetes.io/blog/2025/05/01/kubernetes-v1-33-dra-updates/), [v1.33 source tree](https://github.com/kubernetes/kubernetes/tree/v1.33.13/staging/src/k8s.io/api/resource), [v1.33 feature definitions](https://github.com/kubernetes/kubernetes/blob/v1.33.13/pkg/features/kube_features.go))
- Kubernetes 1.34 promoted core structured DRA and `resource.k8s.io/v1` to stable and enabled it by default, although the `DynamicResourceAllocation` gate could still be disabled in 1.34. ([1.34 DRA GA announcement](https://kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/), [1.34 DRA documentation](https://v1-34.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/), [v1.34 feature definitions](https://github.com/kubernetes/kubernetes/blob/v1.34.10/pkg/features/kube_features.go))
- Kubernetes 1.35 locked core DRA enabled; Kubernetes 1.36 continues to serve stable `resource.k8s.io/v1`. Optional DRA capabilities such as health, extended-resource mapping, consumable capacity, device taints, binding conditions, and partitionable devices have their own version and feature-gate histories and are not implied by core DRA. ([1.35 release](https://kubernetes.io/blog/2025/12/17/kubernetes-v1-35-release/#continued-innovation-in-dynamic-resource-allocation-dra), [current feature gates](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#feature-gates), [1.36 DRA update](https://kubernetes.io/blog/2026/05/07/kubernetes-v1-36-dra-136-updates/))

### Product decision

`dra-control-plane` requires all of the following:

1. Kubernetes server minor 1.34 or newer and no newer than the release-tested maximum.
2. Discovery of `resource.k8s.io/v1` with the required verbs for `DeviceClass`, `ResourceSlice`, `ResourceClaim`, and any owned claim template used by the test path.
3. The stable v1 fields required by the selected Vendor Profile and Scenario Revision.
4. Successful exact-operation SelfSubjectAccessReviews and server dry-runs.
5. A real allocation probe that observes scheduler-written allocation/reservation and Pod binding where the Scenario's acceptance check requires them.

Kubernetes 1.30–1.33 return `UnsupportedFidelity` for `dra-control-plane` even when an alpha or beta resource API is enabled. This avoids maintaining schemas that Kubernetes substantially redesigned and keeps the first DRA contract on a stable API.

Discovery is necessary but not sufficient: a custom scheduler configuration or missing controller behavior can still prevent allocation. A successful `ResourceSlice` write proves inventory publication, not allocation, node preparation, CDI injection, or device health. Those exclusions remain part of the Fidelity Mode receipt.

Optional DRA fields are disabled unless a later decision gives each one a minimum Kubernetes version, required feature gates, source-backed schema, permission set, and E2E assertion. Version-number guessing is not a substitute for discovery and observation.

## KWOK compatibility and installation

KWOK v0.8.0 is the latest formal release as of the research date, published on 2026-06-23. Its controller image is `registry.k8s.io/kwok/kwok:v0.8.0`. ([v0.8.0 release](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0))

Pin these official release assets:

| Asset | Purpose | SHA-256 |
| --- | --- | --- |
| [`kwok.yaml`](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml) | Controller, CRDs, RBAC, Deployment, and FlowSchema | `a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405` |
| [`stage-fast.yaml`](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml) | Required fast Node/Pod lifecycle Stages | `2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24` |
| [`metrics-usage.yaml`](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/metrics-usage.yaml) | Optional usage metrics; not part of the first compatibility floor | `880e46f117cc83587a210aaacc9c6b49ff55aaf455648f497281634377d09437` |

The upstream in-cluster instructions install `kwok.yaml` and `stage-fast.yaml`, with metrics optional. v0.8.0 removed the built-in default Stage, so `kwok.yaml` alone is not a functional default lifecycle installation. ([in-cluster installation](https://kwok.sigs.k8s.io/docs/user/kwok-in-cluster/), [v0.8.0 release changes](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0))

The v0.8.0 release publishes all-in-one cluster tags for Kubernetes 1.31.14 through 1.36.1, while its tagged `supported_releases.txt` also lists 1.30.14. Those artifacts are useful evidence but are not an explicit certification that the in-cluster manifests work on every listed server. The project must run its own KWOK-in-existing-cluster test for every claimed minor, especially 1.30. ([release image list](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0), [`supported_releases.txt`](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/supported_releases.txt), [all-in-one image contract](https://kwok.sigs.k8s.io/docs/user/all-in-one-image/))

Installation policy:

- Product installation is separate from `kasim apply`; apply must fail with `RuntimeUnavailable` before persistent Scenario writes when a compatible runtime is absent.
- Vendor or mirror the exact release manifests and record their digests in a compatibility lock. Never resolve `releases/latest` during install or test.
- Resolve and pin the multi-architecture KWOK image digest in the project release lock; an image tag alone is not a reproducible supply-chain identity.
- Install only the required Stage set and audit the upstream ClusterRole. KWOK v0.8.0 needs cluster-scoped Node/Pod status, Lease, and Stage access; Kubernetes RBAC cannot restrict Node status writes by the project's ownership label.
- Verify the installed KWOK version, manifest digest, Stage digest, controller readiness, and selector configuration during runtime preflight.
- Treat a newer or older KWOK installation as incompatible until its manifest and behavior matrix passes; do not infer compatibility from semver.

KWOK's Helm chart in this release is chart version 0.3.0, not 0.8.0. If Helm is later selected, lock chart version, rendered-manifest digest, application image digest, and values independently. ([v0.8.0 assets](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0))

## kind policy for the End-to-End Test Harness

kind v0.32.0 is the latest formal release as of the research date, published on 2026-06-02. It defaults to Kubernetes 1.36.1 and publishes these exact node images:

| Kubernetes | Release-paired image |
| --- | --- |
| 1.36.1 | `kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5` |
| 1.35.5 | `kindest/node:v1.35.5@sha256:ce977ae6d65918d0b58a5f8b5e940429c2ce42fa3a5619ec2bbc60b949c0ac95` |
| 1.34.8 | `kindest/node:v1.34.8@sha256:02722c2dedddcfc00febf5d27fbeb9b7b2c14294c82109ff4a85d89ac9ba3256` |
| 1.33.12 | `kindest/node:v1.33.12@sha256:3f5c8443c620245e4d355cfe09e96a91ead32ceaa569d3f1ca9edf0cb2fe2ff4` |

The release requires digest pinning, says the new images require kind v0.32.0 or newer for `kind load`, and does not guarantee full node-image compatibility across kind releases. The images are multi-architecture, but the image platform must match the host. ([kind v0.32.0 release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0), [node-image contract](https://kind.sigs.k8s.io/docs/design/node-image/))

Use kind as follows:

- The product CLI never calls kind. Only the End-to-End Test Harness creates and destroys kind clusters.
- Lock the tuple `(kind version, kind binary SHA-256, complete node image reference with digest, host architecture, container provider)`.
- Use release-paired images for the protocol oracle where possible.
- For exact Kubernetes patch fixtures not published by the current kind release, build a node image from the immutable Kubernetes tag with the pinned kind version, publish it to the project CI registry, and lock its digest. Building node images from Kubernetes source is an official kind workflow. ([kind quick start](https://kind.sigs.k8s.io/docs/user/quick-start/#building-images))
- Do not call a custom-built image “kind release-paired”; record it as `project-built` with source tag, build provenance, and digest.
- kind itself does not provide fake Accelerator behavior. Its project scope classifies Device Plugin testing such as GPU as extended testing that needs additional design, so the fake Device Plugin, mock DRA driver, and CDI fixtures remain project-owned. ([kind project scope](https://kind.sigs.k8s.io/docs/contributing/project-scope/#p3-extended-testing-not-covered-above))

## Preflight, dry-run, apply, and RBAC policy

### Capability discovery

Preflight must record:

- server semantic version as diagnostic data;
- discovered GroupVersionResources, subresources, namespaced scope, and verbs;
- target fingerprint;
- installed product runtime and KWOK receipts;
- exact DRA v1 availability when requested.

Behavior is gated by discovery and observations, not only by `gitVersion`. Version remains necessary for the declared compatibility window and for a useful error.

### SelfSubjectAccessReview

Create one `authorization.k8s.io/v1` `SelfSubjectAccessReview` for every exact planned tuple of verb, group, resource, subresource, namespace, and resource name where applicable. Kubernetes documents SSAR as the mechanism used by `kubectl auth can-i` and as an API that evaluates the current user. ([authorization checks](https://kubernetes.io/docs/reference/access-authn-authz/authorization/#checking-api-access), [SSAR API](https://kubernetes.io/docs/reference/kubernetes-api/authorization-resources/self-subject-access-review-v1/))

Rules:

- `allowed=false`, `denied=true`, or a non-empty evaluation error rejects preflight.
- An allowed SSAR proves only the current authorization decision. It does not prove admission success, object ownership, lack of a race, or future authorization.
- Check subresources explicitly, including `nodes/status` and any owned status subresource.
- Check the CLI persona, runtime controller persona, and KWOK persona separately; do not infer one from another.

### Server-side dry-run

After SSAR and ownership reads, repeat every representative mutating request with `dryRun=All`, the same body, field manager, subresource, and strict field validation intended for the real write.

Kubernetes stable dry-run runs defaulting, mutating and validating admission, patch merge, and schema validation but skips persistence. It guarantees that the request is not stored; a matching admission webhook declaring `Some` or `Unknown` side effects causes the dry-run request to fail rather than risk side effects. ([API dry-run contract](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run), [webhook `sideEffects`](https://kubernetes.io/docs/reference/kubernetes-api/extend-resources/validating-webhook-configuration-v1/))

Treat dry-run failure as a preflight rejection. Do not bypass a webhook that cannot safely dry-run. Dry-run does not reserve a name, UID, generation, resourceVersion, capacity, or scheduler outcome, so the persistent operation must repeat target, generation, ownership, and conflict checks.

Dry-run is also not a transaction across dependent objects. For example, a dry-run Node create is not persisted, so a subsequent dry-run against that new Node's `/status` endpoint cannot validate the whole create-then-status sequence. Preflight should dry-run every operation that is independently addressable, validate the remaining subresource bodies against an already owned probe object in CI, and rely on the real reconciliation receipt for the ordered multi-object outcome.

### Server-Side Apply

Server-Side Apply is stable since Kubernetes 1.22. It tracks field managers and rejects a conflicting apply unless the caller forces ownership transfer. ([SSA contract](https://kubernetes.io/docs/reference/using-api/server-side-apply/))

Policy:

- Use stable, operation-specific field-manager names for the Scenario Instance root, Synthetic Node spec/metadata, Accelerator capacity status, DRA inventory, and cleanup gates.
- Apply only fields owned by that workflow.
- Never use `force=true` against an existing object in a Simulation Target. A conflict is an ownership or reconciliation error, not permission to take over.
- Do not use SSA as proof that an object belongs to the Scenario Instance; exact Instance UID and the ownership inventory remain mandatory.
- Use the `/status` subresource for Node capacity, allocatable, and conditions. Separate field managers and tests must demonstrate that the simulator's Accelerator map entries coexist with KWOK's Ready/heartbeat status updates.
- Use strict field validation. Unknown or duplicate fields reject preflight instead of being silently dropped.

### RBAC

Generate a permission manifest from the selected Fidelity Mode and installation profile; do not use wildcard verbs, API groups, or resources. Kubernetes warns that wildcard RBAC entries can grant access to resources added later. `resourceNames` can restrict named access, but it cannot restrict `create`, and list/watch with `resourceNames` requires a matching `metadata.name` field selector. It does not provide label-scoped protection for `nodes/status`. ([RBAC restrictions](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#referring-to-resources))

Separate personas:

1. **Installer**: CRDs, Deployment, ServiceAccount, RBAC, KWOK Stages, and any FlowSchema needed by the pinned installation.
2. **CLI user**: product Scenario Instance lifecycle and read-only capability/receipt operations.
3. **Runtime controller**: exact owned Node, Lease, DRA, and status operations required by the selected Fidelity Mode.
4. **KWOK**: the audited permissions in the pinned KWOK installation.

Because Kubernetes RBAC cannot express “patch only Nodes with this Instance UID label,” ownership enforcement, admission policy where available, mixed-cluster tests, and audit-log assertions are release-blocking safety controls.

## CI, E2E, and release matrix

### Required lanes

| Lane | Cadence | Kubernetes matrix | Required assertions |
| --- | --- | --- | --- |
| Unit/schema | Every PR | No cluster | Scenario canonicalization, version policy, discovery decisions, DRA v1-only gate, permission planning, ownership, and receipts. |
| PR compatibility | Every PR | 1.30.14, 1.34.10, 1.36.3 | `scheduling` on minimum/oldest-active/newest-active; `dra-control-plane` on 1.34 and 1.36; install receipt, explicit target, apply/status/revision/delete, mixed real/Synthetic Node safety, dry-run, SSA conflict, SSAR denial, and zero owned-object leaks. |
| Full minor matrix | Nightly and release | 1.30.14, 1.31.14, 1.32.13, 1.33.13, 1.34.10, 1.35.7, 1.36.3 | `scheduling` behavior and cleanup on every claimed minor; legacy/EOL warning on 1.30–1.33. |
| DRA stable matrix | Nightly and release | 1.34.10, 1.35.7, 1.36.3 | Only `resource.k8s.io/v1`; class/slice discovery, allocation/reservation, binding, deletion, device reuse, and truthful node-runtime exclusions. |
| DRA negative matrix | Nightly and release | 1.30.14, 1.31.14, 1.32.13, 1.33.13 | `dra-control-plane` fails before persistent writes and reports the discovered non-v1 APIs. |
| Node-runtime protocol oracle | Weekly and release | kind v0.32.0 + release-paired 1.36.1 image, then advance deliberately | Real kubelet registration/re-registration, `ListAndWatch`, health, `Allocate`, DRA prepare/unprepare, and CDI fixture assertions; still no real Accelerator computation claim. |
| Upcoming Kubernetes | Weekly, non-blocking | 1.37 prerelease/CI image once available | Discovery, install, and smoke tests only; never expands the public support range before a release gate passes. |
| Reference scale | Release candidate | Newest active tested patch, initially 1.36.3 | Preserve the two-consecutive-trial 1,000 Synthetic Node / 8,000 Accelerator gates in [ADR 0004](../adr/0004-reference-scale-profile.md). |

Use project-built, digest-pinned kind node images for exact patch combinations absent from kind v0.32.0's release list. Cache them by source commit and build recipe; never silently replace a digest under an existing compatibility key.

If the runtime image is released for both amd64 and arm64, make Linux amd64 the complete required matrix and add at least newest-active `scheduling` plus install/cleanup smoke on Linux arm64 before release. kind's node images support both platforms but require host-platform matching. macOS validates the CLI's local parsing and kubeconfig path behavior; in-cluster runtime behavior remains a Linux cluster test.

### Release rules

- A product release publishes `minMinor`, `maxTestedMinor`, every exact tested patch, upstream lifecycle state, supported Fidelity Modes, client-go line, KWOK asset/image lock, and kind fixture lock.
- A failed legacy lane means the release cannot continue claiming that minor. It must either fix the regression or make a separate explicit support-policy decision; it cannot hide the failure behind the minor's EOL status.
- A new Kubernetes minor begins as experimental. It becomes supported only after installation, full `scheduling`, DRA where applicable, negative safety, cleanup, and upgrade tests pass.
- At each Kubernetes minor release, recompute the active three-minor window from upstream data; do not hard-code “1.34–1.36” as a permanent security-support statement.
- Release validation tests an upgrade from the previous project runtime/CRD/KWOK lock on the oldest active and newest active Kubernetes minors. No automatic KWOK upgrade occurs during Scenario apply.
- The release job verifies downloaded artifact SHA-256 values, image digests, served APIs, and installed receipts before behavioral tests.

## Compatibility receipt

Record at least:

```yaml
compatibility:
  checkedAt: "2026-07-30"
  kubernetes:
    serverVersion: v1.36.3
    upstreamState: active
    projectState: project-tested
  fidelity:
    requested: scheduling
    resourceAPI: null
  clientGo: v0.36.3
  kwok:
    version: v0.8.0
    manifestSHA256: a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405
    stageSHA256: 2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24
    image: registry.k8s.io/kwok/kwok@sha256:<resolved-release-digest>
  harness:
    kindVersion: v0.32.0
    nodeImage: kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5
```

The kind fields appear only in End-to-End Test Harness evidence, never in a product Scenario Instance running on an existing Simulation Target.

## Limitations

- This policy does not certify managed Kubernetes distributions; admission, disabled APIs, custom schedulers, and provider policy still require per-target preflight and observation.
- It does not make EOL Kubernetes releases secure.
- It does not claim that KWOK's all-in-one image list is a server compatibility certification.
- It does not support alpha or beta DRA APIs in the product.
- It does not turn DRA control-plane success into evidence of node preparation, CDI, health gRPC, or container access.
- Exact image digests for the project-built kind fixtures and the multi-architecture KWOK image must be resolved and committed by the release implementation; they cannot be inferred from tags.

## Exact primary sources checked

### Kubernetes lifecycle and clients

- [Kubernetes Version Skew Policy](https://kubernetes.io/releases/version-skew-policy/)
- [Kubernetes Patch Releases](https://kubernetes.io/releases/patch-releases/)
- [Kubernetes v1.34.10 release](https://github.com/kubernetes/kubernetes/releases/tag/v1.34.10)
- [Kubernetes v1.35.7 release](https://github.com/kubernetes/kubernetes/releases/tag/v1.35.7)
- [Kubernetes v1.36.3 release](https://github.com/kubernetes/kubernetes/releases/tag/v1.36.3)
- [client-go v0.36.3 README](https://github.com/kubernetes/client-go/blob/v0.36.3/README.md)

### Scheduling, apply, and authorization

- [Node Status](https://kubernetes.io/docs/reference/node/node-status/)
- [Leases](https://kubernetes.io/docs/concepts/architecture/leases/)
- [Advertise Extended Resources for a Node](https://kubernetes.io/docs/tasks/administer-cluster/extended-resource-node/)
- [Resource Management: Extended Resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources)
- [Node v1 API](https://kubernetes.io/docs/reference/kubernetes-api/core-resources/node-v1/)
- [Kubernetes API Concepts: Dry-run](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run)
- [Server-Side Apply](https://kubernetes.io/docs/reference/using-api/server-side-apply/)
- [Authorization: Checking API Access](https://kubernetes.io/docs/reference/access-authn-authz/authorization/#checking-api-access)
- [SelfSubjectAccessReview v1](https://kubernetes.io/docs/reference/kubernetes-api/authorization-resources/self-subject-access-review-v1/)
- [Using RBAC Authorization](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
- [ValidatingWebhookConfiguration v1](https://kubernetes.io/docs/reference/kubernetes-api/extend-resources/validating-webhook-configuration-v1/)

### DRA

- [Kubernetes 1.30 structured-parameters announcement](https://kubernetes.io/blog/2024/03/12/kubernetes-1-30-upcoming-changes/#structured-parameters-for-dynamic-resource-allocation-kep-4381)
- [Kubernetes 1.31 release: new DRA APIs](https://kubernetes.io/blog/2024/08/13/kubernetes-v1-31-release/#new-dra-apis-for-better-accelerators-and-other-hardware-management)
- [Kubernetes 1.32 DRA documentation](https://v1-32.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
- [Kubernetes 1.32 release: structured parameters](https://kubernetes.io/blog/2024/12/11/kubernetes-v1-32-release/#structured-parameter-support)
- [Kubernetes 1.33 DRA update](https://kubernetes.io/blog/2025/05/01/kubernetes-v1-33-dra-updates/)
- [Kubernetes 1.34 DRA GA](https://kubernetes.io/blog/2025/09/01/kubernetes-v1-34-dra-updates/)
- [Kubernetes 1.34 DRA documentation](https://v1-34.docs.kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
- [Kubernetes 1.35 release DRA section](https://kubernetes.io/blog/2025/12/17/kubernetes-v1-35-release/#continued-innovation-in-dynamic-resource-allocation-dra)
- [Kubernetes 1.36 DRA update](https://kubernetes.io/blog/2026/05/07/kubernetes-v1-36-dra-136-updates/)
- [Current Kubernetes feature gates](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/)
- Tagged Kubernetes resource API and feature source for [1.30.14](https://github.com/kubernetes/kubernetes/tree/v1.30.14/staging/src/k8s.io/api/resource), [1.31.14](https://github.com/kubernetes/kubernetes/tree/v1.31.14/staging/src/k8s.io/api/resource), [1.32.13](https://github.com/kubernetes/kubernetes/tree/v1.32.13/staging/src/k8s.io/api/resource), [1.33.13](https://github.com/kubernetes/kubernetes/tree/v1.33.13/staging/src/k8s.io/api/resource), [1.34.10](https://github.com/kubernetes/kubernetes/tree/v1.34.10/staging/src/k8s.io/api/resource), [1.35.7](https://github.com/kubernetes/kubernetes/tree/v1.35.7/staging/src/k8s.io/api/resource), and [1.36.3](https://github.com/kubernetes/kubernetes/tree/v1.36.3/staging/src/k8s.io/api/resource)

### KWOK

- [KWOK v0.8.0 release](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0)
- [KWOK v0.8.0 `go.mod`](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/go.mod)
- [KWOK v0.8.0 supported releases](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/supported_releases.txt)
- [KWOK in-cluster installation](https://kwok.sigs.k8s.io/docs/user/kwok-in-cluster/)
- [KWOK all-in-one image contract](https://kwok.sigs.k8s.io/docs/user/all-in-one-image/)
- [KWOK v0.8.0 `kwok.yaml`](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml)
- [KWOK v0.8.0 `stage-fast.yaml`](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml)
- [KWOK v0.8.0 `metrics-usage.yaml`](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/metrics-usage.yaml)

### kind

- [kind v0.32.0 release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0)
- [kind node-image support contract](https://kind.sigs.k8s.io/docs/design/node-image/)
- [kind quick start and node-image build](https://kind.sigs.k8s.io/docs/user/quick-start/#building-images)
- [kind project scope](https://kind.sigs.k8s.io/docs/contributing/project-scope/#p3-extended-testing-not-covered-above)
