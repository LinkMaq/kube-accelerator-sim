# ADR 0008: Isolate source-backed Simulated Vendor Telemetry

Status: Accepted

## Context

Platform integration tests need Prometheus series that look like the schemas
published by accelerator exporters even when no physical accelerator, driver,
or exporter can run. Synthetic Nodes do not have real kubelets or host device
stacks, so a DaemonSet cannot honestly create one exporter Pod on every Node.
Generating independent random values at scrape time would also make memory,
power, temperature, counters, tests, and incident reproduction inconsistent.

Scheduling Vendor Profiles are already immutable inputs to Scenario Revision
identity. Adding telemetry fields to that catalog would change scheduling
digests for an observation-only feature. Exporter metric evidence also has a
different lifecycle: a scheduling profile can be verified while its public
Prometheus contract remains provisional or unavailable.

Three designs were compared:

1. Register vendor collectors on the existing controller `:8080` endpoint.
   This adds the fewest objects, but couples high-cardinality series and scrape
   failures to reconciliation, and controller replicas duplicate targets.
2. Add a telemetry-only mode to `kasim-controller` and run it in a separate
   Deployment. This isolates Kubernetes delivery but gives one binary two
   unrelated lifecycles and flag sets.
3. Add a dedicated `kasim-telemetry` runtime process in the existing OCI image,
   backed by one deep Module and one read-only Kubernetes source seam.

The third design has the best Depth and Locality. The chart keeps its common
caller as short as the second design: installation enables telemetry without a
Scenario or CLI change.

## Decision

### Runtime boundary

The release image contains a dedicated `kasim-telemetry` binary. Helm installs
one replica with a separate ServiceAccount, read-only ClusterRole, Deployment,
and ClusterIP Service. It reads only Scenario Instances and exactly
Kasim-owned Synthetic Nodes. It cannot mutate cluster state and has no
kubeconfig flag, write route, NodePort, LoadBalancer, Ingress, pprof route, or
remote-write client.

The runtime exposes `GET` and `HEAD` only:

- `/metrics` for one aggregate Prometheus text-format snapshot;
- `/healthz` for process liveness;
- `/readyz` after a fresh Kubernetes observation and successful render.

Synthetic Nodes are metric-series dimensions, not scrape targets or fake
exporter Pods. Every native sample carries exact source-backed native labels
plus `node=<Synthetic Node>` and `kasim_*` provenance including
`kasim_simulated="true"`, Scenario Instance, Synthetic Node, pool, profile,
model, and stable simulated device identity. The compatibility `node` label is
device ownership, never the real Node that schedules the centralized telemetry
Pod.

### Deep Module and seams

`internal/telemetry` is one deep in-process Module. Its production composition
constructs it from a `SnapshotSource`, immutable Telemetry Catalog, listener,
refresh interval, and stale interval, then calls one `Run(context.Context)`
behavior. The Module hides:

- catalog parsing, evidence-state validation, and family collision checks;
- exact device expansion and stable simulated identities;
- deterministic correlated signal generation and counter integration;
- Prometheus escaping, ordering, encoding, immutable caching, and HTTP safety;
- source failure, staleness, readiness, bounded scale, and shutdown.

Kubernetes observation is the only new true external seam. Its production
Adapter joins Scenario Instances to Nodes by exact UID, managed-by label, name,
and desired generation; tests use deterministic in-memory observations. Vendor
coverage remains catalog data. There is no vendor Adapter, generator plugin
registry, catalog network seam, per-node goroutine, or per-device goroutine.

### Separate Telemetry Catalog

The bundled Telemetry Catalog is immutable and versioned independently of the
scheduling catalog. A Telemetry Contract records the exporter, evidence source
and revision, state, native label bindings, exact metric family, Prometheus
type, unit, sealed value semantic, and optional model envelope.

States are:

- `verified`: the source-backed built-in families are emitted automatically;
- `provisional`: first-party evidence is incomplete, so native families are
  discoverable but disabled;
- `unavailable`: no safe public first-party mapping was found, so Kasim emits
  only an explicit availability diagnostic and never invents a native name.

Telemetry catalog changes do not modify a Scenario, Scenario Revision,
scheduling Vendor Profile digest, Resource Contract, Scenario Instance status,
or reconciliation readiness.

### Deterministic realistic values

Values are sampled in 15-second buckets, never randomized on scrape. The seed
binds stable Scenario Instance UID, Synthetic Node, pool, model, device ordinal,
and semantic. The same input and bucket produce identical values across
repeated scrapes and restarts.

One per-device latent load drives utilization, memory use, power, temperature,
clock, and throughput. Values are bounded by the Telemetry Contract envelope;
used and free memory cannot exceed total memory. Unhealthy devices use the
contract's supported health representation and suppress activity. Counters are
monotonic from a documented simulator epoch. Static identity and capacity do
not drift within an observed topology.

These curves are suitable for Prometheus ingestion, dashboards, alert rules,
and platform adaptation tests. They are not performance, capacity, thermal,
power-efficiency, hardware-failure, driver, or physical-sensor evidence.

### Failure and scale contract

Prometheus scrapes perform no Kubernetes I/O; they read an immutable encoded
snapshot. A failed refresh never replaces the last successful buffer. After
the stale interval, native series are removed and the endpoint retains only
Kasim source/error diagnostics while readiness fails. Invalid catalog records,
family type conflicts, label conflicts, observation ownership errors, and
invariant violations fail closed.

One snapshot supports at most 1,000 Synthetic Nodes and 8,000 simulated
devices. Rendering is `O(nodes + devices * enabled families)` with no hidden
thread multiplier. The reference-scale test requires all 8,000 devices to
produce their enabled families within a bounded 128 MiB exposition.

### Fidelity boundary

Simulated Vendor Telemetry is orthogonal to both product Fidelity Modes. It
does not become a third mode and does not change `Ready` or
`FidelitySatisfied`. Kasim preserves exporter schema evidence but does not
observe or reproduce physical vendor telemetry.

## Consequences

- One Helm installation provides a useful default endpoint without changing
  Scenario files or CLI lifecycle commands.
- Telemetry failure cannot stop scheduling reconciliation.
- Adding or promoting a vendor is usually a catalog and golden-fixture change.
- The central endpoint does not reproduce a real vendor DaemonSet's one-target-
  per-node topology; queries should group by native Node labels, `kasim_node`,
  or the common `node` compatibility label rather than Pod placement.
- Provisional and unavailable vendors remain visible without fabricated data.
- The image contains one additional internal runtime binary and the chart owns
  one additional read-only Pod, Service, ServiceAccount, Role, and Binding.

## Evidence

- [Implementation issue #55](https://github.com/LinkMaq/kube-accelerator-sim/issues/55)
- [Accelerator telemetry metric evidence](../research/accelerator-telemetry-metrics.md)
