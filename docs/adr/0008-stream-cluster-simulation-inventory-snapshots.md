# ADR 0008: Stream immutable Cluster Simulation Inventory snapshots

Status: Accepted

`kasim ui` needs a cluster-wide, read-only view whose scale, permissions, update lifecycle, and truth model differ from observing one Scenario Instance. We will add a dedicated Cluster Simulation Inventory Module beside the Scenario Runtime Module instead of widening `Apply`/`Observe`/`Delete`, the single-instance Scenario Control Plane seam, or the ownership-scoped Kubernetes Cluster port.

The Module has one external entry point: `Open` resolves either explicit target fields or the UI's standard kubeconfig/current-context request, then binds the resolved context and client configuration to one immutable Simulation Target and returns a single-consumer stream with `Next` and `Close`. `Next` publishes complete, immutable, monotonically sequenced snapshot replacements. Its first value may be `loading` or `partial`; slow consumers skip intermediate snapshots rather than accumulating an unbounded event log. Recoverable source failures, permission gaps, unsupported APIs, watch reconnects, and `410 Gone` relists are facts inside snapshots, while only invalid targets, failed target establishment, target mismatch, an unsupported Kubernetes floor, or broken internal invariants terminate the stream.

Every observed or derived value carries evidence and distinguishes known zero from unknown, unavailable, incomplete, and stale. Scalar extended resources remain aggregate signals and never acquire invented device IDs. DRA devices preserve their native identity, Pod requests are labelled as scheduler requests rather than utilization, and health is never inferred from Node readiness or allocatable capacity. Snapshot completeness and freshness are independent dimensions because a view may be fresh but partial, or complete but stale.

Kubernetes remains a true external dependency behind a private collection seam with production client-go and deterministic in-memory/recording Adapters. The Module hides discovery, paginated list/watch caches, resource versions, reconnect and relist policy, DRA schema translation, ownership joins, Pod-request aggregation, evidence construction, bounds, and redaction. The first release interprets core Node/Pod and Kasim records on Kubernetes 1.30+ and stable `resource.k8s.io/v1` DRA when discovered. Older or unknown DRA schemas are reported as unsupported rather than decoded heuristically.

## Considered options

- Extending Scenario Runtime or Scenario Control Plane was rejected because it mixes one-instance lifecycle semantics with target-wide collection observation and lowers Locality.
- A public query language with selectors, pagination cursors, snapshot retention, and detail levels was rejected until a second real caller proves that complexity. The local HTTP layer can filter and page the bounded in-memory snapshot without making those concerns part of the Module Interface.
- Streaming Kubernetes-shaped deltas was rejected because every caller would have to own cursor recovery, ordering, joins, partial invalidation, and evidence repair.
