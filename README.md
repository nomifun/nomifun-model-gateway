# NomiFun Model Gateway

[English](README.md) · [简体中文](README.zh-CN.md)

An Apache-2.0 gateway for community operators who want to provide model access,
manage users and API keys, and run their own billing service. It combines native
model relay, a persistent database, wallet and subscription accounting, official
merchant payment adapters, and an embedded web console in a Go application.

**NomiFun does not operate gateway instances, sell API access, or run commercial
services.** Community partners independently operate their services and are
responsible for prices, terms, privacy, payments and support. This repository is
`nomifun-model-gateway`, independent of NomiFun Desktop and its existing platform
gateway MCP crate. Desktop integration is optional; partners do not rebuild
Desktop to connect their instance.

## Start here

| Your goal | Where to start |
| --- | --- |
| See what the console offers | [Product screenshots](#product-screenshots) |
| Run your first local instance | [Install and start](#install-and-start) |
| Add upstream models and publish access | [Operator walkthrough](#operator-walkthrough) |
| Create a key and connect a client | [User walkthrough](#user-walkthrough), [native API examples](#native-api-examples) |
| Connect NomiFun Desktop | [Desktop connection](#desktop-connection) |
| Deploy or maintain a service | [Docker and operations](#docker-and-operations) |
| Develop against the protocol without paid requests | [Mock and protocol conformance](#mock-and-protocol-conformance) |
| Find the full documentation | [Documentation index](docs/README.md) · [中文文档导航](docs/README.zh-CN.md) |

## Features and current boundaries

| Area | Available behavior |
| --- | --- |
| Native relay | OpenAI Chat Completions and Responses, Anthropic Messages, Gemini, images, embeddings and Jina rerank, where the instance and model declare support. OpenAI-compatible and Azure OpenAI channels are supported. Requests stay in the same native protocol. |
| Console | English/Chinese UI, user and administrator workspaces, model catalog, API keys, usage, wallets, subscriptions, channels, model/pricing forms, payment orders, audit logs and reservation reconciliation. |
| Channel onboarding | Provider entry templates, bounded discovery or manual entry, public → upstream model mapping, task/endpoint/price configuration and publication checks. |
| Persistence and credentials | SQLite or PostgreSQL, forward migrations, hashed user keys shown once, and upstream/merchant credentials encrypted with the operator's master key. |
| Accounting | Integer minor currency units, request reservations, usage settlement, wallet credits, scoped key limits and an immutable ledger. Uncertain accepted requests remain for audited reconciliation. |
| Payments | Official Stripe, Alipay and WeChat Pay v3 merchant adapters. Disabled until the operator supplies and validates merchant configuration. |
| Client integration | Frozen Protocol v1, OpenAPI, reusable conformance client, and optional NomiFun Desktop provider with runtime URL/key entry. |

Cross-protocol conversion, Bedrock/Vertex authentication, audio/video/Realtime,
MySQL and multi-node HA are outside the current release scope. One database
supports **one running gateway process**; limit counters remain process-local.
Optional Anthropic `/v1/messages/count_tokens` is currently undeclared by the
application and returns 501; Protocol v1 does not require it.

Native streams, errors, thinking signatures, encrypted reasoning and cache
fields are preserved while usage is parsed beside the stream. Responses
continuation and Anthropic session routing retain upstream account affinity.
Request and response content is not logged by default. Local tests and
screenshots do not establish fidelity for every real upstream account, merchant
acceptance, sustained production capacity or high availability. See the
[delivery record](docs/validation/delivery.md) and
[acceptance boundaries](docs/operations/acceptance.md).

## Product screenshots

These capture the actual local console with synthetic users, channels, models
and accounting data. They contain no real upstream or merchant credentials.
Example balances and models do not represent an official NomiFun service or
real upstream/payment acceptance. [Capture provenance and refresh instructions](docs/screenshots/README.md).

**User overview** — balance, quota, recent usage and client connection entry.

![English user overview with synthetic account data](docs/screenshots/en/overview.png)

<details>
<summary>Model catalog and pricing</summary>

Inspect published models, tasks, limits and pricing before configuring a client.

![English model catalog and pricing](docs/screenshots/en/models.png)

</details>

<details>
<summary>API key management</summary>

Create scoped keys and review key status; plaintext is shown only at creation.

![English API key management](docs/screenshots/en/api-keys.png)

</details>

<details>
<summary>Administrator upstream channels</summary>

Manage native channels and their routing mappings.

![English administrator upstream channels](docs/screenshots/en/channels.png)

</details>

<details>
<summary>Provider onboarding wizard</summary>

Configure connection fields, discovery/mapping, model capabilities and publication review.

![English provider onboarding wizard](docs/screenshots/en/channel-onboarding.png)

</details>

## Install and start

### Prerequisites and build

Use the version pinned in [go.mod](go.mod), currently **Go 1.27.1**, and
**Node.js 24 or later** with npm. The pinned Docker build uses Node.js 24.18.0.
For containers, install Docker Engine and the Docker Compose plugin. A first
local SQLite instance requires no external database service.

From a checked-out repository, run these steps in Bash or PowerShell:

```sh
cd console
npm ci --ignore-scripts
npm run build
npm test
cd ..
go build ./cmd/model-gateway
```

The frontend build copies output into `internal/console/assets`, which Go
embeds in the application. These assets are tracked and required for a Go-only
build. `console/dist` is rebuildable.

### First start: Bash

Generate the master key **once for a new database**. Enter the administrator
email and a password of 12–72 UTF-8 bytes. These commands do not print the key or
password or place them in command arguments:

```bash
export NMG_MASTER_KEY="$(node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("base64"))')"
read -r -p 'Administrator email: ' NMG_ADMIN_EMAIL
export NMG_ADMIN_EMAIL
read -r -s -p 'Administrator password (12–72 bytes): ' NMG_ADMIN_PASSWORD
printf '\n'
export NMG_ADMIN_PASSWORD
go run ./cmd/model-gateway
```

### First start: PowerShell 7

```powershell
$env:NMG_MASTER_KEY = [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:NMG_ADMIN_EMAIL = Read-Host 'Administrator email'
$nmgBootstrapPassword = Read-Host 'Administrator password (12–72 bytes)' -AsSecureString
$env:NMG_ADMIN_PASSWORD = [Net.NetworkCredential]::new('', $nmgBootstrapPassword).Password
Remove-Variable nmgBootstrapPassword
go run ./cmd/model-gateway
```

Before configuring channels, back up the exact master key in operator-owned
secret storage. Reuse it with the same database for every restart; regenerating
it makes encrypted stored credentials unreadable. There is no automatic
master-key rotation tool. These examples set the current process environment;
persist secrets through your own protected environment or secret manager.
The Go application **does not automatically load `.env`**.

Open [http://127.0.0.1:8789/console/](http://127.0.0.1:8789/console/) and sign in
with the bootstrap credentials. A new instance starts with registration disabled
and no configured upstream service. Bootstrap variables do not reset passwords
or promote existing accounts. After successful bootstrap, remove the two admin
variables from subsequent service environments. Stop the foreground process
with Ctrl+C.

| Local address/path | Purpose |
| --- | --- |
| `http://127.0.0.1:8789` | API root; enter this root in integrations |
| `/console/` | Embedded user and administrator console |
| `/healthz` | HTTP process liveness |
| `/readyz` | Readiness including database availability |
| `/nomifun/v1/meta` | Anonymous protocol/instance metadata |
| `data/gateway.db` | Default persistent SQLite database, relative to the working directory |

A running server does not make inference available. Complete the operator
walkthrough, fund a test account, create a key and verify a declared model first.

## Configuration

These are application process variables; business settings are edited in the
authenticated administrator console.

| Variable | Default | Meaning |
| --- | --- | --- |
| `NMG_MASTER_KEY` | Required | Standard base64 of 32 random bytes; encrypts upstream/merchant secrets. Back up separately. |
| `NMG_ADMIN_EMAIL`, `NMG_ADMIN_PASSWORD` | Empty | Paired first-start administrator bootstrap; password 12–72 bytes. Do not overwrite existing accounts. |
| `NMG_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` (`postgresql` also accepted). |
| `NMG_DB_DSN` | `data/gateway.db` | SQLite local file path, or PostgreSQL DSN. Production SQLite rejects memory/URI options. |
| `NMG_LISTEN` | `127.0.0.1:8789` | HTTP listener; containers use `0.0.0.0:8789` internally. |
| `NMG_CONNECT_TIMEOUT` | `10s` | Upstream connection/TLS timeout. |
| `NMG_UPSTREAM_HEADER_TIMEOUT` | `60s` | Upstream response-header timeout. |
| `NMG_STREAM_IDLE_TIMEOUT` | `90s` | Timeout since the last upstream bytes; streams have no total-duration timeout. |
| `NMG_RESPONSE_AFFINITY_TTL` | `720h` | Responses account-binding retention; maximum `8760h`. |
| `NMG_REQUEST_BODY_LIMIT` | `67108864` | Maximum incoming request body in bytes. |
| `NMG_ANTHROPIC_VERSION` | `2023-06-01` | Native version default when the client omits `anthropic-version`. |

Timeouts are positive Go durations, at most `24h` except the affinity TTL.
Compose separately consumes `NMG_PORT` (host port, default 8789) and
`NMG_POSTGRES_*` bootstrap variables from [.env.example](.env.example).
`NMG_BASE_URL`, `NMG_API_KEY` and `NMG_FIXTURE_FILE` configure the conformance
client; they do not provision application users or channels.

## Operator walkthrough

1. **Identify the service.** Sign in and switch to the administrator workspace.
   In **Brand & settings**, enter your independent operator name, currency and
   HTTPS homepage, console, purchase, terms and privacy URLs. Decide whether to
   allow registration and which native endpoint families to declare.
2. **Create an upstream channel.** Open **Channels → Create**. Choose a provider
   template or custom kind (`openai`, `anthropic`, `gemini`, `compatible` or
   `azure`). Fill the upstream root URL and upstream API credential. Replace
   resource/workspace placeholders. Templates fill connection fields; they do
   not prove account access or configure capabilities and prices.
3. **Discover or enter mappings.** Read a model list where supported, or enter
   IDs manually. Map a stable public model ID to the exact upstream ID or Azure
   deployment name. Keep product/region prefixes in the upstream root. Discovery
   only adds selected missing mappings; it does not overwrite existing IDs,
   prices or access policy. Partial discovery has explicit warnings.
4. **Confirm capabilities and prices.** Set tasks, available native endpoints,
   each task's preferred endpoint, input modalities, context/output limits,
   availability and access policy. Configure required price meters explicitly.
   Prices are integer minor currency units per `unit_size`: for example
   `amount: 2, unit_size: 1000, currency: "USD"` means 2 cents per 1,000 units
   of that meter. Missing pricing is not free; explicit `amount: 0` is free.
   Chat publication needs usable context/output limits.
5. **Check and save.** Run the wizard's publication check and fix field issues.
   Incomplete models remain disabled. Save persists the channel/new models
   atomically; creation cannot overwrite an existing public ID. The check verifies
   local configuration and routing without sending paid generation.
6. **Set up user access.** Use **Users** for account management and audited wallet
   credits. If needed, define **Subscription plans** and **Redeem codes**. Enable
   payment providers only after configuring and independently validating your
   own official merchant credentials and callbacks. Payment setup is unnecessary
   for inspecting the console or testing an administrator-funded account.
7. **Verify as a user.** Create a scoped key, inspect its catalog/account, then
   send a bounded native request to a declared model. Review **Usage**, **Wallet
   ledger** and **Reservations** together with upstream usage. Channel probes,
   discovery and inference acceptance are separate checks.

See the [provider guide](docs/operations/provider-presets.md) for root URL rules,
discovery limits and existing routes. Saved channel kind, root URL, API version
and credential form an immutable upstream account identity; prepare replacement
accounts as new channels. The last route of an enabled model cannot be removed;
add a replacement route or disable that model first. If a save result becomes unknown, refresh and confirm
its outcome before retrying a creation.

## User walkthrough

1. Open the operator's `/console/` and sign in. Register if enabled; otherwise
   contact that independent operator for account access.
2. Inspect **Model catalog** for public model IDs, tasks, endpoints, limits and
   prices. **Overview** shows wallet, plan/quota and recent activity.
3. In **API keys**, create a named key with expiry, model restrictions, quota,
   rate/concurrency limits and allowed IPs as needed. Copy the plaintext shown
   once and store it privately. Key lists cannot recover it; create a replacement
   and revoke the old key if it is lost or exposed.
4. Use the API root and this **instance API key** in a native client or Desktop.
   A console login session is not an inference key; users do not need the
   operator's upstream or merchant credentials.
5. Use **Plans** or **Orders & wallet** if purchases are offered, or redeem an
   operator-issued code. Review order status after checkout: a success page does
   not credit funds; the gateway requires a verified payment event/query.
6. Inspect **Usage** for request IDs, settled charges and interrupted requests.
   Report the request ID and safe error details to the operator for support.

## Native API examples

[Protocol v1](docs/protocol/v1.md) and [OpenAPI](openapi.yaml) define the public
contract. Console `/api/console/v1` session APIs are internal management
interfaces, not the client integration contract.

| Request | Authentication | Use |
| --- | --- | --- |
| `GET /nomifun/v1/meta` | Anonymous | Protocol `1.0`, actual operator, declared capabilities/optional endpoints |
| `GET /nomifun/v1/catalog` | Instance API key | Rich model directory visible to this key |
| `GET /nomifun/v1/account` | Instance API key | Plan, balance, quota and rate-limit snapshot |
| `GET /v1/models` | Instance API key | Compatible model list with the same visible model set |

The same key accepts `Authorization: Bearer …`, `x-api-key` or `x-goog-api-key`.
Send one auth header; contradictory credentials are rejected with 401. Gemini
query-key compatibility is confined to its two native generate paths; use
headers in new integrations. Credential-bearing requests must not follow
redirects. Responses include `x-request-id` and `Cache-Control: no-store`.

Save this Node.js example as `client-example.mjs` in a private local directory
outside the repository. It reads credentials from the environment, defaults to
native Responses, and allows another declared protocol via `NMG_NATIVE_PROTOCOL`.
Use your key's public catalog ID for `NMG_MODEL`.

```javascript
const root = process.env.NMG_BASE_URL?.replace(/\/+$/, '');
const key = process.env.NMG_API_KEY;
const model = process.env.NMG_MODEL;
if (!root || !key || !model) throw new Error('Set NMG_BASE_URL, NMG_API_KEY and NMG_MODEL');

const variants = {
  chat: { path: '/v1/chat/completions', headers: { Authorization: `Bearer ${key}` },
    body: { model, messages: [{ role: 'user', content: 'Hello' }], max_tokens: 64 } },
  responses: { path: '/v1/responses', headers: { Authorization: `Bearer ${key}` },
    body: { model, input: 'Hello', max_output_tokens: 64 } },
  anthropic: { path: '/v1/messages', headers: { 'x-api-key': key, 'anthropic-version': '2023-06-01' },
    body: { model, messages: [{ role: 'user', content: 'Hello' }], max_tokens: 64 } },
  gemini: { path: `/v1beta/models/${encodeURIComponent(model)}:generateContent`,
    headers: { 'x-goog-api-key': key },
    body: { contents: [{ role: 'user', parts: [{ text: 'Hello' }] }],
      generationConfig: { maxOutputTokens: 64 } } },
};
const selected = variants[process.env.NMG_NATIVE_PROTOCOL ?? 'responses'];
if (!selected) throw new Error('Choose chat, responses, anthropic or gemini');
const response = await fetch(root + selected.path, {
  method: 'POST', redirect: 'error',
  headers: { ...selected.headers, 'Content-Type': 'application/json' },
  body: JSON.stringify(selected.body),
});
console.log('status:', response.status, 'request ID:', response.headers.get('x-request-id'));
console.log(await response.text());
if (!response.ok) process.exitCode = 1;
```

For a real operator instance, set `NMG_API_KEY` with a hidden prompt or secret
manager and run the script by its path. Never put real keys in source, fixtures,
URLs or command arguments. To try the cost-free bundled mock, start it as below
and use its synthetic fixture:

```bash
export NMG_BASE_URL='http://127.0.0.1:8788'
export NMG_API_KEY='nmg_mock_development'
export NMG_MODEL='mock-gpt'
export NMG_NATIVE_PROTOCOL='responses'
node /path/to/client-example.mjs
```

```powershell
$env:NMG_BASE_URL = 'http://127.0.0.1:8788'
$env:NMG_API_KEY = 'nmg_mock_development'
$env:NMG_MODEL = 'mock-gpt'
$env:NMG_NATIVE_PROTOCOL = 'responses'
node C:\path\to\client-example.mjs
```

Other mock selections are `chat` / `mock-compatible`, `anthropic` / `mock-claude`
and `gemini` / `mock-gemini`. A deployed instance does not automatically contain
these fixture IDs.

For streaming, set native `stream: true` for Chat/Responses/Anthropic, or use
Gemini `:streamGenerateContent?alt=sse`, and consume native SSE events
incrementally without cross-protocol conversion. This short sample buffers the
response and is intended for non-streaming requests. Other declared tasks use
`/v1/images/generations`, `/v1/images/edits` (native multipart), `/v1/embeddings`
and `/v1/rerank`; follow their native payloads and model `task_endpoints`.

Metered chat requires a positive explicit native output limit within the model's
maximum, as shown above. Wallet/quota must cover the conservative reservation
upper bound, which can exceed the final charge. Nullable account quantities mean
unknown/undisclosed, not zero or unlimited. Money is int64 in minor currency
units; preserve integer precision in client calculations.

## Desktop connection

1. Use a NomiFun Desktop version with the optional **NomiFun Model Gateway**
   provider. Installing this independent gateway does not install/update Desktop.
2. Select **Add gateway to Desktop** from the console overview, or choose that
   provider in Desktop and enter the operator's **API root URL**.
3. Check and confirm the operator, then enter the instance key you created.
   The `nomifun://add-provider` link only prefills provider type, URL and name;
   it never contains credentials.
4. Read the authorized catalog, explicitly select models, and add the selected
   models. Set usable native output limits. Import success does not prove model
   calls work; Desktop follows each task's declared preferred native endpoint.
5. Check account information and send a small bounded request. Purchase/renewal
   opens the partner's HTTPS page in the system browser without a key.

Default OpenAI-style client connections use the root plus `/v1`; Anthropic and
Gemini use the root with their own headers. Avoid adding `/v1` twice to native
paths. UI locations may vary by Desktop release; current evidence and known
handoff issues are in [partner integration](docs/partners/integration.md) and
[Desktop handoff](docs/partners/provider-onboarding-desktop-handoff.md).

## Docker and operations

Copy [.env.example](.env.example) to `.env` (`cp .env.example .env` in Bash,
`Copy-Item .env.example .env` in PowerShell). Privately fill the master key and
first-start admin values. `.env` is ignored; do not commit or distribute it.
Compose loads it explicitly. Keep the same master key across upgrades.

Choose **one** independent deployment file:

```sh
# SQLite: persistent named volume, no external database.
docker compose --env-file .env -f compose.sqlite.yaml config --quiet
docker compose --env-file .env -f compose.sqlite.yaml up -d --build
```

```sh
# PostgreSQL: also configure NMG_POSTGRES_PASSWORD and NMG_DB_DSN.
docker compose --env-file .env -f compose.yaml config --quiet
docker compose --env-file .env -f compose.yaml up -d --build
```

The PostgreSQL DSN host is internal service `database:5432`; percent-encode URI
password characters. Do not combine SQLite/PostgreSQL files. Gateway access is
published to host loopback by default. Docker pins official Node/Go images,
collects third-party notices and produces a CGO-free non-root scratch runtime
with embedded console and CA certificates. Provide your own public HTTPS proxy;
preserve SSE, cancellation and idle timeouts. A database permits one gateway
owner, not horizontal replicas.

Check readiness at `/readyz` and startup results with Compose `logs gateway`.
Protect logs; avoid dumping rendered configuration containing secrets.
`docker compose --env-file .env -f <chosen-file> stop` stops that project without
deleting its data volume.

| Operations task | Guide |
| --- | --- |
| TLS/proxy, database, probes, metrics, migrations, data permissions | [Deployment](docs/operations/deployment.md) |
| Back up database/matching master key and verify isolated restores | [Backup and recovery](docs/operations/backup-and-recovery.md) |
| Resolve unknown usage or pending orders with evidence | [Reconciliation](docs/operations/reconciliation.md) |
| Bounded health/native load checks and settlement inspection | [Load testing](docs/operations/load-testing.md) |
| Executed local checks and external gates | [Acceptance](docs/operations/acceptance.md), [delivery](docs/validation/delivery.md) |
| Independently launch a service | [Operator responsibilities](docs/compliance/operator-checklist.md), [security review](docs/security/review.md) |

## Troubleshooting

| Symptom | Check and action |
| --- | --- |
| Startup fails on master key | Use standard base64 of exactly 32 random bytes. Restore an existing database's original key; do not generate a replacement. |
| Administrator cannot sign in | Confirm paired bootstrap email/password on the first empty database and a 12–72 byte password. Variables do not reset existing users; do not bypass authentication by editing tables. |
| Port unavailable or database owned | Check `NMG_LISTEN` and the database owner. Stop only your own instance; keep one process per database. |
| Console stale or missing assets | Build the locked frontend before Go build, run the new binary and verify `internal/console/assets` exists. |
| Registration unavailable | Disabled by default; the operator can enable it in Brand & settings or arrange account access. |
| Discovery fails or is partial | Check kind, provider root/product/region, credential and placeholders. Review warnings and enter manual IDs where needed; discovery is not inference acceptance. |
| Model missing / `model_not_found` | Check publication, enabled routes, plan and key restrictions. Use the public ID, not the upstream deployment name. |
| 400 output-limit/config error | Supply the native positive output limit within the model maximum; check task/preferred endpoint and context/output metadata. |
| 401 or 403 | Use an unexpired instance API key, check plan and send one correct auth header. A console session/upstream key is not the user key. |
| 402 `insufficient_balance` | Fund the account via the operator; allow for the reservation upper bound rather than just expected final cost. |
| 429 `rate_limited` | Observe `Retry-After` and key request/token/concurrency limits; avoid immediate blind retries. |
| Continuation/session rejected | Keep Responses' original key/account binding. Expired/missing bindings fail closed; explicitly start a new request/session. |
| Interrupted request retains a hold | Review Reservations and verified upstream usage. A disconnect does not establish that the upstream did not charge. |
| No payment options / order pending | Payments require configuration; verify official results/callbacks or use an operator code. A browser redirect is not credit evidence. |
| Desktop import/connection fails | Check root, instance key, protocol version and catalog task/preferred endpoint fields; review the handoff's known limits. |

For support, include safe error code, `x-request-id`, time and native endpoint.
Do not attach raw keys, merchant secrets or model content by default.

## Mock and protocol conformance

The deterministic mock contacts no upstream and incurs no model/payment costs:

```sh
go run ./cmd/mock-gateway
```

It listens on `127.0.0.1:8788` with synthetic key `nmg_mock_development`.
`NMG_MOCK_API_KEY` and `--listen` override test settings. It is separate from the
persistent application and its console on port 8789.

In a second terminal, from the repository root:

```bash
NMG_BASE_URL='http://127.0.0.1:8788' \
NMG_API_KEY='nmg_mock_development' \
NMG_FIXTURE_FILE='testdata/conformance.json' \
go run ./cmd/conformance
```

```powershell
$env:NMG_BASE_URL = 'http://127.0.0.1:8788'
$env:NMG_API_KEY = 'nmg_mock_development'
$env:NMG_FIXTURE_FILE = 'testdata/conformance.json'
go run ./cmd/conformance
```

For other gateways, supply controlled keys, authorized native models and optional
pre-provisioned scenarios in private fixtures. The client **sends native inference
requests**, potentially costing money on real services. Mock error keys/debug
behavior are not production dependencies. Undeclared optional scenarios are
explicit skips; disable `count_tokens` when undeclared. Deterministic conformance
does not replace real stream/tool/thinking/cache/usage or merchant validation.

## Local validation and licensing

Run the locked console install, build and tests above **before** Go checks, then:

```sh
go test ./...
go vet ./...
go build ./...
node --test scripts/check-frontend-licenses.test.mjs
bash scripts/check-licenses.sh
```

On Windows, replace the final license command with:

```powershell
pwsh -NoProfile -File scripts/check-licenses.ps1
```

Validation is local, with no hosted CI requirement. Run race checks in local
Linux. Select checks covering the change and record unrun checks/reasons;
historical acceptance does not automatically validate new changes.

Pinned Apache-2.0 `go-licenses/v2@v2.0.1` covers imported Go and test dependencies.
The frontend gate audits all source npm lockfiles, including dev/optional/
transitive entries, exact versions, integrity and resolution. Allowed licenses
are Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, 0BSD, ISC, Unlicense and CC0-1.0.
Unknown licenses and copyleft alternatives fail closed. Binary distribution
retains third-party publisher license/NOTICE files, not just the project license.

The gateway implementation is original. Future borrowing must pin/verify a
permissive commit, retain copyright/license/NOTICE obligations and identify
modifications. See [LICENSE](LICENSE), [NOTICE](NOTICE),
[repository guidelines](AGENTS.md) and [license audit](docs/security/license-audit.md).
