# NomiFun Model Gateway

Apache-2.0 Go gateway software and the frozen **NomiFun Model Gateway Protocol v1**.
The repository contains native relay, persistent channels and affinity, integer
billing, account/API-key management, official merchant adapters, an embedded
operator console, a mock, reusable conformance checks, and local release tools.

**NomiFun does not operate gateway instances, sell API access, or run commercial
services.** Community partners independently operate their gateways and are
responsible for pricing, subscriptions, service terms, privacy and operations.
The NomiFun Desktop provider is optional; users enter the partner's URL and key
at runtime, and partners do not rebuild the desktop application.

NomiFun 官方不运营网关、不出售 API，也不做商业化运营。网关由社区伙伴独立运营；
桌面端的网关 Provider 是可选项，不改变其他供应商的地位，也不与其他功能绑定。
源码实现、本地合成验收、真实上游/商户验收和生产容量证据分别报告，不能互相替代。

## Run the application

Use Go **1.27.1** and Node.js **24.18.0**. Install and build the console first:

```sh
cd console
npm ci --ignore-scripts
npm run build
npm test
cd ..
go build ./cmd/model-gateway
```

The console build is copied into `internal/console/assets` and embedded in the
application binary. These assets are deliberately tracked. `console/dist` is
rebuildable; do not ignore or discard the embedded assets.

Set `NMG_MASTER_KEY` to your own random 32-byte base64 key, and set
`NMG_ADMIN_EMAIL` / `NMG_ADMIN_PASSWORD` for first-start administrator bootstrap.
Use environment variables or operator secret management, keeping credentials
outside source control and command arguments. Then:

```sh
go run ./cmd/model-gateway
```

The default listener is `127.0.0.1:8789`, console `/console/`, and database
`data/gateway.db`. Branding, models, channels, prices, plans, registration and
merchant configuration are runtime admin settings. Payments remain disabled
until the operator supplies and validates merchant configuration.

For production PostgreSQL or independent SQLite container deployments, see
[operations](docs/operations/deployment.md), [backup and recovery](docs/operations/backup-and-recovery.md)
and `.env.example`. The Dockerfile uses pinned official Node/Go images, a
CGO-free build and a non-root scratch runtime. Compose publishes the gateway
only to host loopback by default. No example contains a production secret.

## Native behavior and billing

The gateway relays same-protocol OpenAI Chat/Responses, Anthropic, Gemini,
OpenAI-compatible and Azure OpenAI requests. It preserves native content and
streams while parsing usage beside the stream. Cross-protocol conversion,
Bedrock/Vertex, audio/video/Realtime, MySQL and multi-node HA are outside this
release scope. Optional Anthropic count_tokens is currently undeclared and
returns 501; it is not required by protocol v1.

Responses continuation is bound to an upstream account; Anthropic session/cache
routing is also bound. Accounts, keys, channels and merchant secrets persist in
SQLite or PostgreSQL with forward migrations. User keys are hashed and shown
once at creation; upstream and merchant credentials use AES-GCM with the
operator's master key. Request and response content is not logged by default.

Amounts are integer minor currency units. Reservations, settlement, credits and
payment events are idempotent. A possibly accepted upstream request without
complete usage keeps its hold for explicit, audited reconciliation. A browser
success redirect does not credit a wallet. See [reconciliation](docs/operations/reconciliation.md).

The current deployment supports one gateway process per database. Instance
locks protect migration/recovery; limit counters remain process-local. Redis,
shared limits and multi-node high availability are not implemented or claimed.

## Mock and protocol conformance

[Protocol v1](docs/protocol/v1.md) and [OpenAPI](openapi.yaml) define the public
contract independently of this implementation. The deterministic mock contacts
no upstream and incurs no model/payment costs:

```sh
go run ./cmd/mock-gateway
```

It listens on `127.0.0.1:8788`, using synthetic key `nmg_mock_development`.
`NMG_MOCK_API_KEY` and `--listen` override those test settings.

In a second PowerShell terminal:

```powershell
$env:NMG_BASE_URL = 'http://127.0.0.1:8788'
$env:NMG_API_KEY = 'nmg_mock_development'
$env:NMG_FIXTURE_FILE = 'testdata/conformance.json'
go run ./cmd/conformance
```

Or Bash:

```sh
NMG_BASE_URL='http://127.0.0.1:8788' \
NMG_API_KEY='nmg_mock_development' \
NMG_FIXTURE_FILE='testdata/conformance.json' \
go run ./cmd/conformance
```

For another gateway, provide a controlled key, authorized native models and any
pre-provisioned optional error/key scenarios in your fixture. The suite sends
native inference requests; unavailable optional scenarios are explicit skips.
Production fixtures should leave count_tokens disabled when it is undeclared.

## Local validation and licensing

```sh
go test ./...
go vet ./...
go build ./...
node --test scripts/check-frontend-licenses.test.mjs
bash scripts/check-licenses.sh
```

Windows license check:

```powershell
pwsh -NoProfile -File scripts/check-licenses.ps1
```

The pinned Apache-2.0 `go-licenses/v2@v2.0.1` gate covers all Go packages,
imported transitive dependencies and test dependencies. The frontend gate covers
all npm lockfile v2/v3 entries, including dev, optional and transitive packages,
with resolution coverage, exact versions and valid archive integrity. Allowed
licenses are Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, 0BSD, ISC, Unlicense and
CC0-1.0. Unknown metadata, copyleft alternatives and unsupported lock layouts
fail closed. Source provenance and retained notices are also reviewed; copied
code still requires focused native validation.

The implementation is original; no gateway source was copied from upstream
projects. Future borrowing must pin a permissive source commit, retain notices
and identify modifications. GPL/LGPL/AGPL and unknown-license code are prohibited.
See [LICENSE](LICENSE), [NOTICE](NOTICE), [AGENTS.md](AGENTS.md) and
[license audit](docs/security/license-audit.md).

Validation runs locally with the commands above. Build the locked console before
Go checks and run race checks in a local Linux environment.
[Acceptance evidence and open external gates](docs/operations/acceptance.md)
distinguish deterministic native/payment fixtures from real upstream and merchant
acceptance. No live OpenAI/Anthropic/Gemini or merchant transaction is implied by
a mock or synthetic signed callback.

The [M0–M4 local delivery record](docs/validation/delivery.md) consolidates gateway,
Desktop WebUI, real Step text, container and license evidence while preserving
the remaining native, upstream and merchant validation gaps.

The bounded [load tool](docs/operations/load-testing.md) defaults to cost-free
health requests and reports throughput, p95 and errors without printing secrets.
Partner onboarding and credential-free deep-link buttons are documented in the
[partner guide](docs/partners/integration.md). Before independently launching a
service, complete the [operator responsibility checklist](docs/compliance/operator-checklist.md)
and [security review](docs/security/review.md).
