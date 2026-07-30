# Kubernetes compatibility

The 0.1 release freezes product compatibility to Kubernetes 1.30 through 1.36.
This is a bounded functional statement, not an unbounded `1.30+` promise and
not a security-maintenance claim for old Kubernetes releases.

| Kubernetes | Upstream state on 2026-07-30 | `scheduling` | `dra-control-plane` |
| --- | --- | --- | --- |
| 1.30.14 | EOL | Validated | Unsupported |
| 1.31.14 | EOL | Validated | Unsupported |
| 1.32.13 | EOL | Validated | Unsupported |
| 1.33.13 | EOL | Validated | Unsupported |
| 1.34.10 | Active | Validated | Stable `resource.k8s.io/v1` |
| 1.35.7 | Active | Validated | Stable `resource.k8s.io/v1` |
| 1.36.3 | Active | Validated | Stable `resource.k8s.io/v1` |

The immutable machine-readable input tuple is
[`release/compatibility-lock.json`](../../release/compatibility-lock.json).
It records the exact kind version, node-image digests, project-built versus
kind release-paired classification, Kubernetes release artifact checksums, host
architecture, and checked date. Project-built images are produced only by the
manual `build-kind-node-images.yml` workflow from checksum-verified Kubernetes
server archives; they are never substituted with an older patch.

## Validation cadence

Pull requests and `main` run the scheduling suite on the 1.30 floor, the oldest
active minor 1.34, and the 1.36 ceiling. Stable DRA runs on 1.34 and 1.36.
Nightly and manually dispatched release validation run scheduling on all seven
rows and stable DRA on 1.34, 1.35, and 1.36. A failed row is not allowed to
continue silently.

Each successful row uploads a
`kasim.io/compatibility-receipt/v1alpha1` JSON artifact. Scheduling receipts
record runtime absence, authorization denial, admission rejection, server
dry-run, ownership conflict, real Node and Lease safety, scheduler placement
and exhaustion, health reduction, overcommitment, scale, controller recovery,
context repointing, foreign Pod cleanup blocking, and zero remaining owned
objects. DRA receipts record stable discovery, inventory, allocation,
reservation, Pod binding, device reuse, and cleanup.

The receipts include the source revision, checked timestamp, exact Kubernetes
server and node image, kind/kubectl/Helm/container versions as applicable,
controller/chart/KWOK/catalog/schema/CRD inputs, duration, results, and
exclusions. Cleanup proves absence of the exact API objects owned by the
Scenario Instance; it does not claim that the etcd database file shrank.

## Truth boundary

The product CLI only submits scenarios to an existing explicit kubeconfig and
context. Disposable kind cluster creation and deletion exists only inside
`test/e2e`.

The `scheduling` suite proves Kubernetes control-plane placement and scalar
resource accounting. It does not prove Pod execution, physical Accelerator
access, Accelerator computation, device-plugin gRPC, CDI injection, or DRA
node preparation. The stable DRA suite proves control-plane allocation only;
the separate protocol oracle covers node-runtime protocol behavior without
turning that evidence into a physical-hardware claim.
