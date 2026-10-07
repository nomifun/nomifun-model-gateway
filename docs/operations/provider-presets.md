# 供应商快捷录入与模型目录边界

官方文档核对日期：2026-10-08（Asia/Shanghai）。这些预设帮助填写现有 Channel 字段，
不代表某个账号、所有型号、原生协议或计费已经通过真实上游验收。NomiFun 官方不运营网关
实例、不出售 API；社区伙伴独立运营自己的服务。

预设实现在 `console/src/provider-presets.ts`。品牌不是新增渠道 kind：仍使用 `openai`、
`anthropic`、`gemini`、`compatible`、`azure` 五种协议与认证类型。选中预设不会填写 API Key、
模型映射、限额或售价，也不会发布模型。预设采用最小对话端点；其他原生端点需要运营者
按照具体模型、产品和官方接口单独确认，不能把全目录的非对话模型自动当作 Chat。

## 地址与认证

下表是 Gateway 的 `base_url`，与一些 SDK 显示的完整 API 根略有不同。当前转发器自行拼接
`/v1/chat/completions`、`/v1/responses`、`/v1/messages` 或 Gemini 的 `/v1beta/models/...`。
产品前缀必须保留，例如百炼的 `/compatible-mode` 和 OpenRouter 的 `/api`。
Azure 由现有 Azure 分支拼接 `/openai/v1/...`，无需把 `/openai/v1` 再填入模板。

| 预设 | kind / 默认原生端点 | Gateway base_url | 上游认证 / 目录方式 |
| --- | --- | --- | --- |
| 自定义 | `compatible` / `openai`，可修改为已有类型 | 自行填写 | 由服务合同决定；默认手填模型 |
| OpenAI | `openai` / `openai`、`openai-response` | `https://api.openai.com` | Bearer；`GET /v1/models` |
| Anthropic | `anthropic` / `anthropic` | `https://api.anthropic.com` | `x-api-key`、`anthropic-version: 2023-06-01`；`GET /v1/models` |
| Gemini Developer API | `gemini` / `gemini` | `https://generativelanguage.googleapis.com` | `x-goog-api-key`；`GET /v1beta/models` |
| Azure OpenAI | `azure` / `openai`、`openai-response` | `https://YOUR-RESOURCE-NAME.openai.azure.com` | 资源 API Key；手填 deployment name |
| DeepSeek | `compatible` / `openai` | `https://api.deepseek.com` | Bearer；模型列表合同为 `/models`，Gateway 使用兼容 v1 列表路径，需上游账号验收 |
| Moonshot / Kimi 中国站 | `compatible` / `openai` | `https://api.moonshot.cn` | 中国站 Bearer Key；`GET /v1/models` |
| Moonshot / Kimi 国际站 | `compatible` / `openai` | `https://api.moonshot.ai` | 国际站 Bearer Key；`GET /v1/models` |
| 百炼 北京 | `compatible` / `openai` | `https://YOUR-WORKSPACE-ID.cn-beijing.maas.aliyuncs.com/compatible-mode` | 同地域、同业务空间 Bearer Key；本批手填 |
| Model Studio 新加坡 | `compatible` / `openai` | `https://YOUR-WORKSPACE-ID.ap-southeast-1.maas.aliyuncs.com/compatible-mode` | 同地域、同业务空间 Bearer Key；本批手填 |
| Model Studio 中国香港 | `compatible` / `openai` | `https://YOUR-WORKSPACE-ID.cn-hongkong.maas.aliyuncs.com/compatible-mode` | 同地域、同业务空间 Bearer Key；本批手填 |
| Model Studio 美国弗吉尼亚 | `compatible` / `openai` | `https://YOUR-WORKSPACE-ID.us-east-1.maas.aliyuncs.com/compatible-mode` | 同地域、同业务空间 Bearer Key；本批手填 |
| 硅基流动 中国站 | `compatible` / `openai` | `https://api.siliconflow.cn` | 中国站 Bearer Key；`GET /v1/models` |
| SiliconFlow 国际站 | `compatible` / `openai` | `https://api.siliconflow.com` | 国际站 Bearer Key；`GET /v1/models` |
| StepFun 按量 API | `compatible` / `openai` | `https://api.stepfun.com` | 标准 API Bearer Key；`GET /v1/models` |
| OpenRouter | `compatible` / `openai` | `https://openrouter.ai/api` | Bearer；`GET /api/v1/models`，默认文本输出目录 |

