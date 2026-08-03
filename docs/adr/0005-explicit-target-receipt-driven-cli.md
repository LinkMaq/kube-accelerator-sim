# ADR 0005: Use an explicit-target, receipt-driven CLI

Status: Accepted

## Context

The CLI must make the common single-Node, multi-Accelerator, multi-Node, and
health-change cases short to type without creating a second command-specific
state model. It also operates against arbitrary existing Kubernetes clusters,
where an implicit current context, name-only update, broad cleanup selector, or
interactive confirmation can turn a convenient command into an unsafe
cluster-wide mutation.

The Scenario Instance contract is asynchronous and revisioned. A command can
successfully accept a revision before all Synthetic Nodes converge, and a
timeout does not mean that the accepted revision was rolled back. The CLI must
therefore expose receipts, target identity, generation preconditions, and
bounded status rather than reducing every outcome to a line of prose.

## Decision

The first binary is named `kasim`. Its minimal command surface is:

| Command | Purpose |
| --- | --- |
| `kasim apply` | Compile a Scenario file or one homogeneous shortcut, then create or revise one Scenario Instance |
| `kasim status` | Observe one Scenario Instance once or with `--watch` |
| `kasim health` | Compile an Accelerator Pool healthy-count change into a new Scenario Revision |
| `kasim scale` | Compile a Node Group replica change into a new Scenario Revision |
| `kasim delete` | Request ownership-bounded deletion of one Scenario Instance |
| `kasim profile list` | List bundled Vendor Profiles and their classes offline |
| `kasim profile show` | Show one pinned profile revision, Resource Contracts, models, and evidence offline |
| `kasim version` | Show CLI, schema, catalog, and supported Kubernetes version information |

There is no first-version bulk delete, force delete, arbitrary Node patch,
backend selector, cluster lifecycle command, workflow language, or public
`plan` command. Product installation and upgrade are separate release
workflows; `apply` never installs a controller or cluster-wide RBAC implicitly.
It fails with `RuntimeUnavailable` before a persistent write when a compatible
installation is absent.

### One input model

`kasim apply` accepts exactly one input form:

```text
kasim apply -f scenario.yaml ...
kasim apply -f - ...
kasim apply demo --profile nvidia --model nvidia-h100 \
  --nodes 1 --accelerators-per-node 1 ...
```

`-f` accepts one local file or standard input containing exactly one Scenario.
Directories, URLs, arbitrary Kubernetes manifests, and multiple Scenario
documents are rejected in the first version. YAML duplicate keys, unknown
fields, unresolved references, and non-canonical quantities fail validation.

The shortcut represents one generated Node Group and one Accelerator Pool. It
supports the common single-Node and homogeneous multi-Node shapes through
`--nodes`, `--accelerators-per-node`, and optional
`--healthy-per-node`. `--contract` and `--resource` may be omitted only when
the selected verified Vendor Profile has exactly one unambiguous compatible
choice. Heterogeneous or multi-pool Scenarios use a Scenario file.

File and shortcut inputs pass through the same parser, resolver, canonicalizer,
digest, validation, and `ScenarioRuntime.Apply` call. `health` and `scale`
fetch the current canonical Scenario, change only their typed field, and submit
the result as a new revision. They are not imperative backend operations.

### Explicit Simulation Target

Every lifecycle or mutation command that contacts Kubernetes requires both:

```text
--kubeconfig /explicit/path/to/config
--context exact-context-name
```

Those commands do not use the kubeconfig current context, the default
`~/.kube/config`, or `KUBECONFIG` as a substitute for either flag. The CLI
resolves the file to a canonical absolute path, loads the named context once,
and keeps that client configuration immutable for the process lifetime.

The later read-only `kasim ui` command is the narrow exception defined by
[ADR 0010](0010-embed-authenticated-loopback-ui.md): it defaults to the standard
client-go kubeconfig loading rules and current context, permits either target
flag as an independent override, then freezes and prints the resolved context.

The target fingerprint is the SHA-256 digest of the `kube-system` Namespace
UID with a domain separator. The connection report separately includes the
canonical API server URL and cluster CA digest. The fingerprint remains stable
across context renames and API endpoint changes, while a context silently
repointed to another cluster fails closed.

Every receipt and status result includes the context name, target fingerprint,
Scenario Instance name and UID, desired and observed generation, revision
digest, and resolved Vendor Profile digests. Existing instances reject a
different target fingerprint.

### Preflight and dry-run

