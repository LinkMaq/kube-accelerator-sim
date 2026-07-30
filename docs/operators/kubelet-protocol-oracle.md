# Kubelet protocol oracle

The kubelet protocol oracle is release evidence, not a product runtime mode.
It runs a small test-only Device Plugin against a real kubelet in a disposable
kind cluster. The product continues to expose exactly two Fidelity Modes:
`scheduling` and `dra-control-plane`.

The weekly and manually dispatched `protocol-oracle.yml` workflow validates
the release-selected Kubernetes 1.30 floor and 1.36 ceiling on Linux. Each row
uses a digest-pinned kind node image and builds the oracle image directly from
the tested source revision.

## Evidence covered

The harness checks:

- Device Plugin v1beta1 registration with a real kubelet;
- Node capacity and allocatable publication for two test-only devices;
- `ListAndWatch` transitions from healthy to unhealthy and back;
- scheduler placement and kubelet `Allocate` invocation;
- plugin Pod replacement and re-registration;
- removal of the DaemonSet, Pods, Namespace, and oracle Unix socket.

Every successful row uploads a separate
`kasim.io/protocol-oracle-receipt/v1alpha1` JSON artifact. It records the
source revision, timestamp, Kubernetes server and kubelet versions, exact kind
node image, locally built oracle image ID, Dockerfile checksum, harness tool
versions, duration, outcomes, and exclusions.

## Deliberate exclusions

The oracle has no physical accelerator, vendor driver, host device mount, CDI
injection, or accelerator computation. Its `Allocate` response contains only
an environment variable identifying deterministic fake device IDs. The
test-only DaemonSet is not privileged and no node agent is installed by the
product Helm chart.

The oracle may mount the kubelet Device Plugin socket directory and run as
UID 0 inside the disposable test container because the kubelet socket is
root-owned. That permission is confined to the ephemeral test DaemonSet and
must not be copied into the controller or runtime chart.

## Manual release check

Trigger **Kubelet protocol oracle** from GitHub Actions before a release, then
retain both row receipts. A missing receipt, failed cleanup assertion, or
failed matrix row blocks the protocol claim; it does not downgrade silently
to simulated control-plane evidence.
