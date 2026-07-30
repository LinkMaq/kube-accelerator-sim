# KWOK v0.8.0 Runtime Lock Verification

Research date: 2026-07-30

Decision tickets:
[compatibility policy #9](https://github.com/LinkMaq/kube-accelerator-sim/issues/9)
and [runtime implementation #21](https://github.com/LinkMaq/kube-accelerator-sim/issues/21)

## Verification result

Use the following tuple as the initial KWOK runtime lock:

| Surface | Locked value | Verification |
| --- | --- | --- |
| Upstream release | `v0.8.0` | Published 2026-06-23; not an immutable GitHub Release |
| Source commit | `156033d7df7ea0e09cea82b715fe566ea68aeeb4` | The official tag-ref API resolves the lightweight tag directly to this commit |
| `kwok.yaml` | `sha256:a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405` | GitHub asset digest and independently calculated SHA-256 agree |
| `stage-fast.yaml` | `sha256:2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24` | GitHub asset digest and independently calculated SHA-256 agree |
| Controller image | `registry.k8s.io/kwok/kwok@sha256:6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0` | Official registry header, raw OCI index hash, and Docker inspection agree |
| Managed-node selector | Annotation selector `kwok.x-k8s.io/node=fake` | Explicit in the release asset; the label selector is empty |
| Node Lease duration | `40s` | Explicit `nodeLeaseDurationSeconds: 40` in the release asset |
| Fast Stages | Exactly five: `node-heartbeat-with-lease`, `node-initialize`, `pod-complete`, `pod-delete`, and `pod-ready` | Parsed from the digest-verified release asset |

These values are mutually complementary. The source commit identifies the
audited source tree, the two file digests identify the bytes installed into a
Simulation Target, and the OCI digest identifies the image pulled by the
cluster. None is a substitute for the others.

## Evidence boundary

This report separates three kinds of statement:

1. **Upstream fact** is directly present in the official KWOK GitHub API,
   commit-pinned source or documentation, release bytes, or
   `registry.k8s.io`.
2. **Project conclusion** is a reproducibility or safety decision derived from
   those facts.
3. **Not established** identifies a relationship the inspected sources do not
   prove.

The important boundary is that the source commit, release assets, and OCI
image are independently identifiable. The checks below do not by themselves
provide a cryptographic build-provenance chain proving that the release
assets and image were produced from that source commit. The project must not
describe the tuple as such a provenance proof unless it separately verifies
signed build attestations.

## Source and release identity

### Upstream facts

The official Git tag-ref API returned a direct commit object:

```text
refs/tags/v0.8.0
object.type = commit
object.sha  = 156033d7df7ea0e09cea82b715fe566ea68aeeb4
```

The referenced commit is named `Release 0.8 (#1674)`, has tree
`e21ee89e8fb5b751bd01b8237e3fa86a3cc89bcd`, and the GitHub commit API reports
valid verification.

The official Release API returned:

```text
tag_name         = v0.8.0
target_commitish = main
draft            = false
prerelease       = false
immutable        = false
published_at     = 2026-06-23T03:30:37Z
```

`target_commitish: main` is Release metadata, not the source identity used by
this lock. The tag-ref resolution to the full commit is the relevant observed
mapping.

### Project conclusion

The Release API's `immutable: false` means the project cannot treat the
Release page, tag-named asset URLs, or the version string as a content lock.
It must:

- record the full source commit;
- verify every downloaded asset against the expected content digest before
  use; and
- deploy the controller image by OCI digest, not by tag alone.

The tag-ref mapping is also checked rather than merely trusted. A future
verification that resolves `v0.8.0` to any other commit must fail even if the
version text still reads `v0.8.0`.

## Release asset lock

### Upstream facts

The GitHub Release API advertises these assets:

| Asset | Size | GitHub digest | Independently calculated |
| --- | ---: | --- | --- |
| `kwok.yaml` | 108,286 bytes | `sha256:a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405` | Match |
| `stage-fast.yaml` | 7,564 bytes | `sha256:2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24` | Match |

Both files were downloaded from the official v0.8.0 Release and checked with
SHA-256. The independently calculated values exactly matched the asset
`digest` fields returned by GitHub.

The commit-pinned [in-cluster installation
guide](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/site/content/en/docs/user/kwok-in-cluster.md#L24-L52)
requires applying `kwok.yaml` and then `stage-fast.yaml`. It calls the Stage
installation required for Pod and Node simulation behavior.

### Project conclusion

Installation must fetch or vendor these exact bytes and verify SHA-256 before
rendering or applying them. A successful download from an expected filename is
not sufficient.

The upstream `kwok.yaml` contains:

```yaml
image: registry.k8s.io/kwok/kwok:v0.8.0
```

Therefore, verifying only the manifest digest still leaves the image reference
tag-based. The project-owned installation rendering must replace that image
reference with the locked `@sha256:` reference and verify the final rendered
Deployment. That rendering is a deliberate project transformation; its output
needs its own release fixture or digest.

## OCI image lock

### Upstream facts

A direct request to the official registry manifest endpoint with the OCI index
media type returned:

```text
content-type: application/vnd.oci.image.index.v1+json
docker-content-digest: sha256:6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0
```

Calculating SHA-256 over the returned raw index bytes produced the same value.
`docker buildx imagetools inspect` independently reported the same index
digest.

The index contains two runnable Linux platform manifests:

| Platform | Manifest digest |
| --- | --- |
| `linux/amd64` | `sha256:28ee38abba19bd0b89600b1b367c32480da45f1d954c80d14db5fc74feee83f2` |
| `linux/arm64` | `sha256:b9de5357c7cb1312728b17bbb1bb703e463c6b5306a2b5af266c2cfee60f1c2c` |

The index also contains two `unknown/unknown` manifests annotated as
`attestation-manifest`. They are attestations, not additional runnable
platforms. The Arm manifest declares `linux/arm64` without a `variant`, so the
lock must not invent an `arm64/v8` claim.

### Project conclusion

The runtime reference is:

```text
registry.k8s.io/kwok/kwok@sha256:6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0
```

Pinning the top-level OCI index preserves the verified multi-architecture
selection and attestations as one content-addressed object. A per-platform
digest may be recorded in validation evidence, but it must not silently
replace the release-level multi-architecture lock.

## Runtime behavior locked by the assets

### Managed Synthetic Node selection

The digest-verified `kwok.yaml` contains:

```yaml
manageAllNodes: false
manageNodesWithAnnotationSelector: 'kwok.x-k8s.io/node=fake'
manageNodesWithLabelSelector: ''
manageSingleNode: ''
```

This is an **annotation selector**, not a label selector. The commit-pinned
[node-management
guide](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/site/content/en/docs/user/kwok-manage-nodes-and-pods.md#L21-L31)
documents the two modes separately, and its Synthetic Node example uses:

```yaml
metadata:
  annotations:
    kwok.x-k8s.io/node: fake
```

The upstream fact is the KWOK selector and its annotation semantics. The
project conclusion is that every project-created Synthetic Node intended for
this controller must carry that annotation, while the Scenario Instance's
separate project ownership metadata continues to bound reconciliation and
cleanup. The KWOK annotation alone is not a project ownership proof.

### Lease duration and renewal cadence

The digest-verified `kwok.yaml` contains:

```yaml
nodeLeaseDurationSeconds: 40
```

The commit-pinned configuration type defines this field as the duration put on
the corresponding Lease. The controller implementation computes:

```text
lease duration       = 40s
renew interval base  = lease duration / 4 = 10s
renew jitter factor  = 0.04
```

See the [field
definition](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/pkg/apis/config/v1alpha1/kwok_configuration_types.go#L142-L143),
the [renewal calculation](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/pkg/kwok/controllers/controller.go#L248-L262),
and the [jittered
interval](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/pkg/kwok/controllers/node_lease_controller.go#L150-L151).

Accordingly, `40s` must be named **Lease duration**, not “Lease interval” or
“heartbeat interval.” Receipts that expose both values should use distinct
fields such as `leaseDurationSeconds: 40` and
`renewIntervalBaseSeconds: 10`.

There are two unrelated timing fields in the
`node-heartbeat-with-lease` Stage: `durationMilliseconds: 600000` and
`jitterDurationMilliseconds: 610000`. Neither field is the Lease renewal
period.

### Required fast Stages

The digest-verified `stage-fast.yaml` contains exactly five `Stage` documents:

1. `node-heartbeat-with-lease`
2. `node-initialize`
3. `pod-complete`
4. `pod-delete`
5. `pod-ready`

Both the digest and the semantic inventory are locked. Release validation
should fail if the file hash, Stage count, duplicate-name check, or exact
ordered name list differs. The semantic checks give a useful diagnostic; they
do not replace the content digest.

## Canonical project lock

The runtime implementation must preserve at least this information,
independent of the final machine-readable release schema:

```yaml
kwok:
  release: v0.8.0
  sourceCommit: 156033d7df7ea0e09cea82b715fe566ea68aeeb4
  assets:
    kwok.yaml:
      sha256: a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405
      sizeBytes: 108286
    stage-fast.yaml:
      sha256: 2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24
      sizeBytes: 7564
  image:
    reference: registry.k8s.io/kwok/kwok@sha256:6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0
    indexMediaType: application/vnd.oci.image.index.v1+json
    platforms:
      - linux/amd64
      - linux/arm64
  behavior:
    managedNodeSelector:
      kind: annotation
      expression: kwok.x-k8s.io/node=fake
    leaseDurationSeconds: 40
    renewIntervalBaseSeconds: 10
    stages:
      - node-heartbeat-with-lease
      - node-initialize
      - pod-complete
      - pod-delete
      - pod-ready
```

`release: v0.8.0` is human-readable metadata. The source commit, asset hashes,
and image digest are the content locks.

## Release and CI acceptance checks

A release should reject the runtime bundle unless all of these checks pass:

1. Resolve `refs/tags/v0.8.0` through the GitHub tag-ref API and require the
   locked full commit.
2. Download both named assets, check byte sizes, and verify SHA-256 before
   parsing.
3. Parse the controller configuration and require:
   `manageAllNodes: false`, the exact annotation selector, an empty label
   selector, and `nodeLeaseDurationSeconds: 40`.
4. Parse `stage-fast.yaml` as a multi-document YAML stream and require exactly
   the five locked, uniquely named Stages.
5. Fetch the OCI index using an explicit OCI/Docker manifest-list `Accept`
   header and verify the raw response digest.
6. Require runnable `linux/amd64` and `linux/arm64` manifests; do not count
   attestation manifests as runtime platforms.
7. Render the project installation and require the Deployment image to use the
   locked `@sha256:` reference.
8. Record the exact tuple in the product version, installation, and validation
   receipts required by the Kubernetes compatibility policy.

End-to-end validation still has to prove that the installed controller
manages only owned Synthetic Nodes, creates and renews their Leases, advances
the required Pod lifecycle, and does not mutate pre-existing real Nodes. A
correct content lock makes that behavior reproducible; it does not replace
behavioral validation.

## Exact primary sources checked

- [KWOK v0.8.0 tag-ref API](https://api.github.com/repos/kubernetes-sigs/kwok/git/ref/tags/v0.8.0)
- [Source commit `156033d`](https://github.com/kubernetes-sigs/kwok/commit/156033d7df7ea0e09cea82b715fe566ea68aeeb4)
- [KWOK v0.8.0 Release API](https://api.github.com/repos/kubernetes-sigs/kwok/releases/tags/v0.8.0)
- [KWOK v0.8.0 Release page](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0)
- [`kwok.yaml` Release asset](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml)
- [`stage-fast.yaml` Release asset](https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml)
- [Commit-pinned in-cluster installation guide](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/site/content/en/docs/user/kwok-in-cluster.md)
- [Commit-pinned node and Pod management guide](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/site/content/en/docs/user/kwok-manage-nodes-and-pods.md)
- [Commit-pinned KWOK configuration type](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/pkg/apis/config/v1alpha1/kwok_configuration_types.go)
- [Commit-pinned Lease controller wiring](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/pkg/kwok/controllers/controller.go)
- [Commit-pinned Lease renewal controller](https://github.com/kubernetes-sigs/kwok/blob/156033d7df7ea0e09cea82b715fe566ea68aeeb4/pkg/kwok/controllers/node_lease_controller.go)
- [Official `registry.k8s.io` v0.8.0 manifest endpoint](https://registry.k8s.io/v2/kwok/kwok/manifests/v0.8.0)

The tag, Release API, asset URLs, and image tag are live upstream names and may
change because the Release is not immutable. The full commit and recorded
content digests are the values that make this research reproducible.
