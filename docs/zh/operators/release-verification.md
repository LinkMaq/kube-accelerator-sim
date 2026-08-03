# 发布验证

发布只由手动的 `Evidence-gated release` 工作流构建。候选版本必须绑定一个准确 tag
提交，并提供完整 Kubernetes 兼容矩阵、Kubelet Device Plugin 协议基准以及两轮
1,000 Node 规模门禁的成功运行 ID。证据校验器会在打包前拒绝缺失版本行、来源修订
不一致、失败结果、数量缩水、身份漂移和所属对象泄漏。

发布制品包括五种原生 `kasim` CLI 归档、确定性 Helm TGZ、规范化证据、依赖清单、
`release-receipt.json`、SPDX JSON SBOM、校验和及 Sigstore bundle。控制器镜像发布
Linux amd64/arm64，并带 BuildKit SBOM 与来源证明；同一个 Chart TGZ 也作为 OCI
制品发布。

在下载目录中验证文件：

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

按 Registry manifest 中的不可变摘要验证控制器镜像，并拉取 Chart：

```sh
cosign verify \
  --certificate-identity-regexp \
  '^https://github.com/LinkMaq/kube-accelerator-sim/.github/workflows/release.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/linkmaq/kube-accelerator-sim-controller@sha256:REPLACE_WITH_DIGEST

helm pull oci://ghcr.io/linkmaq/charts/kasim-runtime \
  --version 0.1.0
```

`release-receipt.json` 是公开表面和兼容性的权威回执。支持范围只对应其中兼容性锁
记录的准确 Kubernetes patch 与模式；`1.30–1.36` 不是开放式 `1.30+` 承诺。
