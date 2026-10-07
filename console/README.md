# Operator console

This is the gateway's API-driven React/TypeScript console. It uses Arco Design,
UnoCSS and i18next with matching Simplified Chinese and English translations.
It has no mock-data fallback. NomiFun does not operate a gateway instance or
sell API access; every deployed service is operated independently.

Use Node.js 24 or later:

```sh
npm ci --ignore-scripts
npm run typecheck
npm test
npm run licenses
npm run build
```

`npm run build` generates `console/dist`, retains runtime dependency licenses,
then copies the build into `internal/console/assets`. The latter is tracked and
embedded by Go, so `go build ./cmd/model-gateway` needs no frontend runtime. Do not
ignore or delete embedded assets when packaging the gateway. Ordinary
`console/dist` and `node_modules` are rebuildable outputs.

For local development, run the Go gateway on its default `127.0.0.1:8789`, then use
`npm run dev` and open `http://127.0.0.1:5173/console/`. Vite proxies API requests
to the local gateway. The production console is served at `/console/` by the
same binary as `/api/console/v1`.

Set `NMG_DEV_PROXY` to a different local gateway origin when using an isolated
development instance. The proxy preserves the browser's Host and Origin pair
so console writes still pass the gateway's same-origin protection.

The console adapts eight selected components from the user-owned local
`portal/codevisual` library: StatCard, UsageMeter, StateEmpty, StateError,
Toast, Filters, CommandPalette and the configuration checklist adapted from
HealthCheck. The copyright owner explicitly authorized Apache-2.0
transplantation. Source SHA-256 snapshots, authorization and changes are recorded
in `licenses/codevisual/provenance.json` and distributed frontend notices.
The provided source has no Git commit; the record does not invent one.

These are real component integrations with controlled input, live API data,
actions and keyboard navigation. Gallery defaults, fake trends, simulated
health/latency and looping/fade-out animations are removed. Motion 13.4.6 and
Lucide React 1.49.0 are pinned and audited. Existing Arco table pagination and
dialog focus management remain in use. No commercial CodedVisuals source or
extracted map/face assets are included.

Light and dark themes share the same semantic styles; theme selection lasts
for the current page session. Animations respect `prefers-reduced-motion`.

User and administrator workspaces have separate navigation. Page hashes retain
the current destination on reload and support browser history. Ctrl/Cmd+K opens
page search. Narrow screens use a navigation drawer, stacked panels and table
scrolling. Lists provide search, relevant status filters and explicit empty
states. Editors validate each field and keep required inputs visible while
optional access controls live in an advanced section.

The Vite 7 maintenance line is pinned deliberately. Vite 8's required
Lightning CSS dependency is MPL-2.0, which violates this project's
permissive-only dependency policy. The built-in esbuild JSX transform avoids
non-allowlisted build dependencies. All direct, development, optional and
transitive packages are audited through the npm v3 lockfile.

Money is entered in integer minor currency units. Native bigint and
`json-bigint` preserve all int64 values through JSON, including values beyond
JavaScript's safe-integer range. API keys and redeem codes are shown once in
memory only. Upstream and merchant credentials are write-only. An account
session bearer uses `sessionStorage`; sign-out revokes the server session
before removing the local bearer. Language preference is the only value stored
in `localStorage`.

User controls cover account overview, model catalog, scoped API keys, usage,
subscription purchase, wallet recharge, checkout status and code redemption.
Administrator controls cover channels and probes, models and prices, access
controls and audited credit, plans, payment reconciliation, redeem codes,
operator branding, official merchant providers, audit logs, ledger and request
reservation reconciliation. Official WeChat Native checkout codes render as QR
images; external checkout and operator links require credential-free HTTPS.
