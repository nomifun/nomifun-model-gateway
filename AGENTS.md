# Repository guidelines

## Scope and architecture

- This repository is `nomifun-model-gateway`, independent of NomiFun Desktop
  and its existing platform gateway MCP crate. Do not rename it to
  `nomifun-gateway`.
- M0 is complete. The user has authorized continuous delivery through M1-M4:
  production relay, persistence, billing, payments, console, desktop integration
  and release preparation. Report milestone evidence while continuing. Do not
  publish, commit, push or operate an external paid instance without an explicit
  request. Preserve the frozen protocol and identify real external acceptance
  gaps independently of deterministic local tests.
- `docs/protocol/v1.md` and `openapi.yaml` are the public contract. A client must
  not depend on mock implementation details. Keep examples, mock behavior and
  conformance assertions consistent with the contract.
- Preserve native protocol payloads, errors, streaming events, thinking
  signatures and cache fields. Copying permissively licensed adapter code does
  not establish correctness; later milestones still require focused native
  protocol and real upstream validation when credentials are available.
- Official NomiFun does not operate gateway instances, sell API access, or run
  commercial services. Community partners independently operate their services.
  Keep this boundary explicit in documentation and UI text.

## Build and validation

Validation is local. Do not add GitHub Actions workflows or hosted CI unless
the user explicitly requests them.

Use the Go version pinned in `go.mod` (currently Go 1.27.1) and Node.js 24 or
later for the dependency-license audit. From this repository:

```sh
go test ./...
go vet ./...
go build ./...
bash scripts/check-licenses.sh
node --test scripts/check-frontend-licenses.test.mjs
```

Before Go checks, run `npm ci --ignore-scripts`, `npm run build` and `npm test`
in `console/`. Keep `internal/console/assets` tracked: it is embedded by Go and
is required for a Go-only build. `console/dist` is rebuildable. Docker packaging
also collects Go and frontend publisher license/NOTICE documents; binary
distribution must retain third-party notices, not only the project's LICENSE.

`cmd/load-test` is a bounded HTTP acceptance tool. It defaults to `/healthz`
with no key and uses `NMG_LOAD_*` environment configuration; URLs, keys and
bodies never appear in its numeric report. See the operations documentation.

On Windows, use the equivalent license command:

```powershell
pwsh -NoProfile -File scripts/check-licenses.ps1
```

Run the mock and reusable conformance client with `go run ./cmd/mock-gateway`
and `go run ./cmd/conformance`. The client reads `NMG_BASE_URL`, `NMG_API_KEY`
and `NMG_FIXTURE_FILE`; see README for local examples. Use synthetic test keys.
Do not place real credentials in source, fixtures, command-line arguments or
logs. Gemini query-key compatibility is confined to its native endpoints;
prefer the header form in client integrations.

Choose checks that directly cover the change. M0 validates deterministic local
protocol behavior; it does not prove upstream fidelity, production billing or
high availability. Record any check that was not run and why.

## License hygiene

- Apache-2.0 is the project license. Only clearly licensed permissive code and
  dependencies may be incorporated. GPL, LGPL, AGPL and other copyleft or
  unknown licenses are prohibited, including transitive and test dependencies.
- Do not copy or rewrite source from prohibited projects or put that source in
  an AI tool's context to generate this project's implementation. Public
  interface behavior can be studied without copying their implementation.
- A permissive borrowing must pin the exact source commit, verify the license
  at that commit, preserve copyright/license/NOTICE requirements, and identify
  modifications in the source header or NOTICE. Do not assume that a project's
  current license applies to every historical commit or vice versa.
- If referring to new-api code, the permitted source is only the confirmed
  Apache-2.0 commit `67eb7315` or an independently verified earlier permissive
  commit. Never use v0.8.8.0-alpha.5 or later source. Do not use New API branding
  or logos. sub2api source is prohibited.
- Check provenance before borrowing from one-hub or done-hub. Do not borrow
  subscription reverse-proxy channels for Claude Code, Codex, Gemini CLI or
  Antigravity. Use official merchant payment APIs in later milestones.
- `scripts/check-licenses.ps1` and `.sh` install the pinned Apache-2.0
  `go-licenses` v2.0.1 checker and inspect all Go packages, imported transitive
  dependencies and test dependencies against an explicit permissive allowlist.
  Unknown or unapproved license names fail the check. Investigate non-Go source
  warnings for additional license obligations before release.
- The same scripts discover every source
  `package.json` and audit every package in its adjacent npm `package-lock.json`
  v2/v3, including dev, optional and transitive dependencies. All license
  identifiers must be allowlisted, archives must have exact versions and
  integrity metadata, and manifests must match their lockfile. Every dependency
  reference must resolve in the lockfile; absent peers require an explicit
  optional flag. Copyleft
  alternatives in dual-license expressions are also rejected. Unknown/missing
  license metadata, unsupported lockfiles and local workspace links fail closed.
  Source provenance and retained notices still need human review before release.
- The explicit allowlist includes Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause,
  0BSD, ISC, Unlicense and CC0-1.0. Admitting this verified permissive license
  does not relax unknown or copyleft rejection.

## Git and workspace

- Preserve unrelated work and inspect staged paths before committing.
- Use the contributor's configured Git identity. Do not install attribution
  hooks or add AI signatures or `Co-Authored-By` trailers.
- Do not commit or push without an explicit request. Never use `--no-verify`,
  force-push or rewrite shared history without explicit authorization.
- Do not expose credentials or include runtime data in source control.
