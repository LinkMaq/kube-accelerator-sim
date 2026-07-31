# kube-accelerator-sim

`kube-accelerator-sim` (`kasim`) projects source-backed accelerator resource
contracts into an explicitly selected, already-existing Kubernetes cluster.
It is intended for platform scheduling, inventory, admission, and integration
tests when physical accelerators are unavailable.

The product supports bounded Kubernetes versions 1.30–1.36. Scalar
extended-resource scheduling is supported across that range; stable
`resource.k8s.io/v1` DRA control-plane projection is supported on 1.34–1.36.
The CLI does not manage cluster lifecycle. Test-only workflows may create
disposable clusters, but that behavior is not a product command.

## Quick start

Build the CLI and inspect the bundled, evidence-backed catalog without
contacting a cluster:

```sh
go build -trimpath -o ./dist/kasim ./cmd/kasim
./dist/kasim profile list -o json
./dist/kasim apply -f ./examples/single-node-single-accelerator.yaml \
  --dry-run=client \
  -o json
```

Install the shared runtime separately into an existing cluster, always naming
both the kubeconfig and context:

```sh
kubectl --kubeconfig ./target.kubeconfig --context target \
  create namespace kasim-system

helm upgrade --install kasim-runtime ./charts/kasim-runtime \
  --kubeconfig ./target.kubeconfig \
  --kube-context target \
  --namespace kasim-system \
  --wait
```

Then submit and observe a scenario:

```sh
./dist/kasim apply -f ./examples/single-node-single-accelerator.yaml \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json

./dist/kasim status single-node-single-accelerator \
  --kubeconfig ./target.kubeconfig \
  --context target \
  -o json
```

Continue with the [operator quickstart](docs/operators/quickstart.md) for exact
receipt handling, health and scale revisions, safe deletion, and runtime
cleanup.

## Deliberate fidelity boundary

The simulator writes Kubernetes control-plane objects only. It does not
provide device access, does not execute accelerator compute, does not install
vendor drivers, does not provide vendor telemetry, does not simulate NUMA
topology, and does not inject CDI devices. It also does not claim CUDA, ROCm,
CANN, firmware, device-file, collective-communication, or Pod runtime
fidelity.

`scheduling` proves scalar capacity, allocatable accounting, placement, and
safe lifecycle behavior. `dra-control-plane` proves stable DRA API inventory
and scheduler allocation, not node preparation. A separate
[kubelet protocol oracle](docs/operators/kubelet-protocol-oracle.md) tests the
Device Plugin protocol in test infrastructure without changing the product
fidelity claim.

## Documentation

- [Scenario examples](docs/operators/scenario-examples.md)
- [Vendor profile evidence and support classes](docs/operators/profile-evidence.md)
- [Runtime installation and permissions](docs/operators/runtime-installation.md)
- [Kubernetes compatibility](docs/operators/kubernetes-compatibility.md)
- [Upgrade and rollback](docs/operators/upgrade-rollback.md)
- [Troubleshooting and security](docs/operators/troubleshooting-security.md)
- [Release verification](docs/operators/release-verification.md)
- [Final v1 audit](docs/operators/final-audit.md)
- [Normative requirement traceability](docs/operators/requirement-traceability.md)
- [v1 product specification](docs/spec/v1.md)

## Development verification

The normal local gate is:

```sh
make verify
```

It checks formatting, static analysis, unit/integration/race tests,
architecture rules, generated traceability, Markdown links, executable
examples, and Helm rendering across the frozen Kubernetes range. Real
scheduler, DRA, protocol-oracle, scale, and release evidence is produced by
the dedicated GitHub Actions workflows.
