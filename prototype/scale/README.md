# PROTOTYPE — 1,000-Node scale boundary

This throwaway prototype answers
[#4](https://github.com/LinkMaq/kube-accelerator-sim/issues/4):

> On a developer-class local environment, what latency, resource usage,
> failure-recovery, observation, and cleanup characteristics appear for one
> `scheduling` Scenario Instance containing 1,000 Synthetic Nodes and 8,000
> Accelerators?

The experiment creates a disposable kind cluster, installs checksum-pinned
KWOK, and uses a small concurrent API driver to apply one homogeneous Node
Group with 1,000 replicas and one `nvidia.com/gpu` Accelerator Pool with count
eight. It measures convergence, repeated observation, 10% health loss and
recovery, 100 representative full-node Accelerator Pods, controller outage
recovery, exact-UID cleanup, kind-container resource usage, and etcd database
size.

kind remains an End-to-End Test Harness. Nothing in this prototype gives the
product CLI cluster-lifecycle responsibility.

## Run

Requirements: Intel macOS, Docker, `kubectl`, `curl`, `jq`, `shasum`, and
roughly 8 GiB assigned to the Docker runtime.

```sh
./prototype/scale/run.sh
```

All generated data is placed in the ignored `.results/` directory. The
prototype is retained only on its throwaway branch; the validated performance
contract is recorded on the issue and in the main architecture documentation.
The two captured full-scale trials and proposed gates are summarized in
[`EVIDENCE.md`](EVIDENCE.md).
