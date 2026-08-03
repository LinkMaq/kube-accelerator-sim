# Read-only cluster inventory UI

`kasim ui` opens a temporary evidence-first view of accelerator and auxiliary
signals in one explicitly selected Kubernetes cluster. It reads both Kasim
Synthetic Nodes and other Nodes, but always places and labels Kasim Nodes
separately.

```sh
./dist/kasim ui \
  --kubeconfig ./target.kubeconfig \
  --context target \
  --port 8080 \
  --open
```

The command supports Kubernetes 1.30–1.36 and never owns cluster lifecycle.
`--open` is optional; a browser-open failure leaves the printed URL usable.
The listener is always `127.0.0.1`, and there is deliberately no `--address`
flag.

## What the home page shows

The first page is the device and signal ledger, not a chart dashboard. It
shows:

- exact Node ownership and Scenario joins;
- source-backed vendor/model facts when available;
- scalar extended-resource capacity, allocatable quantity, and observed Pod
  requests without inventing per-device IDs;
- native DRA `(driver, pool, device)` identities, allowlisted attributes, and
  distinct allocated, reserved, and scheduled-consumer evidence;
- auxiliary RDMA or SR-IOV scheduling tokens and their associated Accelerator
  Device Pools;
- source availability, watch/poll mode, freshness, and bounded diagnostics.

Unknown facts stay unknown. Node Ready is not device health, allocatable is
not utilization, and an auxiliary token does not prove a physical NIC, link,
CNI, fabric, GPUDirect path, or runtime data plane.

Search and filters for origin, Scenario, vendor, model, role, representation,
health, and source state are stored in the query string so browser navigation
restores the view. The capability remains in the fragment and is never copied
into those filter parameters. English and Simplified Chinese are embedded in
the same binary.

## Local security

Each launch generates a new 256-bit capability. The CLI prints a URL such as:

```text
http://127.0.0.1:8080/#token=<ephemeral-capability>
```

The fragment is not sent in the initial HTTP request. The frontend keeps it
in memory and attaches it as a bearer credential to inventory requests. The
server accepts only `GET` and `HEAD`, validates the exact loopback Host, has no
mutation route or permissive CORS, disables caching, and serves only embedded
HTML, CSS, and JavaScript. Stop it with `Ctrl+C`; shutdown cancels watches and
finishes within five seconds.

Treat the printed URL as a temporary secret. Anyone able to use it from the
same machine can read the same cluster metadata until the process stops.

## Read-only RBAC and partial views

The complete view uses cluster-wide `list` and `watch` for Nodes, Pods,
Scenario Instances, ResourceSlices, ResourceClaims, and DeviceClasses. The
runtime Chart grants these reads to its controller; a local user's kubeconfig
must independently authorize them.

Sources fail independently. If list is allowed but watch is forbidden, Kasim
labels that source `polling` and refreshes every 30 seconds. A temporary watch
failure retains the last successful objects as `reconnecting`, marks them
`stale` after 15 seconds, and retries with bounded jitter. Missing stable DRA
APIs below Kubernetes 1.34 are reported as `unsupported-schema`; scalar Node
inventory remains usable.

Use the source diagnostic panel before interpreting totals. A `partial` or
`diagnostics-only` page is deliberately not presented as complete evidence.

## Auxiliary signal example

Compile the bundled H100/RDMA plus MI300X/SR-IOV example offline before
submitting it:

```sh
./dist/kasim apply \
  -f ./examples/signals/auxiliary-rdma-sriov.yaml \
  --dry-run=client \
  -o json
```

Both auxiliary contracts are upstream-configurable. Therefore the Scenario,
not the catalog, supplies their exact fully qualified extended-resource names.
See [Scenario examples](scenario-examples.md) for the full topology.

## Reproduce the browser and package gates

The release test uses pinned Playwright Chromium against a deterministic
1,001-Node fixture. It verifies desktop and 390 px layouts, English and
Chinese, URL history, keyboard/focus behavior, partial data, same-origin
requests, fragment-token isolation, no-JavaScript fallback, and the 100-row
DOM bound. Three screenshots are retained as CI visual evidence:

```sh
npm ci
npx playwright install chromium
npm run test:ui
```

`go test ./internal/ui` enforces the 256 KiB raw and 96 KiB gzip static-asset
limits. The evidence-gated release builder additionally compares every native
CLI with a measurement-only no-UI build and rejects a compressed delta above
1 MiB; those measurements are published in `release-receipt.json`.
