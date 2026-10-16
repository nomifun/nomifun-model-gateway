# 2026-10 Desktop 模型管理与 Gateway 对齐记录

本记录以 2026-10-16 的 NomiFun Desktop 模型管理实现为参考，更新 `nomifun-model-gateway` 的协议描述、模型发现、管理员配置与原生流边界。首次基线和后续 `803cb7a1e` 增量分别记录如下。公共契约仍为 [Protocol v1](../protocol/v1.md) 与 [OpenAPI](../../openapi.yaml)。本批只修改 Gateway；没有修改或验收 Desktop importer，也没有新增音视频、音乐或实时模型转发。

NomiFun 官方提供开源软件与开放协议，不运营网关实例、不出售 API，也不运营付费服务。社区伙伴独立负责实例、供应商账号、服务政策、价格、账单和用户支持。

## 审计基线与对齐目标

Desktop 只读审计基线为 `7787646cedf1f94d491226b7f81bffe814da155d`，检查时工作区无未提交改动。Gateway 启动基线为 `ae3cab3d79475d33f0c8b20bc94de45fdf5206b4`，本批修改在该基线之上，尚未提交。重要的近期 Desktop 变更包括：

| Desktop commit | 本批采用的行为约束 |
| --- | --- |
| `e5fb41cd4` | 显式区分 Chat Completions 与 Responses；模型输出字段、provider 参数与协议选择各有作用范围 |
| `0f092d996` | 协议描述集中管理；模型推荐具备精确型号及来源，推荐不替代保存配置 |
| `e0da57ae7` | 按任务发现候选协议；原生目录、文档支持和实际验证是不同证据 |
| `df386e41b` | Chat 健康验证使用实际调用链；连接可达不等于模型能完成工具往返 |
| `621e3e05b` | 兼容流式终止与真实 Agent wire stack 的验证边界；未知字段与原生流不应被改写 |
| `b2b50ec9d` | 自动配置复用同一编辑配置的结果；发现过程不反复覆盖用户选择 |

审计读取了 Desktop 的 `nomifun-api-types/src/{provider,model_task,model_protocol,model_gateway}.rs`、`nomifun-model-invoke/src/{manifest,chat_verification,chat_sse}.rs`、`nomifun-system/src/{model_gateway,model_fetcher/*}.rs`，以及模型编辑与自动配置相关前端逻辑。这里记录的是接口与行为对齐，未复制第三方网关的实现；Desktop commit 名称中的兼容服务名称不构成第三方源码来源或许可证依据。

## 新增公开协议描述

Gateway 和 Mock 的 meta 增加可选字符串：

```json
{"protocol_manifest":"/nomifun/v1/protocols"}
```

`GET /nomifun/v1/protocols` 匿名读取，描述本软件已实现并由当前实例 `capabilities` 启用的协议。响应不带上游地址、账号、凭据、用户可见模型或价格。它不请求上游，也不证明某个账号拥有模型或通过了模型调用验收。账户权限及具体任务路线仍由鉴权后的 catalog 表达。

本扩展保持 `contract_version: "1.0"`，另有 `manifest_version: "1"`。`protocols` 按既有端点族顺序排列并去重；实例未启用的端点族不出现，空集合保持 `[]`。`unsupported_tasks` 说明软件实现边界，不把运营方临时停用的已实现任务误记为软件不支持。旧 v1 客户端可以忽略 meta 附加字段；其他 v1 实现也可以不提供该扩展。

`optional_endpoints` 继续只声明可选 Anthropic 原生路径 `/v1/messages/count_tokens`，不用于发布协议描述地址。扩展路径只能从 `meta.protocol_manifest` 发现。

