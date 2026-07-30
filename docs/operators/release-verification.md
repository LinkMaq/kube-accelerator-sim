# Release verification

Releases are built only by the manual `Evidence-gated release` workflow. A
release candidate must use one exact tag commit and supply successful workflow
run IDs for the full Kubernetes compatibility matrix, the kubelet Device Plugin
protocol oracle, and the two-trial 1,000-Node scale gate. The evidence verifier
rejects missing rows, mismatched source revisions, failed outcomes, reduced
counts, identity drift, and owned-object leaks before packaging begins.

The published asset set contains five native `kasim` CLI archives, a
deterministic Helm TGZ, normalized evidence, `release-dependencies.json`,
`release-receipt.json`, an SPDX JSON SBOM, `cli-checksums.txt`,
`checksums.txt`, and a Sigstore bundle for the checksum manifest. The
controller image is published for Linux amd64 and arm64 with BuildKit SBOM and
provenance attestations. The same chart TGZ is pushed as an OCI artifact.

Verify downloaded files from the release directory:

```sh
sha256sum --check checksums.txt
gh attestation verify kasim_0.1.0_linux_amd64.tar.gz \
  --repo LinkMaq/kube-accelerator-sim
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp \
  '^https://github.com/LinkMaq/kube-accelerator-sim/.github/workflows/release.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Verify the published controller image and chart by immutable digest obtained
from their registry manifests:

```sh
cosign verify \
  --certificate-identity-regexp \
  '^https://github.com/LinkMaq/kube-accelerator-sim/.github/workflows/release.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/linkmaq/kube-accelerator-sim-controller@sha256:REPLACE_WITH_DIGEST

helm pull oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.1.0
```

`release-receipt.json` is the authoritative public-surface and compatibility
receipt. Support is bounded to the exact Kubernetes patches and modes in its
embedded compatibility lock; `1.30-1.36` is not an open-ended `1.30+` promise.
The scheduling modes do not claim physical devices, computation, vendor
drivers, telemetry, CDI injection, NUMA behavior, or container device access.
