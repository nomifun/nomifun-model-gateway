# NomiFun Model Gateway

[English](README.md) · [简体中文](README.zh-CN.md)

面向社区运营方的 Apache-2.0 开源模型网关：提供原生模型转发、用户与 API 密钥管理、
钱包与订阅计费、官方商户支付适配器，以及内嵌 Web 控制台。Go 应用通过 SQLite 或
PostgreSQL 持久保存配置与账务数据，运营方可部署自己的独立服务。

**NomiFun 官方不运营网关实例、不出售 API，也不经营商业服务。** 社区伙伴独立运营，
并对价格、服务条款、隐私、支付与用户支持负责。本仓库为 `nomifun-model-gateway`，
独立于 NomiFun Desktop 及其既有平台网关 MCP crate。Desktop 模型网关 provider
是可选接入方式；伙伴连接自己的实例不需要重新编译 Desktop。

## 从这里开始

| 你的目标 | 阅读入口 |
| --- | --- |
| 了解控制台功能 | [产品截图](#产品截图) |
| 启动第一个本地实例 | [安装与启动](#安装与启动) |
| 添加上游模型并发布 | [运营方操作流程](#运营方操作流程) |
| 创建密钥并连接客户端 | [用户使用流程](#用户使用流程)、[原生 API 示例](#原生-api-示例) |
| 连接 NomiFun Desktop | [Desktop 接入](#desktop-接入) |
| 部署或维护服务 | [Docker 与运维](#docker-与运维) |
| 不产生付费请求地开发协议客户端 | [Mock 与协议一致性检查](#mock-与协议一致性检查) |
| 查阅完整文档 | [中文文档导航](docs/README.zh-CN.md) · [English documentation index](docs/README.md) |

## 功能与当前边界

| 范围 | 当前提供的能力 |
| --- | --- |
| 原生转发 | 按实例与模型声明支持 OpenAI Chat Completions、Responses、Anthropic Messages、Gemini、图片、embeddings 与 Jina rerank；支持 OpenAI 兼容和 Azure OpenAI 渠道。请求采用相同原生协议。 |
| 控制台 | 中英文界面、用户与管理工作区、模型目录、API 密钥、用量、钱包、订阅、渠道、模型与价格表单、支付订单、审计日志和预扣对账。 |
| 渠道录入 | 供应商模板、有界目录发现或手填、公开模型 → 上游模型映射、任务/端点/售价配置和发布检查。 |
| 持久化与凭据 | SQLite 或 PostgreSQL、前向迁移；用户密钥只保存哈希、明文仅创建时展示；上游与商户凭据使用运营方主密钥加密。 |
| 账务 | 整数最小货币单位、请求预扣、用量结算、钱包入账、密钥权限与限额、不可变账本；已接受但结果未知的请求保留到审计对账。 |
| 支付 | 官方 Stripe、支付宝、微信支付 v3 商户适配器；运营方配置并独立验证商户资料之前保持禁用。 |
| 客户端接入 | 冻结协议 v1、OpenAPI、可复用一致性客户端，以及运行时填写地址和密钥的可选 NomiFun Desktop provider。 |

当前版本不包含跨协议转换、Bedrock/Vertex 认证、音频/视频/Realtime、MySQL 或多节点
高可用。同一个数据库只允许 **一个运行中的网关进程**；限流计数仍在进程内。
应用当前未声明可选 Anthropic `/v1/messages/count_tokens`，调用返回 501；
协议 v1 不要求该可选端点。

转发保持原生流、错误、thinking 签名、加密 reasoning 与缓存字段，同时旁路解析 usage。
Responses 续接与 Anthropic 会话保持上游账号亲和，默认不记录请求或响应内容。
本地测试与截图不能证明所有真实上游账号的原生保真、真实商户验收、生产持续容量或
高可用。已执行证据及未完成外部验收见 [交付记录](docs/validation/delivery.md) 和
[验收边界](docs/operations/acceptance.md)。

## 产品截图

以下为真实本地控制台截图，使用合成用户、渠道、模型与账务数据，不包含真实上游
或商户凭据。示例余额和模型不代表 NomiFun 官方服务，也不代表真实上游或支付验收。
[截图来源与刷新方法](docs/screenshots/README.zh-CN.md)。

**用户概览**：查看余额、配额、最近调用和客户端连接入口。

![中文用户概览，展示合成账户数据](docs/screenshots/zh-CN/overview.png)

<details>
<summary>模型目录与价格</summary>

配置客户端之前查看已发布模型、任务、限额与售价。

![中文模型目录与价格](docs/screenshots/zh-CN/models.png)

</details>

<details>
<summary>API 密钥管理</summary>

创建限定范围的密钥并查看状态，明文仅在创建时展示一次。

![中文 API 密钥管理](docs/screenshots/zh-CN/api-keys.png)

</details>

<details>
<summary>管理员上游渠道</summary>

管理原生协议渠道与路由映射。

![中文管理员上游渠道](docs/screenshots/zh-CN/channels.png)

</details>

<details>
<summary>供应商录入向导</summary>

按连接信息、发现与映射、模型能力、发布复核的步骤完成录入。

![中文供应商录入向导](docs/screenshots/zh-CN/channel-onboarding.png)

</details>

## 安装与启动

### 环境要求与构建

使用 [go.mod](go.mod) 固定版本，当前为 **Go 1.27.1**，以及带 npm 的
**Node.js 24 或更新版本**。固定的 Docker 构建使用 Node.js 24.18.0。
容器部署还需要 Docker Engine 和 Docker Compose 插件。首次本地使用 SQLite，
无需单独安装数据库服务。

在检出的仓库中执行；Bash 与 PowerShell 均可使用：

```sh
cd console
npm ci --ignore-scripts
npm run build
npm test
cd ..
go build ./cmd/model-gateway
```

前端构建复制产物到 `internal/console/assets`，由 Go 嵌入应用。这些资源被跟踪，
也是仅使用 Go 构建时必需的文件；`console/dist` 可以重新生成。

### 首次启动：Bash

**仅在创建新数据库时生成一次主密钥。** 输入管理员邮箱及 12–72 个 UTF-8 字节的
密码。以下命令不会输出密钥或密码，也不会将它们放入命令参数：

```bash
export NMG_MASTER_KEY="$(node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("base64"))')"
read -r -p 'Administrator email: ' NMG_ADMIN_EMAIL
export NMG_ADMIN_EMAIL
read -r -s -p 'Administrator password (12–72 bytes): ' NMG_ADMIN_PASSWORD
printf '\n'
export NMG_ADMIN_PASSWORD
go run ./cmd/model-gateway
```

### 首次启动：PowerShell 7

```powershell
$env:NMG_MASTER_KEY = [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:NMG_ADMIN_EMAIL = Read-Host 'Administrator email'
$nmgBootstrapPassword = Read-Host 'Administrator password (12–72 bytes)' -AsSecureString
$env:NMG_ADMIN_PASSWORD = [Net.NetworkCredential]::new('', $nmgBootstrapPassword).Password
Remove-Variable nmgBootstrapPassword
go run ./cmd/model-gateway
```

配置渠道前，把完整主密钥备份到运营方自己的安全凭据存储。之后每次打开同一数据库
必须复用原主密钥，重新生成会使已保存的加密凭据无法读取。当前没有自动主密钥轮换
工具。以上示例仅设置当前进程环境，需要使用受保护的环境配置或凭据管理器保存重启
所需的值。Go 应用 **不会自动加载 `.env`**。

打开 [http://127.0.0.1:8789/console/](http://127.0.0.1:8789/console/)，使用引导凭据
登录。新实例默认关闭注册，尚未配置上游服务。引导变量不会重置已有账户密码，也
不会把已有账户提升为管理员。确认首次引导成功后，后续运行环境可移除两项管理员
变量；前台运行按 Ctrl+C 停止。

| 本地地址或路径 | 用途 |
| --- | --- |
| `http://127.0.0.1:8789` | API 根地址，客户端接入填写此根地址 |
| `/console/` | 内嵌用户工作台与管理员控制台 |
| `/healthz` | HTTP 进程存活探针 |
| `/readyz` | 包括数据库可用性的就绪探针 |
| `/nomifun/v1/meta` | 匿名协议与实例元数据 |
| `data/gateway.db` | 默认持久 SQLite 数据库，相对于运行工作目录 |

服务启动不表示模型推理已可用。继续完成运营方流程，为测试账户准备余额，创建
API 密钥，再验证实例声明支持的模型。

## 配置说明

下表为应用进程环境变量，业务配置通过登录后的管理控制台编辑。

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `NMG_MASTER_KEY` | 必填 | 32 个随机字节的标准 base64；加密上游和商户凭据，必须另行备份。 |
| `NMG_ADMIN_EMAIL`、`NMG_ADMIN_PASSWORD` | 空 | 成对设置的首次管理员引导；密码 12–72 字节，不覆盖已有账户。 |
| `NMG_DB_DRIVER` | `sqlite` | 使用 `sqlite` 或 `postgres`，也接受 `postgresql`。 |
| `NMG_DB_DSN` | `data/gateway.db` | SQLite 本地文件路径或 PostgreSQL DSN；生产 SQLite 拒绝内存数据库及 URI 参数。 |
| `NMG_LISTEN` | `127.0.0.1:8789` | HTTP 监听地址，容器内部使用 `0.0.0.0:8789`。 |
| `NMG_CONNECT_TIMEOUT` | `10s` | 上游连接与 TLS 握手超时。 |
| `NMG_UPSTREAM_HEADER_TIMEOUT` | `60s` | 上游响应头超时。 |
| `NMG_STREAM_IDLE_TIMEOUT` | `90s` | 距离最后收到上游字节的空闲超时；流式请求不设总时长超时。 |
| `NMG_RESPONSE_AFFINITY_TTL` | `720h` | Responses 上游账号绑定期限，最大 `8760h`。 |
| `NMG_REQUEST_BODY_LIMIT` | `67108864` | 入站请求体大小上限，单位为字节。 |
| `NMG_ANTHROPIC_VERSION` | `2023-06-01` | 客户端省略 `anthropic-version` 时的原生版本默认值。 |

超时采用正数 Go duration，除亲和 TTL 外最大 `24h`。Compose 另行使用
[.env.example](.env.example) 中的 `NMG_PORT`（宿主机端口，默认 8789）和
`NMG_POSTGRES_*` 数据库引导变量。`NMG_BASE_URL`、`NMG_API_KEY`、`NMG_FIXTURE_FILE`
用于一致性客户端，不会为应用创建用户或渠道。

## 运营方操作流程

1. **明确运营方信息。** 登录并切换到管理工作区，在「品牌与设置」填写独立运营方
   名称、币种、HTTPS 主页、控制台、购买页、服务条款和隐私链接。决定是否开放
   注册，以及实例声明哪些原生端点族。
2. **创建上游渠道。** 打开「上游渠道 → 新建」，选择供应商模板或自定义类型
   `openai`、`anthropic`、`gemini`、`compatible`、`azure`，填写上游根地址和上游
   API 凭据，替换资源/业务空间占位符。模板只填写连接字段，不代表账号权限，
   也不会替你配置能力和售价。
3. **发现或手填映射。** 对支持列表的供应商读取目录，或手工填写模型 ID。将稳定
   公开模型 ID 映射到准确上游 ID 或 Azure 部署名称；保留地址所需产品/地域前缀。
   发现只添加选中的缺失映射，不覆盖既有 ID、价格或访问策略；不完整目录明确提示。
4. **确认能力与价格。** 填写任务、可用原生端点、每任务首选端点、输入模态、上下文/
   输出限额、状态与访问策略，明确配置所需各项计量价格。金额为每 `unit_size` 的
   整数最小货币单位，例如 `amount: 2, unit_size: 1000, currency: "USD"` 表示
   每 1,000 个该计量单位收费 2 美分。缺价格不等于免费，明确 `amount: 0` 才是
   免费；发布 chat 模型需要可用的上下文与输出上限。
5. **检查并保存。** 执行发布检查并按字段修复问题，不完整模型保持禁用。保存通过
   事务同时写入渠道与新模型，创建不能覆盖已有公开 ID。检查验证本地配置和路由，
   不发送付费生成请求。
6. **准备用户访问。** 在「用户管理」管理账户并进行带审计钱包入账，按需创建
   「订阅套餐」和「兑换码」。独立验证自己的官方商户凭据与回调后才启用支付；
   查看控制台或测试管理员入账的账户无需支付配置。
7. **以用户身份验证。** 创建限定权限的密钥，读取目录/账户，向已声明模型发送有界
   原生请求。结合上游用量检查「用量记录」「钱包账本」「预扣与对账」；连接探测、
   目录发现与推理验收分别确认。

供应商地址、发现上限和既有路由细节见 [供应商录入指南](docs/operations/provider-presets.md)。
渠道 kind、根地址、API version 与凭据组成不可变上游账号身份，需要换账号时创建
新渠道。不能删除启用模型的最后一条路由，应先添加替代路由或停用模型。保存结果未知时，刷新确认
结果后再决定是否重试创建。

## 用户使用流程

1. 打开运营方 `/console/` 登录；开放注册时可创建账户，否则联系该独立运营方
   获取访问方式。
2. 在「模型目录」查看公开 ID、任务、端点、限额与价格；「概览」展示钱包、套餐/
   配额和近期调用。
3. 在「API 密钥」创建有名称的密钥，按需设置有效期、模型范围、额度、速率/并发
   限制和允许 IP。复制仅展示一次的明文并私下保存；列表无法找回，丢失或泄露时
   创建替代密钥并撤销旧密钥。
4. 在原生客户端或 Desktop 填写根地址与该 **实例 API 密钥**。控制台登录会话不是
   推理密钥，用户不需要运营方的上游或商户凭据。
5. 若提供购买功能，在「购买套餐」或「钱包与订单」购买，也可兑换运营方发放的代码。
   完成 checkout 后检查订单状态：成功页不会增加余额，入账依赖验证的支付事件/
   主动查询结果。
6. 在「用量记录」查看请求 ID、结算收费和中断请求；反馈请求 ID 与安全错误信息，
   便于运营方排查。

## 原生 API 示例

[协议 v1](docs/protocol/v1.md) 与 [OpenAPI](openapi.yaml) 定义公开合同。控制台
`/api/console/v1` 会话接口是内部管理接口，不是客户端接入合同。

| 请求 | 鉴权 | 用途 |
| --- | --- | --- |
| `GET /nomifun/v1/meta` | 匿名 | 协议 `1.0`、实际运营方、声明能力与可选端点 |
| `GET /nomifun/v1/catalog` | 实例 API 密钥 | 当前密钥可见的丰富模型目录 |
| `GET /nomifun/v1/account` | 实例 API 密钥 | 套餐、余额、配额与速率限制快照 |
| `GET /v1/models` | 实例 API 密钥 | 与目录可见集合相同的兼容模型列表 |

同一个密钥接受 `Authorization: Bearer …`、`x-api-key` 或 `x-goog-api-key`。
通常只发送一个鉴权头，冲突凭据会返回 401。Gemini 查询 key 兼容仅限两个原生生成
路径，新集成使用请求头。携带凭据的请求不能自动跟随重定向；响应包含 `x-request-id`
和 `Cache-Control: no-store`。

把以下 Node.js 示例保存为仓库外私有本地目录中的 `client-example.mjs`。它从环境
读取凭据，默认使用原生 Responses，通过 `NMG_NATIVE_PROTOCOL` 选择另一已声明
协议；`NMG_MODEL` 使用当前密钥目录中的公开 ID。

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

真实实例通过隐藏输入或凭据管理器设置 `NMG_API_KEY`，再按路径运行脚本。不要把真实
密钥写进源码、fixture、URL 或命令参数。先试免费的 bundled mock 时，按后文启动，
使用其合成配置：

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

其他 mock 组合是 `chat` / `mock-compatible`、`anthropic` / `mock-claude` 和
`gemini` / `mock-gemini`，新部署实例不会自动包含这些 fixture ID。

流式调用在 Chat/Responses/Anthropic 原生正文中设置 `stream: true`，Gemini 使用
`:streamGenerateContent?alt=sse`。增量消费原生 SSE，不跨协议转换。上面短示例缓冲
响应，适用于非流式请求。其他已声明任务使用 `/v1/images/generations`、
`/v1/images/edits`（原生 multipart）、`/v1/embeddings`、`/v1/rerank`，遵守各自
原生正文和模型 `task_endpoints`。

计量 chat 要求显式正数原生输出上限，且不超过模型最大值，示例已包含字段。钱包/
额度需要覆盖可能高于最终收费的保守预扣上界。可空账户数值表示未知/未披露，不能
显示为零或无限；金额为 int64 最小货币单位，客户端计算必须保留整数精度。

## Desktop 接入

1. 使用包含可选 **NomiFun Model Gateway** provider 的 Desktop 版本；安装这个
   独立网关不会安装或更新 Desktop。
2. 在控制台概览点击「在桌面端添加网关」，或在 Desktop 选择此 provider，填写
   运营方 **API 根地址**。
3. 检查并确认运营方，再填写创建的实例密钥。`nomifun://add-provider` 链接只预填
   provider 类型、URL 与名称，不携带凭据。
4. 读取授权目录，明确选择模型并添加所选模型，设置可用原生输出上限。导入成功
   不代表模型调用成功；Desktop 按每任务声明的首选原生端点调用。
5. 检查账户并发送有界小请求；购买/续费在系统浏览器打开伙伴 HTTPS 页面，不带密钥。

普通 OpenAI 类客户端连接使用根地址加 `/v1`，Anthropic 与 Gemini 使用根地址及各自
请求头；避免给原生路径重复添加 `/v1`。界面位置随 Desktop 版本可能变化，当前证据
和已知交接问题见 [伙伴接入](docs/partners/integration.md) 与
[Desktop 联调交接](docs/partners/provider-onboarding-desktop-handoff.md)。

## Docker 与运维

将 [.env.example](.env.example) 复制为 `.env`：Bash 用 `cp .env.example .env`，
PowerShell 用 `Copy-Item .env.example .env`。私下填写主密钥与首次管理员信息，
`.env` 已被忽略，不要提交或分发。Compose 显式加载它，升级继续使用原主密钥。

选择 **一种** 独立部署文件：

```sh
# SQLite：命名卷持久化，不需要外部数据库。
docker compose --env-file .env -f compose.sqlite.yaml config --quiet
docker compose --env-file .env -f compose.sqlite.yaml up -d --build
```

```sh
# PostgreSQL：还需配置 NMG_POSTGRES_PASSWORD 和 NMG_DB_DSN。
docker compose --env-file .env -f compose.yaml config --quiet
docker compose --env-file .env -f compose.yaml up -d --build
```

PostgreSQL DSN 主机为内部服务 `database:5432`，URI 密码字符按规则百分号编码。
不要合并两种部署文件。网关默认只发布到宿主机 loopback；Docker 固定官方 Node/Go
镜像、收集第三方通知，生成不依赖 CGO 的非 root scratch 运行镜像，包含内嵌控制台
与 CA 证书。公开接入由运营方提供 HTTPS 代理，保留 SSE、取消和空闲超时。一个
数据库只允许一个网关 owner，不能部署水平副本。

在 `/readyz` 检查就绪状态，用 Compose `logs gateway` 查看启动结果。保护日志，
避免输出含秘密的完整解析配置；
`docker compose --env-file .env -f <所选文件> stop` 停止项目而不删除数据卷。

| 运维任务 | 文档 |
| --- | --- |
| TLS/代理、数据库、探针、指标、迁移、数据权限 | [运行与部署](docs/operations/deployment.md) |
| 备份数据库及匹配主密钥、验证隔离恢复 | [备份与恢复](docs/operations/backup-and-recovery.md) |
| 按证据处理未知用量或待确认订单 | [请求与支付对账](docs/operations/reconciliation.md) |
| 有界健康/原生请求负载并核对结算 | [负载验收](docs/operations/load-testing.md) |
| 已执行检查与外部验收缺口 | [本地验收](docs/operations/acceptance.md)、[交付记录](docs/validation/delivery.md) |
| 上线独立运营服务 | [运营责任清单](docs/compliance/operator-checklist.md)、[安全检查](docs/security/review.md) |

## 常见问题与排错

| 现象 | 检查与处理 |
| --- | --- |
| 主密钥导致启动失败 | 使用恰好 32 个随机字节的标准 base64，已有数据库恢复原匹配密钥，不生成替代密钥。 |
| 管理员无法登录 | 确认首次空数据库成对设置邮箱和 12–72 字节密码；变量不重置已有用户，不通过修改账户表绕过认证。 |
| 端口不可用或数据库被占用 | 检查 `NMG_LISTEN` 和数据库实际 owner，只停止自己的实例，保持一库一进程。 |
| 控制台未更新或缺资源 | Go 构建前执行锁定前端构建，运行新二进制并确认 `internal/console/assets` 存在。 |
| 无法注册 | 默认关闭，运营方可在「品牌与设置」启用或安排账户访问。 |
| 发现失败或不完整 | 检查 kind、产品/地域根地址、凭据及占位符，阅读警告并按需手填；发现不等于推理验收。 |
| 目录缺模型或 `model_not_found` | 检查发布、启用路由、套餐及密钥权限，使用公开 ID，不直接使用上游部署名。 |
| 400 输出限额/配置错误 | 显式填写原生正数输出上限且不超过模型最大值；检查任务/首选端点和上下文/输出元数据。 |
| 401 或 403 | 使用未到期实例密钥，检查套餐，发送一个正确鉴权头；控制台会话/上游凭据不是用户密钥。 |
| 402 `insufficient_balance` | 通过运营方增加余额，预留保守预扣上界，不只看预计最终费用。 |
| 429 `rate_limited` | 遵守 `Retry-After` 与密钥请求/token/并发限制，避免立即盲目重试。 |
| 续接/会话被拒绝 | 保持 Responses 原密钥和账号绑定；失效/丢失绑定失败关闭，明确开始新请求/会话。 |
| 中断后仍保留预扣 | 在「预扣与对账」核对真实上游 usage，断开连接不表示上游未收费。 |
| 无支付选项或订单 pending | 支付需要配置，核对官方结果/回调或使用运营方代码；浏览器跳转不证明入账。 |
| Desktop 导入/连接失败 | 检查根地址、实例密钥、版本、目录任务与首选端点字段，并阅读交接的已知边界。 |

反馈时提供安全错误码、`x-request-id`、时间与原生端点，默认不附带原始密钥、商户
秘密或模型内容。

## Mock 与协议一致性检查

确定性 mock 不连接上游，不产生模型或支付费用：

```sh
go run ./cmd/mock-gateway
```

默认监听 `127.0.0.1:8788`，使用合成密钥 `nmg_mock_development`；可用
`NMG_MOCK_API_KEY` 与 `--listen` 更改测试设置。它与 8789 端口的持久化应用/控制台
是不同程序。

在第二个终端中，从仓库根目录执行：

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

其他网关使用受控密钥、授权原生模型与私有 fixture 中预先准备的可选场景。客户端
**会发送原生推理请求**，真实服务可能产生费用。mock 人工错误密钥/调试行为不是
生产依赖；未声明的可选场景明确跳过，count_tokens 未声明时禁用检查。确定性通过
不能替代真实流/工具/thinking/cache/usage 或商户验收。

## 本地验证与许可证

先执行上文锁定的控制台安装、构建和测试，**再** 进行 Go 检查：

```sh
go test ./...
go vet ./...
go build ./...
node --test scripts/check-frontend-licenses.test.mjs
bash scripts/check-licenses.sh
```

Windows 将最后的许可证命令替换为：

```powershell
pwsh -NoProfile -File scripts/check-licenses.ps1
```

验证在本地进行，不要求 hosted CI，race 检查在本地 Linux 环境运行。选择直接覆盖
变更的检查并记录未执行项与原因，历史验收不会自动验证新变更。

固定 Apache-2.0 的 `go-licenses/v2@v2.0.1` 检查 Go 导入与测试依赖。前端审计全部
源码 npm lockfile，包括 dev/optional/传递依赖、精确版本、完整性与解析。许可白名单
为 Apache-2.0、MIT、BSD-2-Clause、BSD-3-Clause、0BSD、ISC、Unlicense、CC0-1.0。
未知许可和 copyleft 备选项拒绝通过。二进制发行保留第三方发布者 license/NOTICE，
不只保留项目自己的许可证。

网关实现为独立编写。未来借用须固定/验证宽松许可提交，保留版权/license/NOTICE
义务并标明修改。见 [LICENSE](LICENSE)、[NOTICE](NOTICE)、[仓库约定](AGENTS.md)
与 [许可证审计](docs/security/license-audit.md)。