`YOUR-RESOURCE-NAME` 和 `YOUR-WORKSPACE-ID` 是必须替换的占位符。Azure 从资源控制台复制
自己的 Endpoint；百炼从所选业务空间复制实际 API Host。控制台拒绝使用未替换的占位符
进行发现或保存。Azure 当前 v1 模式保持 `api_version` 为空；填写日期版本会使用现有旧版
部署路径，必须与该资源及端点合同匹配。模型映射的上游 ID 是部署名称，不能用全球目录
里的基础模型名代替部署名称。[Azure v1 文档](https://learn.microsoft.com/en-us/azure/foundry/openai/api-version-lifecycle)、
[部署与模型](https://learn.microsoft.com/en-us/azure/ai-foundry/openai/how-to/working-with-models)

Moonshot 中国站与国际站 Key 不可混用；硅基流动两站也分开配置，不推断它们的账号权限、
模型 ID 或目录完全一致。百炼 Key 与地域、业务空间和计费产品匹配。官方仍保留旧共享
DashScope 接入域名，但推荐生产使用业务空间专属域名；运营者可自行修改为官方文档列出的
共享域名，不能通过发现失败自动切换地域或计费产品。[百炼地址总览](https://help.aliyun.com/zh/model-studio/base-url)

## 目录读取与手填

`discovery: models` 表示供应商公布了列表接口，管理员可尝试读取；它不表示列表 2xx 等同
实际调用成功。`discovery: manual` 是本批的明确手填入口，不能以付费生成请求探测或生成
模型列表。连接探测、目录读取、实际生成、合成测试和真实上游验收需要分别记录。

Anthropic 使用 `has_more`、`last_id` 与请求 `after_id`；Gemini 使用 `nextPageToken` 与
请求 `pageToken`。发现必须保留分页完整性，后页失败或游标循环不能当作完整目录。
Gemini 列表同时含其他操作，只能为支持 `generateContent` 的模型配置当前 Gemini 对话端点。
[Anthropic Models](https://platform.claude.com/docs/en/api/models/list)、
[Gemini Models](https://ai.google.dev/api/models)

百炼的正式目录是同地域产品的 `GET /api/v1/models`，响应为 `output.models[].model`，
使用 `page_no`、`page_size`、`output.total`。它与 `/compatible-mode/v1/models` 不是同一合同。
本批模板保留手填；若后续增加目录适配，应覆盖实际地域/业务空间地址与有界分页，避免
静默改用其他域名。[百炼模型列表](https://help.aliyun.com/zh/model-studio/list-models)

OpenRouter 列表省略 `offset` 和 `limit` 时返回该筛选下的完整列表；它仍受 Gateway 的响应体
与条目上限约束。官方默认 `output_modalities=text`，因此不能把默认结果描述为全部模态目录。
目录的供应商价格不等于运营者售价，模型入库仍需确认任务、限额和整数最小货币单位的售价。
[OpenRouter 模型列表](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties)

手填 ID 始终可用，列表不构成模型 ID 白名单。目录失败不得自动覆盖现有映射、售价、访问
策略或能力配置；未知元数据保持待确认。缺少价格不是免费，待配置模型保持禁用。

## 本批未纳入的入口

| 入口 | 具体原因 |
| --- | --- |
| 智谱普通按量 API | 官方调用路径是 `https://open.bigmodel.cn/api/paas/v4/chat/completions`；现有兼容路径拼接会产生 `/api/paas/v4/v1/chat/completions`。本批没有修改原生转发地址规则，因此不声明可用预设。可用型号按官方目录文档手动核对，也没有声称存在普通 Key 的 `/models` 合同。 |
| AWS Bedrock、Gemini / Claude on Vertex AI | 需要 AWS SigV4 或 Google Cloud IAM、项目与区域定位；现有五种渠道认证类型不足以覆盖。 |
| 百炼 Tokyo / Frankfurt 专属地区及 Anthropic 兼容入口 | 本批先覆盖四个常用标准 Chat 入口；可用自定义模板填入官方地址，其他地区/协议需逐项验收。 |
| Coding Plan、Token Plan、Step Plan 等订阅通道 | 与按量 API 的产品、凭据和授权边界不同，不纳入普通后端网关快捷模板，也不把订阅 Key 发送到按量根。 |
| OpenRouter Responses 默认预设 | 官方是无状态接口，`store: true` 和非空 `previous_response_id` 会返回 400；本批使用 Chat，不能借由“兼容”宣称存储会话、续接、获取或删除 Responses 可用。 |
| 音频、视频、音乐、Realtime、异步 job | 超出本批范围；目录中出现这些型号也不能自动公布该任务。 |

智谱路径依据 [HTTP API 官方说明](https://docs.bigmodel.cn/cn/guide/develop/http/introduction)。
OpenRouter 的限制依据 [Responses 官方概览](https://openrouter.ai/docs/api_reference/responses/overview.md)。

## 官方来源与验证范围

预设由本项目独立编写。Desktop 仅用于只读了解候选供应商、地域与产品区分，没有复制适配器
源码、Logo 或建立构建依赖。官方文档网页、API Reference 和说明中的公开接口行为是下列
核对来源；没有使用禁止项目的实现源码。

| 供应商 | 官方接口说明 |
| --- | --- |
| OpenAI | [鉴权](https://developers.openai.com/api/reference/overview)、[Models](https://developers.openai.com/api/reference/resources/models/methods/list)、[Responses](https://developers.openai.com/api/reference/resources/responses/methods/create) |
| Anthropic | [Messages](https://platform.claude.com/docs/en/api/messages/create)、[Models](https://platform.claude.com/docs/en/api/models/list) |
| Gemini | [API Key](https://ai.google.dev/gemini-api/docs/api-key)、[Models](https://ai.google.dev/api/models)、[generateContent](https://ai.google.dev/api/generate-content) |
| DeepSeek | [API 根与鉴权](https://api-docs.deepseek.com/)、[Chat](https://api-docs.deepseek.com/api/create-chat-completion/)、[当前官方 v1 Chat 示例](https://api-docs.deepseek.com/quick_start/agent_integrations/workbuddy/)、[Models](https://api-docs.deepseek.com/api/list-models/) |
| Moonshot / Kimi | [中国站 Models](https://platform.kimi.com/docs/api/list-models)、[国际站 Models](https://platform.kimi.ai/docs/api/list-models)、[国际站 API 概览](https://platform.kimi.ai/docs/api/overview) |
| 百炼 | [地址与计费产品](https://help.aliyun.com/zh/model-studio/base-url)、[Chat](https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions)、[目录](https://help.aliyun.com/zh/model-studio/list-models) |
| 硅基流动 | [中国站 Chat](https://docs.siliconflow.cn/docs/api/chat-completions-post)、[中国站 Models](https://docs.siliconflow.cn/docs/api/models-get)、[国际站 Chat](https://docs.siliconflow.com/en/api-reference/chat-completions/chat-completions)、[国际站 Models](https://docs.siliconflow.com/en/api-reference/models/get-model-list) |
| StepFun | [按量 API 入口](https://platform.stepfun.com/)、[Models](https://platform.stepfun.com/docs/zh/api-reference/models/list) |
| OpenRouter | [Quickstart](https://openrouter.ai/docs/quickstart)、[Models](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties) |

本批预设本地测试覆盖草稿与模板互不污染、既有 kind/endpoint、凭据与 URL 边界、未声明目录的
手填入口。没有真实上游 Key，没有读取用户凭据、余额或付费实例，也没有发送真实生成请求。
因此账号权限、地域可达性、型号原生字段/SSE、thinking signatures、cache、实际用量与真实
计费仍是外部验收缺口；文档核对和本地 Mock 不补齐这些缺口。

## 控制台操作与管理员接口

在管理台的「上游渠道 → 新建」选择模板，替换资源/工作区占位地址并填写上游凭据。
向导先保留浏览器内存草稿，读取模型目录或手填映射，逐项填写公开模型的任务、原生端点、
首选端点、已确认限额和售价，再执行发布检查并保存。完整保存采用一个内部事务，任何新模型
ID 冲突均回滚，不重绑已存在渠道身份。取消向导不保存草稿或凭据。

发现预览显示新增、同 ID 冲突和已有映射差异；勾选只添加缺失映射，不覆盖现有条目。
重复或不完整的表格行保留到运营方纠正。发现不生成售价、能力开关或免费价格。
任务必须使用 v1 已有端点表达，traits/input_modalities 只是目录描述，不能决定真实协议支持。
金额与计量基数按 int64 精确提交；零价只有明确填写时才生效。未知限额可在停用的有效目录
定义中留空，但原生契约的必需字段仍须配置，例如 Anthropic 首选端点要求输出上限。

保存渠道的「发现模型」使用该渠道的密封凭据；发现和连接探测是独立操作，均不能代替真实
推理验收。渠道编辑允许运营字段和映射调整，kind/base_url/api_version/凭据属于不可变账号身份。
移除启用模型的最后一条声明路线会被拒绝，应先停用该模型。

以下接口属于受管理员会话保护的内部控制台 API，前缀为 `/api/console/v1`；它们不是公开 v1
客户端接入合同，也不要求 Desktop 使用：

| 操作 | 接口与结果 |
| --- | --- |
| 未保存渠道发现 | `POST /admin/channels/discover-preview`，只接收 kind/base_url/api_key/api_version，不入库 |
| 保存渠道发现 | `POST /admin/channels/:id/discover`，返回 models/complete/pages/warnings |
| 完整录入预检查 | `POST /admin/onboarding/check`，接收 channel/models，返回 ready/issues |
| 完整录入保存 | `POST /admin/onboarding`，事务创建渠道/新模型/审计；冲突 409，启用项不就绪 422 |
| 模型发布检查 | `POST /admin/models/check`，读取当前路由/实例币种/访问策略，不发上游请求 |
| 模型创建/编辑 | `POST /admin/models` 仅新建，返回 201 或同 ID 409；`PATCH /admin/models/:id` 编辑已有模型，ID 不可变 |

读取目录最多 20 秒、10 页、1000 个模型，每页 2 MiB、总量 8 MiB，不跟随携带凭据的重定向。
Anthropic/Gemini 按官方游标分页；OpenAI 兼容供应商如果返回没有通用合同的分页标志，明确返回
不完整警告，使用手填或供应商专项验收。后页失败保留已获得目录，首个有效页前失败返回安全 502。
客户端保存结果未知时锁定重复提交，要求关闭并刷新确认；不会盲目重试创建。

本批完整本地操作、原生/账务回归、进程归属和未执行的外部验收见
[Desktop 联调交接](../partners/provider-onboarding-desktop-handoff.md)。
