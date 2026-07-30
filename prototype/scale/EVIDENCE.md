# Scale prototype evidence

The default 1,000-Synthetic-Node / 8,000-Accelerator experiment passed twice
consecutively on the same reference environment:

- Intel macOS host: 8 logical CPUs and 16 GiB memory
- Docker runtime: 4 CPUs and 7.753 GiB memory
- kind `v0.32.0`
- Kubernetes `v1.36.1`
- KWOK `v0.8.0`
- Scenario: 1,000 homogeneous Synthetic Nodes, eight `nvidia.com/gpu` per
  Node, 100 health changes, and 100 representative Pods requesting eight
  Accelerators each

## Results

| Measurement | Run `50006` | Run `52456` | Slow or high value |
| --- | ---: | ---: | ---: |
| Create 1,000 Node objects | 16.541s | 16.478s | 16.541s |
| Converge all Nodes Ready with 8,000 available Accelerators | 98.271s | 96.681s | 98.271s |
| Add exact ownership to 1,000 Leases | 9.296s | 6.957s | 9.296s |
| Apply-to-Ready-and-owned total | 124.108s | 120.116s | 124.108s |
| Full-instance observation p95, 10 samples | 600ms | 398ms | 600ms |
| Set 100 Nodes from 8 healthy to 0 | 4.114s | 2.012s | 4.114s |
| Restore 100 Nodes from 0 healthy to 8 | 2.617s | 1.649s | 2.617s |
| Create and run 100 full-node Accelerator Pods | 17.360s | 4.614s | 17.360s |
| Controller restart and all-Node Ready recovery | 69s | 67s | 69s |
| Delete 1,000 exact-UID Nodes and Leases | 44.141s | 44.711s | 44.711s |
| Peak kind control-plane container memory | 1.323 GiB | 1.252 GiB | 1.323 GiB |
| Peak process count | 269 | 272 | 272 |

All 100 representative Pods reached `Running`. Both runs restored all 1,000
Nodes after 100 Ready conditions were forced false during a KWOK controller
outage. Exact-UID cleanup left zero owned Nodes and zero owned Leases, and no
temporary kind cluster remained.

The four-CPU Docker runtime was saturated during convergence. This is expected
for the reference performance tier; latency and completion, rather than the
instantaneous Docker CPU percentage, are the stable acceptance signals.

## Storage behavior

The etcd database file grew from roughly 1 MiB at baseline to 50–52 MiB when
the Nodes became Ready. It was 71–78.5 MiB immediately after successful
cleanup. Kubernetes deletion removes live API objects but does not immediately
compact or defragment etcd history and tombstones.

Therefore a successful Scenario Instance deletion proves zero owned live
objects, not etcd disk reclamation. The simulator must not attempt etcd
maintenance in a user-owned Simulation Target. Long-running test environments
own their normal etcd compaction and defragmentation policy.

## Proposed specification gates

The `scheduling` scale profile uses a reference environment with at least four
Docker CPUs and 8 GiB Docker memory. A release candidate passes only when two
consecutive trials each satisfy:

- exactly 1,000 Ready Synthetic Nodes and 8,000 capacity and allocatable
  Accelerators within 180 seconds of apply acceptance, including exact Lease
  ownership;
- full Scenario Instance observation p95 at or below 2 seconds over ten
  sequential samples;
- loss or recovery of 100 Nodes worth of aggregate health within 15 seconds
  per revision;
- 100 representative full-node Accelerator Pods all `Running` within 60
  seconds;
- controller restart and full Ready recovery within 120 seconds;
- exact-UID Node and Lease cleanup, with zero live-object leaks, within 120
  seconds;
- peak kind control-plane container memory at or below 2 GiB;
- no API error, controller crash, or silent reduction of the requested object
  count.

These gates define the maintained local reference tier, not an unlimited
capacity claim. Smaller developer environments may still run the product, but
the 1,000/8,000 scale guarantee applies only to an environment meeting the
declared reference resources.

## Verdict

Keep 1,000 Synthetic Nodes and 8,000 Accelerators as a supported release
validation target for the default `scheduling` Fidelity Mode. Run it as a
dedicated scale job rather than a normal unit or pull-request test. The observed
margin supports the target without adding another runtime or weakening the
Scenario contract.
