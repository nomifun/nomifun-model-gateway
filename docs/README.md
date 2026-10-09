# Documentation guide

English | [简体中文](README.zh-CN.md)

Start with the [project README](../README.md) for installation, configuration,
operator/user workflows and native API examples. The [console README](../console/README.md)
explains each page and the frontend development workflow. Topic documents below
contain detailed contracts, operational procedures and dated acceptance evidence;
some are currently written in Chinese.

NomiFun provides this independent Apache-2.0 gateway project. Community partners
operate their own instances and are responsible for prices, terms, privacy,
payments and support. NomiFun does not operate gateway instances or sell API access.

## Choose your next step

| Goal | Guide |
| --- | --- |
| Install and start a local gateway | [Root quick start](../README.md#install-and-start) |
| See the product before installing | [Product screenshots](../README.md#product-screenshots), [screenshot gallery and provenance](screenshots/README.md) |
| Add a provider and publish models | [Operator walkthrough](../README.md#operator-walkthrough), [provider presets and discovery boundaries](operations/provider-presets.md) |
| Use the console as a customer or operator | [Console usage guide](../console/README.md) |
| Create a key and call a model | [User walkthrough](../README.md#user-walkthrough), [native API examples](../README.md#native-api-examples) |
| Connect NomiFun Desktop or another client | [Desktop connection](../README.md#desktop-connection), [partner integration](partners/integration.md) |
| Develop without upstream or payment costs | [Mock and conformance](../README.md#mock-and-protocol-conformance) |

## Protocol and integration

| Document | Contents |
| --- | --- |
| [Protocol v1](protocol/v1.md) | Frozen public metadata, catalog, account, native endpoints, authentication and error contracts |
| [OpenAPI](../openapi.yaml) | Machine-readable public API contract |
| [Partner integration](partners/integration.md) | Operator/client boundary, runtime Desktop connection and credential-free integration links |
| [Provider onboarding and Desktop handoff](partners/provider-onboarding-desktop-handoff.md) | Local synthetic fixture operation, onboarding evidence and remaining Desktop integration gaps |

Clients integrate with the public protocol, not the console's internal
`/api/console/v1` session API or the mock's synthetic implementation details.
Native requests must use a task/endpoint declared for the selected public model.

## Deployment and operations

| Document | Contents |
| --- | --- |
| [Deployment](operations/deployment.md) | SQLite/PostgreSQL, Docker/Compose, listener/environment setup, HTTPS and single-process boundaries |
| [Provider presets](operations/provider-presets.md) | Upstream product roots, native authentication, model discovery limits and manual mapping |
| [Backup and recovery](operations/backup-and-recovery.md) | Database backups, separate master-key retention and recovery drills |
| [Reconciliation](operations/reconciliation.md) | Interrupted request holds, verified usage and merchant order reconciliation |
| [Load testing](operations/load-testing.md) | Bounded local HTTP acceptance, cost-free health defaults and numeric reports |
| [Acceptance](operations/acceptance.md) | Deterministic local tests versus real upstream/merchant acceptance and production evidence |
| [Operator responsibility checklist](compliance/operator-checklist.md) | Independently operated services, terms, privacy, support and launch responsibilities |

## Security, licensing and evidence

| Document | Contents |
| --- | --- |
| [Security review](security/review.md) | Credential, access, relay, payment and operational controls and their limitations |
| [License audit](security/license-audit.md) | Permissive-only dependency gate, provenance and redistribution notices |
| [Frontend attribution](../licenses/frontend/README.md) | Pinned publisher attribution and notice collection commands |
| [M0 validation](validation/m0.md) | Protocol/mock/conformance milestone evidence |
| [M0–M4 delivery record](validation/delivery.md) | Gateway, Desktop, container and license evidence, with open external gates |
| [UI redesign validation](validation/ui-redesign.md) | First-round console layout and interaction acceptance |
| [UI component validation](validation/ui-components.md) | Later component transplantation, attribution and interaction evidence |
| [Screenshot capture notes](screenshots/README.md) | Current English/Chinese product images, synthetic data scope and refresh procedure |

Run the local validation commands in the [root README](../README.md#local-validation-and-licensing)
and follow [repository guidelines](../AGENTS.md). Evidence documents record their
own date and scope. A successful local mock, signed synthetic payment callback or
product screenshot does not establish real upstream fidelity, merchant settlement,
sustained production capacity or multi-node high availability.