Apply preflight is ordered to minimize side effects:

1. parse, validate, resolve profiles, canonicalize, and digest the Scenario
   without network access;
2. load the explicitly named target and establish authenticated TLS;
3. discover Kubernetes APIs, product runtime compatibility, and target
   fingerprint;
4. perform SelfSubjectAccessReviews for the exact required operations;
5. read the named Scenario Instance and check UID, generation, fidelity,
   ownership, and object-name conflicts;
6. run supported Kubernetes admission through `dryRun=All`;
7. emit the proposed receipt and only then accept a persistent revision.

`apply --dry-run=client` stops after step 1 and needs no Simulation Target.
`apply --dry-run=server` performs all read-only and non-persisted preflight,
including admission dry-run, and never accepts a revision. Server dry-run is a
mode of `Apply`, not another public lifecycle operation. Its output states that
apply will repeat conflict checks because dry-run cannot reserve cluster state.

### Concurrency and identity preconditions

Creating a missing Scenario Instance defaults to expected generation zero. A
same-name, same-digest reapply is an idempotent no-op. Any different revision
of an existing instance requires both:

```text
--instance-uid <server-assigned-uid>
--expected-generation <current-generation>
```

`health` and `scale` require the same two values. `delete` additionally requires
the exact instance name and never accepts name prefix, label-only, `--all`, or
wildcard targeting. The Scenario Instance controller enforces UID and
generation atomically; a client-side read followed by an unguarded write is
insufficient.

A different or missing UID, stale generation, target mismatch, pre-existing
object without the exact ownership UID, or incompatible Fidelity Mode fails
before revision acceptance. Concurrent writers therefore produce a typed
conflict instead of last-write-wins behavior.

### Waiting, status, and output

`apply`, `health`, `scale`, and `delete` wait for their requested terminal
condition by default, with a configurable `--timeout`. `--async` returns after
revision acceptance. A timeout never claims rollback; it returns the accepted
receipt and latest Snapshot so automation can continue with `status --watch`.

Human-readable output is the terminal default. Every command supports
`-o json` and `-o yaml` with a versioned output schema. Machine output keeps
diagnostic codes, retryability, whether a revision was accepted, achieved and
excluded fidelity surfaces, capped blockers, and receipts on both success and
failure. Credentials, bearer tokens, client keys, and kubeconfig contents are
never printed.

Stable process exit categories are:

- `0`: dry-run succeeded, apply was a no-op, or the requested terminal
  condition was reached;
- `2`: invocation, schema, catalog, or canonical Scenario validation failed;
- `3`: target, authentication, authorization, capability, or runtime preflight
  rejected the operation before revision acceptance;
- `4`: UID, generation, fingerprint, fidelity, or ownership safety precondition
  conflicted before revision acceptance;
- `5`: a revision was accepted but convergence failed, timed out, or remained
  blocked.

Machine-readable diagnostic codes are more specific than the exit category and
are the stable automation contract.

### Safe deletion

Deletion first accepts a revision that closes scheduling on the owned
Synthetic Nodes. It removes only allowlisted objects carrying the exact
Scenario Instance UID and never uninstalls the shared runtime.

There is no interactive confirmation and no `--force` escape hatch in the
first version. Safety comes from explicit target selection, instance UID,
expected generation, target fingerprint, and server-enforced ownership. If an
unowned Pod remains, deletion reaches `CleanupBlocked`, reports capped object
references, and exits in category 5 when waiting. Retrying the same deletion is
idempotent after the user removes the blocker; the CLI never evicts or deletes
that workload.

## Consequences

- A short homogeneous command and a full Scenario file have identical domain
  semantics and reconciliation behavior.
- Scripts cannot accidentally inherit a changed shell or kubeconfig current
  context.
- Receipts distinguish rejected preflight, accepted asynchronous work,
  terminal failure, and successful convergence.
- UID and generation preconditions make name reuse and concurrent automation
  fail closed.
- Server dry-run exercises target-specific policy without reserving state or
  overstating race safety.
- The first CLI remains small; installation, bulk operations, forced cleanup,
  remote Scenario fetching, and richer workflow orchestration require separate
  future decisions.

## Evidence

- [CLI and cluster safety decision](https://github.com/LinkMaq/kube-accelerator-sim/issues/5)
- [Revisioned Scenario Instance contract](https://github.com/LinkMaq/kube-accelerator-sim/blob/main/docs/adr/0003-revisioned-scenario-instance-contract.md)
