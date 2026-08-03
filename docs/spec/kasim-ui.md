# `kasim ui` Cluster Simulation Inventory proposal

Status: **Implemented for the next release.**

This specification turns the [Wayfinder decision map](https://github.com/LinkMaq/kube-accelerator-sim/issues/34), the [Kubernetes inventory research](../research/kasim-ui-kubernetes-inventory.md), and the [accelerator and RDMA signal research](../research/kasim-ui-accelerator-rdma-signals.md) into an implementation-ready contract. It extends the product terminology in [`CONTEXT.md`](../../CONTEXT.md) and the decisions in [ADR 0008](../adr/0008-stream-cluster-simulation-inventory-snapshots.md), [ADR 0009](../adr/0009-model-auxiliary-device-pools.md), and [ADR 0010](../adr/0010-embed-authenticated-loopback-ui.md).

## Outcome

`kasim ui` starts a temporary read-only web view for one explicitly selected Simulation Target. Its first screen answers:

1. Which Nodes are Kasim Synthetic Nodes, and which are not Kasim-owned?
2. Which accelerator and Auxiliary Device Signals does Kubernetes currently expose?
3. Which values are scalar quantities, and which are native DRA devices with verifiable identities?
4. What are capacity, allocatable quantity, observed Pod requests, DRA allocation, and health evidence?
5. Is the view live, stale, incomplete, forbidden, or unsupported, and why?

The UI never creates, changes, scales, heals, or deletes a Scenario. It never owns Kubernetes cluster lifecycle and never claims physical accelerator or RDMA capability.

## Command contract

```text
kasim ui \
  --kubeconfig /absolute/path/to/config \
  --context exact-context-name \
  [--port 8080] \
  [--open]
```

- Both target flags are mandatory. There is no current-context, `KUBECONFIG`, or in-cluster fallback.
- The only listen address is `127.0.0.1`; there is no `--address` flag.
- The default port is `8080`; `--port` accepts `1..65535`.
- The command prints the target context, redacted fingerprint, freshness state, and access URL.
- `--open` opens the browser only after the listener and target identity are ready. Failure to open the browser is a warning because the printed URL remains usable.
- A bind conflict, invalid target, failed DNS/TLS/authentication, unsupported Kubernetes release, or target mismatch returns a stable startup diagnostic and closes the listener.
- `SIGINT` and `SIGTERM` cancel source synchronization, stop accepting requests, and allow at most five seconds for graceful shutdown.
- Source-specific authorization or capability gaps do not stop startup; the UI opens in partial or diagnostics-only mode.

The command follows the release's bounded Kubernetes matrix, currently 1.30 through 1.36. The 1.30 floor is not an open-ended compatibility promise above the validated ceiling.

## Local security contract

Every process launch generates at least 256 bits of random capability material. The URL contains the token in its fragment, for example:

```text
http://127.0.0.1:8080/#token=<base64url-capability>
```

Fragments are not sent in HTTP requests, server logs, or referrers. The frontend keeps the token in memory and sends it as `Authorization: Bearer` on every data request. It does not write the token to local storage, cookies, logs, or page content.

The local server:

- validates the exact loopback Host;
- serves no cluster data without the capability;
- accepts only `GET` and `HEAD` and has no mutation route;
- emits no permissive CORS header;
- sets `frame-ancestors 'none'`, `object-src 'none'`, `base-uri 'none'`, same-origin resource policy, `nosniff`, and a strict `default-src 'self'` Content Security Policy;
- sends `Cache-Control: no-store` for HTML and data;
- has no analytics, service worker, remote font, CDN, or third-party request;
- exposes a versioned JSON/fetch-stream transport that is private to the same `kasim` version, not a public remote API.

Authenticated fetch streaming is preferred because the browser can attach the bearer token. Bounded authenticated polling is the fallback; a query-string token, WebSocket-only contract, and cookie login flow are rejected.

## Reading path and interaction

The winning prototype is [Variant A: evidence-first ledger](https://github.com/LinkMaq/kube-accelerator-sim/tree/prototype/kasim-ui-options/prototypes/kasim-ui). Its desktop and mobile screenshots are primary design evidence on that branch.

The production home page keeps this order:

1. Target, connection, live/stale/partial state, and last update.
2. A visible diagnostic when evidence is missing or stale.
3. On mobile, the device and signal ledger; on desktop, a compact summary band followed immediately by the ledger.
4. Stable summary totals separated by unit and representation.
5. The complete Node list, with Kasim Synthetic Nodes first.
6. A Node/detail drawer derived from prototype Variant B.

The ledger keeps these fields visible without hover:

- Node and Kasim ownership;
- vendor/model only when source-backed;
- signal role: accelerator, auxiliary, or unclassified;
- representation: scalar extended resource or native DRA device;
- exact resource name or native DRA identity;
- capacity, allocatable, requested-from-observed-Pods, and allocation phase;
- health with evidence, or `unknown / not reported`;
- Profile/Resource Contract or Kubernetes source;
- Auxiliary Device Pool association and scheduling-only caveat.

Search, origin, Scenario, vendor, model, signal role, representation, health, and source-state filters are URL-backed. Default browser back/forward behavior restores the same view. The capability remains in the fragment and is not copied into query parameters.

English and Simplified Chinese are bundled. Browser language chooses the first locale and a page control changes it. Kubernetes field names, exact resource names, object names, driver IDs, and vendor identifiers remain unchanged.

## Visual and accessibility contract

The UI is an operational inventory, not a decorative chart dashboard. It uses semantic HTML tables, direct labels, compact summary values, dividers, and an evidence drawer. It does not require a charting library, Canvas, WebGL, animation, or generated imagery.

Color roles are redundant with text and shape:

- cyan accent plus `Kasim` text for Synthetic Nodes;
- neutral slate plus `Non-Kasim` text for other Nodes;
- violet plus `Auxiliary` text for related device signals;
- green only for evidence-backed ready/reported or simulated-available states;
- amber plus `Unknown`, `Partial`, or `Unsupported` text;
- red plus `Stale`, `Offline`, or terminal diagnostic text.

Keyboard users can reach filters, rows, the detail drawer, locale selection, and close/reset controls. Focus is visible, the drawer returns focus to its invoking row, and Escape closes it. No essential value is hover-only. Status updates use a restrained live region and do not announce every watch event.

At 360–430 CSS pixels, the exact signal/device ledger precedes the summary band, table rows become labelled records, filters collapse without hiding active values, and the detail drawer becomes a full-width sheet. Primary controls target at least 44 CSS pixels. Reduced-motion users lose no information because production requires no meaningful animation.

If JavaScript is disabled or fails to load, the static page exposes no cluster data and explains that the local live inventory requires JavaScript. The CLI remains the recovery path.

## Cluster Simulation Inventory Module

The UI consumes a dedicated deep Module and never calls client-go, parses CRDs, interprets conditions, or manages Kubernetes watch cursors.

```go
type Module struct { /* private implementation */ }

func (m *Module) Open(
    context.Context,
    OpenRequest,
) (SnapshotStream, error)

type SnapshotStream interface {
    Next(context.Context) (Snapshot, error)
    Close() error
}
```

`OpenRequest` contains one explicit `cluster.TargetSelection`. `Open` fixes the target identity and returns after connection establishment. The first `Next` returns revision 1, often `loading` or `partial`. Later values are complete immutable replacements with a monotonically increasing local revision. A slow consumer keeps only the newest pending snapshot.

Temporary source failures never terminate the stream. `Next` returns a terminal error only for context cancellation, a closed stream, target mismatch, or a broken internal invariant. `Close` is idempotent, concurrency-safe, and waits until Module-owned watches, retries, timers, and goroutines stop.

The private Kubernetes collection seam has production client-go and deterministic recording/in-memory Adapters. It uses a closed set of Nodes, Pods, Scenario Instances, ResourceSlices, ResourceClaims, and DeviceClasses. No Kubernetes runtime object, unstructured payload, GVR, kubeconfig content, credential, Secret, Pod log, container environment, or raw server error enters the public read model.

## Truthful read model

Every quantity, identity, classification, allocation, and health field is a fact:

```text
Fact state:
  known | unknown | unavailable | incomplete

Evidence class:
  observed | derived | kasim-simulated | unavailable

Snapshot completeness:
  loading | complete | partial | diagnostics-only

Snapshot freshness:
  loading | fresh | reconnecting | stale
```

Known zero is valid only as `state=known, value=0`. It is never substituted for forbidden, unsupported, incomplete, stale, or unknown data.

The snapshot contains:

```text
target and local revision
source states and bounded diagnostics
unit-safe summary
Scenario Instance views
Node views
scalar Resource Signals
native DRA Device views
DRA pool completeness
ResourceClaim allocation and observed Pod joins
bounds and omitted-count reports
```

Required interpretation rules:

- `Kasim Synthetic` requires exact managed-by, Instance UID, and Scenario Instance ownership joins. Every other Node is labelled `Non-Kasim`, which does not assert physical hardware.
- An extended resource is one scalar signal row. It never creates per-device rows or invented IDs.
- A stable DRA device keeps its `(driver, pool, device)` identity. A vendor UUID is a separate attribute.
- Capacity and allocatable come from Node status. Neither is utilization.
- `requestedFromObservedPods` is a derived scheduler reservation. `allocatable - requested` is a remaining estimate, not physical free capacity.
- DRA allocation, reservation, an observed scheduled consumer, and runtime use are distinct. API-server-only inventory reports runtime use as unknown.
- Node Ready, allocatable quantity, claim readiness, and device health are not interchangeable.
- Unknown resource names remain verbatim and unclassified. Vendor or model is never inferred from a domain substring.
- Shared tokens, memory units, partitions, cores, and virtual functions do not enter a physical-card total.
- Auxiliary signals never prove a NIC, link, driver, CNI, fabric, GPUDirect path, or working network.

## Auxiliary Device Pool contract

Scenario documents gain an optional collection next to `acceleratorPools`:

```yaml
nodeGroups:
  - name: nvidia-workers
    replicas: 2
    acceleratorPools:
      - name: h100
        # Existing source-backed Accelerator Pool fields.
    auxiliaryDevicePools:
      - name: rdma-a
        profile:
          id: rdma-shared-device-plugin
          revision: pinned-revision
          digest: sha256:pinned-profile-digest
        contract: shared-hca
        resource: shared-token
        resourceName: rdma/rdma_shared_device_a
        count: 8
        available: 8
        associatedAcceleratorPools: [h100]
```

The catalog schema revision classifies every Resource Contract as `accelerator` or `auxiliary`. An auxiliary contract adds a source-backed category and a resource naming policy:

- `fixed`: the Profile owns the exact fully qualified resource name;
- `scenario-required`: the upstream plugin makes the name configurable, so the Scenario must provide one exact valid extended-resource name.

The compiler accepts an override only for `scenario-required`, requires at least one local Accelerator Pool association, and rejects duplicate pool names, unresolved references, availability above count, resource collisions with Node capacity or any other pool, unsupported fidelity, and non-source-backed contract selection. Empty auxiliary collections preserve existing canonical Scenario bytes and digests.

`count` and `available` describe simulated capacity and schedulability, not physical health. Auxiliary Device Pools follow the Node Group revision, scale, receipt, status, ownership, and cleanup lifecycle. Initial built-in templates cover the source-backed configurable contracts of the RDMA Shared Device Plugin and SR-IOV Network Device Plugin.

## Kubernetes compatibility and source state

Core inventory on every validated Kubernetes minor uses stable Nodes, Pods, and Kasim Scenario Instances. Stable DRA interpretation is enabled only when `resource.k8s.io/v1` is discovered with the expected resources and fields, currently Kubernetes 1.34–1.36. DRA APIs on 1.30–1.33 or unknown schemas are reported as `unsupported-schema`; the implementation never guesses fields by name and never upgrades Kasim Fidelity from legacy observation.

Each source independently reports:

```text
availability:
  available | forbidden | unsupported | unsupported-schema | failed

mode:
  initializing | live | polling | snapshot-only | unavailable

freshness:
  fresh | reconnecting | stale | resyncing | incomplete
```

Synchronization policy:

- discover, paginated list, then watch from the list resource version;
- default list page size 500, following opaque continue tokens;
- request watch bookmarks but never depend on a fixed bookmark cadence;
- one cluster-wide Pod list/watch, never one per Node;
- debounce source bursts for 250 ms with a two-second maximum publication delay;
- reconnect with full-jitter exponential backoff from 250 ms to 30 seconds;
- retain the last successful data immediately as `reconnecting`, then mark it `stale` after 15 seconds disconnected;
- on `410 Gone`, relist only that source and mark dependent derived facts incomplete until replacement;
- when list is allowed and watch is forbidden, relist every 30 seconds and label the source `polling` rather than `live`;
- do not blank the page during reconnect, relist, tab backgrounding, or partial failure.

Release safety bounds start from existing project limits: 16,384 observed collection objects, 65,536 Pods, 65,536 Claims, and 128 devices per ResourceSlice. A source that crosses its hard bound is `incomplete`; affected totals and derived values cannot be labelled exact. Output details use stable sorting, default page size 100, maximum page size 500, bounded diagnostics, and explicit omitted counts.

## Read-only permissions

The complete view needs only `list` and `watch`:

```yaml
rules:
  - apiGroups: [""]
    resources: ["nodes", "pods"]
    verbs: ["list", "watch"]
  - apiGroups: ["simulation.kasim.io"]
    resources: ["scenarioinstances"]
    verbs: ["list", "watch"]
  - apiGroups: ["resource.k8s.io"]
    resources: ["resourceslices", "resourceclaims", "deviceclasses"]
    verbs: ["list", "watch"]
```

No UI data path requests Secrets, logs, metrics, `nodes/proxy`, impersonation, token creation, or a write verb. A SelfSubjectAccessReview may improve diagnostics only if already allowed; the UI must work from actual discovery/list/watch results without requiring its `create` verb.

Denied Pods remove requested quantities, not capacity. Denied Claims remove allocation, not DRA publication. Missing DRA removes device details, not extended-resource signals. Denied Nodes yields a Scenario-only or diagnostics-only page, not an empty-cluster claim.

## Production frontend and package budget

The production rewrite uses standards-based semantic HTML, CSS, and JavaScript modules embedded with Go `embed`. It has no frontend framework, chart library, remote asset, runtime Node.js dependency, or external static directory. Build-time minification is optional and reproducible; source remains inspectable.

Release gates:

- all embedded static assets: at most 256 KiB uncompressed and 96 KiB gzip;
- compressed release-binary increase attributable to the UI: at most 1 MiB, reported for every platform;
- initial page contains no cross-origin request and works under the documented CSP;
- current visible page renders at most 100 ledger rows and never creates a DOM row for every cached Pod;
- a 1,000-Node inventory remains searchable and opens details without unbounded browser memory growth.

The throwaway prototype uses 33,721 uncompressed bytes of HTML, JavaScript, and CSS; its JavaScript and CSS are approximately 5.8 KiB and 3.6 KiB gzip respectively. These numbers are evidence that the no-framework route is viable, not production baselines.

## Verification matrix

| Layer | Required evidence |
| --- | --- |
| Domain/unit | Fact states; known zero; unit-safe totals; exact Kasim ownership; no vendor/health inference; Pod request calculation; DRA identity; auxiliary count/availability and association validation. |
| Module Interface | Loading first snapshot; immutable full replacements; monotonic revision; slow-consumer coalescing; source partial/stale states; terminal target mismatch; idempotent clean shutdown. |
| Kubernetes Adapter | Discovery; paginated list/watch; bookmarks; disconnect; `410 Gone`; list-only polling; 403; unsupported GVR/schema; stable DRA v1 conversion; no raw object leakage. |
| HTTP/security | Loopback-only bind; exact Host; token absent/invalid/valid; no token in logs/referrer/cache; GET/HEAD only; no CORS; CSP and framing headers; graceful signal shutdown. |
| Browser/component | English/Chinese; URL filters; keyboard/focus/drawer; no hover-only values; 390 px and desktop layouts; partial/stale/offline/empty states; screen-reader names; no-JavaScript fallback. |
| Visual regression | Evidence-first desktop; Chinese partial-data mobile; detail drawer; long resource/driver names; unknown health; mixed Kasim and Non-Kasim Nodes. |
| E2E scheduling | Kubernetes 1.30 floor and validated ceiling; multiple Vendor Profiles; scalar signals; Pod scheduling and requests; real/non-Kasim Node present; reconnect and partial RBAC. |
| E2E DRA | Every validated stable-DRA minor; complete/incomplete multi-slice pools; native IDs; claim allocation/reservation/Pod join; runtime-use unknown; old schema unsupported. |
| Auxiliary E2E | Two Synthetic Nodes with accelerator and configurable RDMA token pools; exact resources on Node status; explicit association; no physical-network claim; scale, revision, status, and cleanup. |
| Scale/performance | 1,000 Nodes; 65,536 Pod/Claim fixtures; event burst debounce; slow browser; bounded diagnostics/payload/DOM; package budgets. |
| Release/docs | Cross-platform `kasim` binaries contain assets; checksums/signatures remain deterministic; Docker/Helm artifacts remain valid; examples, operator skill, English docs, and Chinese docs update together. |

## Acceptance demonstration

The release candidate must show one existing cluster containing:

- at least one Non-Kasim Node;
- three or more Kasim Synthetic Nodes across at least three accelerator ecosystems;
- one scalar extended-resource Accelerator Pool;
- one complete stable DRA pool with native device identities and one Claim allocation;
- one RDMA Auxiliary Device Pool with an explicit Accelerator association;
- observed Pod requests;
- one unknown health field that stays unknown;
- one denied or unsupported optional source that degrades only its dependent fields;
- a disconnect/reconnect that keeps the last known inventory visible;
- English desktop and Chinese mobile views.

Success means the page is truthful and navigable, not merely reachable: scalar resources have no invented devices, card totals exclude shared tokens, source gaps are visible, and stopping the CLI leaves no process or cluster mutation behind.

## Out of scope

- Kubernetes cluster installation or lifecycle;
- a remotely reachable or persistent dashboard;
- multi-cluster aggregation, accounts, or remote authorization;
- UI mutations of Scenario state;
- metrics-server or vendor telemetry;
- kubelet PodResources, `nodes/proxy`, logs, Secrets, or container environments;
- physical device files, drivers, firmware, CUDA/ROCm/CANN runtime, CNI, RDMA fabric, or network data plane;
- inferred vendor, model, topology, device identity, or health;
- a stable public browser API.