| `endpoint` | `protocol_id` | 任务 | 连接与根地址 |
| --- | --- | --- | --- |
| `openai` | `openai.chat_text` | `chat` | `default`，Bearer，实例根加 `/v1` |
| `openai-response` | `openai.responses` | `chat` | `default`，Bearer，实例根加 `/v1` |
| `anthropic` | `anthropic.messages` | `chat` | `anthropic`，`header_key:x-api-key`，实例根 |
| `gemini` | `gemini.generate_text` | `chat` | `gemini`，`header_key:x-goog-api-key`，实例根 |
| `image-generation` | `openai.images` | `image_generation`、`image_edit` | `default`，Bearer，实例根加 `/v1` |
| `embeddings` | `openai.embeddings` | `embedding` | `default`，Bearer，实例根加 `/v1` |
| `jina-rerank` | `generic.rerank` | `rerank` | `default`，Bearer，实例根加 `/v1` |

协议项同时公开 `executor`、`transport`、`root_shape`、`base_url_suffix`、`requires_output_ceiling`、`requires_stream_usage` 和操作模板 `endpoints`。四种 Chat 协议使用 `agent`，其他任务使用 `model_invoke`；当前传输均为 HTTP。根形状与模板成对使用：`versioned_root` 把 `/v1` 放在连接上；`origin_root` 把版本段放在操作路径上。Gemini 的生成和流式生成分别列为 `submit` 和 `stream` 操作。具体字段以 [协议类型](../../contract/v1/protocols.go) 与 [协议正文](../protocol/v1.md#411-可选协议描述扩展) 为准。

网关 manifest 与 Desktop 内部 `/api/model-protocols` 响应不是同一对象。后续 Desktop 适配应解析该扩展版本，再将显式协议、连接角色与任务保存到自身模型配置；不能直接反序列化为 Desktop 内部响应，也不能根据供应商品牌或模型名自动选择协议。本批没有实现这项 Desktop 修改。

## 任务、特征与技术能力边界

Desktop 有十项任务；网关 v1 保持已冻结的五项任务：`chat`、`image_generation`、`image_edit`、`embedding`、`rerank`。未实现的五项为：

- `realtime_conversation`：需要独立实时会话与双向传输。
- `video_generation`、`music_generation`：需要各自的生成、产物与计量契约。
- `speech_synthesis`、`speech_recognition`：需要语音原生正文、内容验证与计费维度。

这些任务明确出现在 manifest 的 `unsupported_tasks`；模型发现即使报告其中某项，也只能用于管理员审阅，不能保存成 v1 可执行路线。新增任务需新版本或双方协商的扩展，并完成权限、原生协议与外部验收。不能把实时会话写成 Chat 特征，也不能因 Chat 有音视频输入而声称语音或视频生成已经可用。

`vision_input`、`audio_input`、`video_input`、`web_search` 是建议特征。它们不改变协议分派，也不能禁止已选原生协议能够表达的输入。`function_calling`、`reasoning`、`streaming` 是运行时技术能力，不再被当成管理员勾选后就已经验证的特征；控制台将二者分开表达。实际模型是否接受某项操作，仍须以该任务的原生验证和具体错误证据判断。

## 上游模型发现与手动应用

管理员的临时发现和已保存渠道发现仍只执行有界、只读的原生 GET。它们不发起生成、不改变渠道健康、亲和绑定、模型映射或账务。分页失败仍明确返回部分结果与警告；Azure offering 不被误当作 deployment ID，继续要求手工确认 deployment。

结果新增 `advisory: true`；只有真正读取成功的页面才声明 `catalog_source: "remote"`。Azure 手动回退不声称已读取远程目录。每个发现项保留原有 ID、展示信息及原生 generation methods，另提供与 Desktop 风格一致的建议字段：

| 字段 | 行为 |
| --- | --- |
| `context_limit`、`output_limit` | 已核实原生字段中的精确正安全整数；未知、无效或超界时省略，不序列化为 `null` |
| `token_limit_sources.context_limit`、`.output_limit` | 被采用的原生字段路径；只为存在的值提供来源 |
| `token_limit_sources.context_limit_kind` | `input_only` 或 `combined`；输入上限不能再减去独立输出上限 |
| `tasks`、`tasks_source` | 只从明确、无歧义的原生方法产生建议；当前来源值为 `provider_declared` |
| `supported_generation_methods` | 保留 Gemini 原生方法，包括未知方法，供管理员核对 |

旧 `input_token_limit`、`output_token_limit` 字段继续保留。当前采用的来源与含义为：

| 原生目录 | 上下文来源与语义 | 输出来源 |
| --- | --- | --- |
| Anthropic | `max_input_tokens`，`input_only` | `max_tokens` |
| Gemini | `inputTokenLimit`，`input_only` | `outputTokenLimit` |
| OpenAI/兼容格式的已知 OpenRouter 扩展 | 优先 `context_length`，缺少有效值时使用 `top_provider.context_length`；`combined` | `top_provider.max_completion_tokens` |

标准 OpenAI 模型 ID 本身不提供限额或任务依据。其他无已核实语义的 `tasks`、`type`、`context_window`、协议字段、任意来源字符串与供应商价格不会自动变成本实例的权威模型配置。数值解析允许精确整数 JSON number 和十进制整数字符串；非整数值、布尔值、对象、非正数、超过 `9007199254740991` 的数值与异常指数被丢弃，避免精度损失及使整张目录失败。

Gemini `embedContent` / `batchEmbedContents` 只建议 `embedding`，`bidiGenerateContent` 只建议 `realtime_conversation`；后者仍超出网关 v1 执行范围。`generateContent` 可以生成不同类型内容，不能据此默认建议 Chat、图片或语音任务。模型名包含 `chat`、`image`、`audio` 等词也不产生任务或限额推断。

控制台显示远程建议与来源，并提供明确的“应用到草稿/编辑器”动作。应用只填入空缺任务或限额，保留已有手工编辑、非空限额、任务路线、首选端点和售价。任务建议不会自动带入端点偏好；新增任务路线仍须管理员选择已实现的原生端点。保存和启用继续经过发布检查；取消或只读取目录不会自动写入配置。供应商成本始终不能当作网关售价，缺价不等于免费。

来源与 `context_limit_kind` 当前属于管理员建议层；公共 catalog v1 没有字段级来源及上下文类型字段，Desktop Gateway importer 也没有新增这些字段的处理。本批不声称已完成这一元数据的端到端持久化。后续若需向 Desktop 保留该语义，应通过可选扩展及双方适配明确约定，不能悄悄改变冻结字段含义。

## 数值与原生流兼容

公共 v1 的模型 token 上限仍为正 `int64` 或 `null`。本生产 Gateway 为兼容当前 Desktop renderer，另对新增和编辑采用 `1..9007199254740991` 上限，并由 manifest 的 `max_token_limit` 明示。控制台和后端保持一致；历史正 int64 配置仍可完整读取，管理员可通过编辑修正，不能自动截断或因新增策略使旧目录整体读取失败。价格、余额、配额和账务仍保留完整 `int64` 规则，不能因 renderer 使用安全整数而缩小金额范围。

原生 JSON、未知字段、thinking 签名、缓存、reasoning 与 SSE 字节继续直通。新增 EOF 边界与当前 Desktop 的有限兼容行为对齐：支持 CRLF、LF、CR 及混合换行，跨块 CRLF 与 UTF-8 均保留；正常 SSE 事件必须以空行定界，仅 OpenAI Chat 的完整 `[DONE]` 哨兵在事件名为空、`message` 或 `done` 时可在 EOF 缺少末尾空行。其他协议仍须原生终态，矛盾的 `event: error` 不能确认成功。其他未定界帧即使包含看似完整的终止 JSON，也不能确认终止 usage；原始字节仍转发，已接受请求进入 `reconciliation`，保留预扣和有效的已有用量证据。已收到终态后的截断或客户端 flush 失败也不自动结算。`[DONE]` 本身不产生 token 数，缺少完整有效 usage 仍不能成功结算。Anthropic 的 `error: null` 联合字段视为缺失，不会误报渠道失败。

本实例的四种 Chat 协议均要求显式正原生输出上限。OpenAI Chat 流还要求取得最终 usage，manifest 因而声明 `requires_stream_usage: true`。省略 `stream_options` 或其中的 `include_usage` 时，网关补充 `include_usage: true`；显式 `stream_options: null`、非对象值或显式非真 `include_usage` 均拒绝为 HTTP 400。Desktop 给其他兼容服务提供的省略流式用量选项不适用于本实例，本批没有放宽这项收费边界。

## 本地验证记录

2026-10-16 本地验证已完成，结果如下。已先完成前端安装、构建和测试，再执行 Go 检查；嵌入资产与第三方 notices 随最终构建保留。没有增加依赖或托管 CI。

| 检查 | 本批状态 |
| --- | --- |
| `console/`: `npm ci --ignore-scripts`、`npm run build`、`npm test` | 通过；Node 24.18.0，57 项前端测试通过；后续 manifest 更新刷新回归也通过 |
| Go 1.27.1: `go test ./...`、`go vet ./...`、`go build ./...` | 最终代码全部通过 |
| `scripts/check-licenses.ps1` 与前端许可证回归 | 通过；262 项 lockfile 依赖受审计，5 项许可证回归通过 |
| Mock 与 reusable conformance client | 本地独立 Mock HTTP 进程：227 PASS、0 FAIL、0 SKIP，包含新增 manifest 检查 |
| 本地生产控制台与公开 meta/manifest | 实际浏览器及 HTTP 检查通过，详见下文 |
| `git diff --check` | 通过 |

实际浏览器通过隔离的 `127.0.0.1:18891` 控制台登录合成管理员，验证协议清单加载、Chat 参考特征、超限 `9007199254740992` 被字段错误阻止，修正后保存停用模型 `desktop-alignment-pending`，重新打开准确读回 `128000` / `4096`、首选 `openai` 与视觉输入说明。合成上游目录读取后，名称含 `chat` 的模型仍将未声明的任务和限额显示为“未声明”；取消向导后渠道数保持 0，未保存任何映射。公开 meta 通过新路径指向 7 项协议及 5 项未支持任务的 manifest。所有操作使用合成数据和 loopback 服务。

截图保留在本工作区忽略目录 `.dev/provider-onboarding/desktop-model-management.png`、`desktop-discovery-review.png`；合成数据库与配置也留在该目录，没有进入源码。验收进程已按记录中的 PID、路径及启动时间清理。新增进程级账务联调命令被自动审批审查拒绝，返回原因为 `blocked by policy`；该额外检查未完成、未计为通过，也未操作外部付费实例。原生转发及账务证据来自本批已通过的临时数据库和合成 HTTP 上游回归。

新增本地覆盖的实现位置包括 [协议 registry 与 manifest 测试](../../contract/v1/protocols_test.go)、[生产协议描述路由测试](../../internal/server/model_protocols_test.go)、[发现元数据测试](../../internal/relay/discovery_metadata_test.go)、[原生流边界测试](../../internal/relay/desktop_stream_test.go)、[逐字节 SSE 换行回归](../../internal/relay/sse_test.go)、[15 项异常流账务回归](../../internal/relay/desktop_stream_billing_test.go) 与 [历史 int64 配置兼容回归](../../internal/store/catalog_desktop_test.go)。这些测试使用合成模型、临时数据库与合成凭据；测试通过也只证明本地确定性行为。许可工具的非 Go 源码警告仍限于已有 x/sys、x/crypto 汇编和 ugorji codec，来源记录见 [许可证审计](../security/license-audit.md)。

## Desktop `803cb7a1e` 后续增量

再次只读对比 Desktop `7787646ce..803cb7a1e50685d03bf2d8bdaf63b9fba1945064`，工作区干净。本次模型相关变更集中在保存的输出上限探测、Anthropic 工具 schema 适配和流内错误重试分类。Gateway importer、协议 ID、连接角色和端点没有变化，Responses 的加密 reasoning、`store:false` 和函数工具格式也已存在于上次基线；不需要变更公开 v1 或 manifest 版本。

| Desktop 新行为 | Gateway 对齐及直接证据 |
| --- | --- |
| 健康与协议发现发送完整已保存 `output_limit`；未配置才采用 4096 | 原生字段已经完整透传，不增加裁剪；新增合成 `131072` 请求与上游 `128000` 拒绝的回归，确认错误原文保留、无重试且预留释放。本地配置更小上限则在上游调用和账务预留前拒绝。示例数值不是对任何真实型号的上限声明 |
| Anthropic/Bedrock/Vertex 客户端处理工具 schema 根级组合约束，运行时仍按完整 canonical schema 验参 | 网关不重复执行适配；回归同时覆盖已由调用方适配的 Anthropic schema、未适配的 Responses 根级 `oneOf`、嵌套约束与 `$defs`、cache、thinking 和加密 reasoning 的原始字节保真 |
| `upstream_error` 在客户端无语义输出阶段作为暂时错误重试，具体 code 优先 | 网关保留 `type` / `code`，错误流无最终 usage 时对账且不扣款；已发送流不向备用账号重试，避免拼接响应和重复上游生成 |
| 原生协议继续由精确模型任务选择 | 修复 Responses 顶层 `type: error` 未标记失败以及后续终态会清除既有失败状态的渠道健康问题；修复 Anthropic 多个 beta 请求头值只转发第一个的问题，保留显式版本与全部 beta 值 |

调用失败不等于未产生计费 token。此次保留既有的 Responses 终态计量策略：`response.failed` / `response.incomplete` 带完整有效 usage 且传输干净结束时结算；缺少最终 usage 则保留预扣对账。失败标记影响渠道健康，不把错误正文写入持久化 usage，也不改变原生事件。此边界由 [原生错误与计费回归](../../internal/relay/desktop_error_upgrade_test.go) 覆盖；请求上限与工具/schema/请求头保真由 [Desktop 协议升级回归](../../internal/relay/desktop_protocol_upgrade_test.go) 覆盖。OpenAI Responses 的顶层错误格式已对照 [官方事件契约](https://developers.openai.com/api/reference/resources/responses/streaming-events) 核实。

本增量验证完成：先执行 `console/` 的 `npm ci --ignore-scripts`、`npm run build` 和 `npm test`，57 项全部通过，嵌入资产与 notices 保留；新增 relay 定向回归及 Go 1.27.1 的 `go test ./...`、`go vet ./...`、`go build ./...` 全部通过。`scripts/check-licenses.ps1` 和 5 项前端许可证回归通过，`git diff --check` 通过；许可工具仍仅报告前述已有汇编源码警告。独立 Mock/conformance HTTP 进程没有重跑，公开契约和 Mock 行为未修改，已有符合性与 schema 集成测试包含在本次 Go 全量测试中。未修改、构建或测试 Desktop；没有增加依赖，没有执行真实上游生成、提交或发布。

## 外部验收与后续协作

以下事项仍需独立证据，本批没有将它们计为已完成：

- Desktop 运行时读取本扩展并将其应用到模型编辑器、完整导入与同步，以及任务级调用验证；本次未修改、构建或测试 Desktop。
- 有授权凭据时，逐供应商、逐型号验证目录、原生工具往返、流式终结、thinking 签名、缓存和状态续接；不能以 manifest 或模型 ID 推断通过。
- 按真实供应商账单核对完整 usage、异常断流预扣与 reconciliation，不以合成数字代替实付证据。
- 生产 PostgreSQL 并发与恢复、多节点亲和/失效状态、容量、高可用及官方商户支付验收。
- 新媒体与实时任务的版本协商、协议实现、产物验证及计费；本批没有开放这五项任务。

需要恢复此前合成 Gateway/Mock 联调时，参考 [既有交接记录](provider-onboarding-desktop-handoff.md)，重新核实端口、进程与本地数据目录，避免把旧批次的验收结果当作本批结果。未经用户明确请求，不提交、推送、发布或操作外部付费实例。
