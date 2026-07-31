# Final v1 requirement audit

The normative source is the [v1 product specification](../spec/v1.md).
[`release/traceability.json`](../../release/traceability.json) contains one
machine-readable row for every normative ID, and the generated
[requirement traceability](requirement-traceability.md) presents the same
mapping for reviewers. No v1 requirement is silently omitted or deferred.

## Audit results

| Area | Reviewable result |
| --- | --- |
| Product boundary | CLI operates only on an explicit existing target; cluster lifecycle remains test infrastructure |
| Fidelity | `scheduling` covers Kubernetes 1.30–1.36; stable DRA control-plane projection is bounded to 1.34–1.36 |
| Catalog | Verified, provisional, custom, and catalog-only semantics are distinct; exact revisions/digests are compiled before writes |
| Scenarios | Single/single, single/multi, multi/multi, heterogeneous, health, scale, DRA, and blocked-delete paths have versioned examples plus executable CLI/E2E contracts |
| Lifecycle safety | UID/generation/target preconditions, exact ownership, real-Node protection, unowned-Pod blocking, and zero-owned-object cleanup are tested |
| Operator workflow | Installation, apply, receipts, status, typed revisions, safe deletion, uninstall, upgrade, rollback, and troubleshooting are documented |
| Evidence | CI, seven-minor scheduling, three-minor stable DRA, floor/ceiling protocol oracle, two-trial 1,000-Node scale, and release workflows produce source-revision-bound receipts |
| Release | Five native CLI archives, controller image, OCI/TGZ chart, checksums, SBOM, provenance, dependency lock, signatures, and evidence normalization are gated by the manual release workflow |

## Documentation verification

`test/contract/documentation_test.go` performs four independent checks:

1. every normative ID appears exactly once with existing implementation, test,
   and evidence paths;
2. every versioned Scenario example compiles through the real CLI client
   dry-run path;
3. all local links in operator documentation resolve;
4. operator text preserves explicit target selection, bounded Kubernetes
   versions, cluster-lifecycle separation, and the fidelity exclusions.

`internal/tools/traceability` deterministically regenerates the JSON and
Markdown indexes. `make verify` checks generated-reference drift in addition
to formatting, vet, unit/integration/race, architecture, and Helm checks.
Dedicated GitHub workflows remain required for behavior that needs real
Kubernetes, kubelet, scheduler, container runtime, or reference-scale
infrastructure.

## Architecture and safety review boundary

The architecture gate rejects forbidden imports and shallow cross-module
seams. The safety suites exercise ownership conflicts, stale resource
versions, mixed real/Synthetic Nodes, foreign bound Pods, controller restart,
and final leak checks against real API servers. Release evidence is accepted
only when compatibility, protocol, and scale receipts all name the exact
source revision being packaged.

The final release claim is deliberately narrow: the simulator proves
Kubernetes control-plane behavior. It does not provide device access, does not
execute accelerator compute, does not install vendor drivers, does not provide
vendor telemetry, does not simulate NUMA topology, and does not inject CDI
devices.
