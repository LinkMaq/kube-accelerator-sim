# PROTOTYPE — kasim ui layout options

Question: which single-page layout makes Kasim-owned accelerator signals,
non-Kasim Nodes, native DRA devices, scalar extended resources, auxiliary RDMA
signals, evidence, and stale/partial state easiest to understand at first scan?

This is a throwaway UI prototype. It uses deterministic fixture data, never
connects to Kubernetes, and must not be promoted directly into production.

Run it with one command:

```sh
go run ./prototypes/kasim-ui
```

Then open:

- `http://127.0.0.1:18080/?variant=A` — evidence-first ledger
- `http://127.0.0.1:18080/?variant=B` — node operations workspace
- `http://127.0.0.1:18080/?variant=C` — ecosystem matrix

Use the floating switcher or Left/Right arrow keys to change variants. Add
`mode=partial` or `mode=stale`, and switch language from the header. The URL
preserves the active variant and inspection state.

## Verdict

Variant A, the evidence-first ledger, wins. It is the only layout that keeps
the exact resource or DRA identity, quantities, health truth, provenance, and
Kasim ownership visible together on the home surface. Production should keep
its summary band and ledger, use Variant B's inspector as the Node/detail
drawer, and reserve Variant C's ecosystem comparison for an optional filtered
view rather than the default home page.

The production rewrite should use standards-based HTML, CSS, and JavaScript
with no chart or UI framework. This prototype's uncompressed static assets are
under 34 KiB before screenshots. It validates the interaction and information
hierarchy only; its code, fixtures, and styling are not production quality.
