# Console screenshots

English | [简体中文](README.zh-CN.md)

These screenshots show the real embedded console built from this checkout on
**2026-10-09 (Asia/Shanghai)**. Use the [gateway guide](../../README.md) for setup
and the [console guide](../../console/README.md) for page-by-page instructions.

## Gallery

| Screen | English | Simplified Chinese | What it shows |
| --- | --- | --- | --- |
| Account overview | [overview.png](en/overview.png) | [overview.png](zh-CN/overview.png) | Wallet balance, Desktop connection steps, account and settled usage |
| Model catalog | [models.png](en/models.png) | [models.png](zh-CN/models.png) | Public model IDs, task labels, context and output limits |
| API keys | [api-keys.png](en/api-keys.png) | [api-keys.png](zh-CN/api-keys.png) | Key prefixes, quotas and revocation controls |
| Administrator channels | [channels.png](en/channels.png) | [channels.png](zh-CN/channels.png) | Upstream channels, mappings, discovery, probes and editing |
| Provider setup wizard | [channel-onboarding.png](en/channel-onboarding.png) | [channel-onboarding.png](zh-CN/channel-onboarding.png) | Native model discovery and the four-step configuration flow |

## Capture scope

The gateway and `cmd/provider-onboarding-fixture` ran in a separate loopback-only
instance at ports **18921 / 18922**, backed by an isolated SQLite database in
ignored `.dev/readme-screenshots/`. The console fetched real API responses;
no frontend response interception or mock-data fallback was used.

The displayed operator, `example.test` accounts, API keys, three public chat
models, two channels and wallet credit are synthetic demonstration data. The
three public models map to the fixture's `synthetic-chat-v1`. Four requests
actually passed through the local relay and settled in its ledger. No real model
provider or merchant was contacted, and no paid upstream request was sent.
These pictures show the product's interface, not external upstream, payment,
capacity or high-availability acceptance.

Captures use a **1440 × 1040** browser viewport at device scale 1, the light theme,
English or Simplified Chinese as selected through the UI, and reduced motion.
The overview is a full-page capture, so its image is taller than the viewport.
Passwords and upstream credentials remain masked; the key list shows only
prefixes. No one-time secret dialog is captured.

## Refreshing screenshots

1. Build the console and gateway using the [root quick start](../../README.md#install-and-start).
2. Start an isolated synthetic instance using the existing
   [provider-onboarding acceptance script](../../scripts/provider-onboarding-acceptance.ps1)
   and [local fixture instructions](../partners/provider-onboarding-desktop-handoff.md).
   That script uses ports 18891 / 18892 / 18893 and its own ignored data directory;
   inspect its status and port ownership before starting it.
3. Sign in with the synthetic bootstrap account described by the local fixture
   configuration. Use the real console to create channels, model mappings,
   capabilities and explicit prices. Enable registration if a separate user
   account is needed. Use only synthetic keys and local fixture addresses.
4. Give the demonstration account an audited synthetic wallet credit, create
   scoped keys and make a bounded local native request to populate usage. Do not
   substitute production accounts, customer data or merchant transactions.
5. Choose each language, open the five screens above and capture the same
   viewport. Save each PNG under its existing language directory. Close secret
   dialogs before taking pictures and review every image before committing it.
6. Stop only the processes owned by the fixture run, retain its ignored evidence,
   and update the capture date and any changed page descriptions here and in the
   Chinese version. Keep corresponding captions and links in the root and
   console READMEs aligned.

Screenshots are documentation assets, not required runtime files. They may be
refreshed without changing the public protocol or the tracked Go-embedded
console assets.
