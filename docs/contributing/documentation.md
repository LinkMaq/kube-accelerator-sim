# Keep English and Chinese documentation in sync

The online documentation is built directly from the repository's canonical
Markdown under `docs/`. Do not create a second copy of product documentation
for the website.

## Documentation is part of a feature

Every product-facing change must update the relevant documentation in the same
pull request. This includes changes to:

- CLI commands, flags, receipts, errors, or target-selection behavior;
- Scenario, Vendor Profile, Resource Contract, or Fidelity Mode semantics;
- runtime installation, permissions, ownership, upgrade, or cleanup;
- supported Kubernetes versions or tested compatibility claims;
- bundled profiles, models, resource signals, or examples;
- published binaries, images, Helm charts, and release verification.

Update `CONTEXT.md` or an ADR when the domain language or an architectural
decision changes. Update `docs/.vitepress/config.mts` when a new durable page
needs to appear in navigation.

English specifications and ADRs are the normative design records. Public
operator pages must keep the English source under `docs/operators/` and its
Chinese counterpart under `docs/zh/operators/` aligned in the same change.
When a deep design record remains English-only, the Chinese navigation must
label it as English and provide a Chinese architecture summary instead of
silently presenting it as translated content.

## Local workflow

Install the pinned documentation dependency and build the site:

```sh
npm ci
npm run docs:build
```

For local authoring with hot reload:

```sh
npm run docs:dev
```

The production site uses `/kube-accelerator-sim/` for English and
`/kube-accelerator-sim/zh/` for Chinese. Test the production build before
relying on a link that works only in the development server.

## Pull-request gate

CI compares the pull request with its base commit. A change to a product path
such as `api/`, `cmd/`, `internal/`, `profiles/`, `examples/`, `charts/`, or
`config/crd/` must include a canonical Markdown change under `docs/`, or an
appropriate update to `README.md`, `CONTEXT.md`, `examples/README.md`, or
release notes.

Test-only, workflow-only, and Agent-only changes do not trigger the gate unless
they also change a product surface. Passing the gate means documentation was
included; reviewers still verify that it accurately describes the behavior
and that affected English and Chinese operator pages remain aligned.

After a change reaches `main`, the GitHub Pages workflow rebuilds and deploys
the site automatically.
