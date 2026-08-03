## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the five default canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a single-context domain documentation layout. See `docs/agents/domain.md`.

### Accelerator simulation operations

Use the repository skill at `.agents/skills/operate-kasim/SKILL.md` when a user
asks to install the Kasim runtime, start or change simulated accelerator
devices, inspect a Scenario Instance, or safely remove simulator resources.

### Documentation synchronization

Every product-facing feature or behavior change must update its canonical
documentation in the same change. Read `docs/contributing/documentation.md`,
add new durable pages to `docs/.vitepress/config.mts`, and run
`npm run docs:build`. Public operator behavior must be updated in both English
under `docs/operators/` and Chinese under `docs/zh/operators/`. Do not
duplicate product documentation outside `docs/`.
