# 供应商录入批次：Desktop 联调交接

本批只修改 `nomifun-model-gateway`。Desktop 使用公开 v1 契约接入，不依赖 Gateway 内部源码、管理接口、构建产物或供应商品牌预设。官方 NomiFun 不运营网关实例、不出售 API；社区伙伴独立运营其服务。

## 只读检查范围

- Gateway：`C:/Users/MINISFORUM/code/nomifun/multi/nomifun-model-gateway`，启动检查为 `main` / `84bf30be25712f13f2ada8751a8e29e18aef7b71`。
- Desktop：`C:/Users/MINISFORUM/code/nomifun/main/nomifun-desktop`，检查时为 `main` / `eb964cc98c2df15c2d83a5135c27ad0143461ccb`。
- Desktop 的 `crates/backend/nomifun-system/src/model_gateway.rs` 检查时已有未提交改动，且该仓库有其他并行工作；下述位置对应本次读取的工作区版本，交接后应由 Desktop 任务再次核对。
- 未修改、构建或测试 Desktop；未读取真实凭据，未通过消息工具联系外部人员或其他 Desktop 聊天。

## 公共接口与既有接入路径

`crates/backend/nomifun-system/src/model_gateway.rs` 通过 `/nomifun/v1/meta`、`/nomifun/v1/catalog`、`/nomifun/v1/account` 读取公开控制面。`normalize_gateway_root` 接受实例根地址或精确 `/v1` 后缀，拒绝额外路径、userinfo、query、fragment；控制请求有超时和 2 MiB 响应上限，并禁止重定向。

每个任务采用目录声明的 `preferred_endpoint`。OpenAI 使用默认 Bearer 连接；Anthropic 使用独立 `x-api-key` 连接；Gemini 使用独立 `x-goog-api-key` 连接。Gateway 供应商预设只负责管理员录入上游渠道，不需要 Desktop 添加对应品牌，也不改变公开模型 ID。

`crates/backend/nomifun-api-types/src/model_gateway.rs` 以 i64 解析公开 JSON 整数；金额、价格、配额和速率在 Desktop 内部 API 中序列化为十进制字符串，避免 renderer 丢失精度。目录上下文和输出限额则要求 renderer 安全整数。同步使用保存的目录基线做字段级合并，保留用户覆盖、启用状态和排序；不能把 Gateway 目录读取成功当作模型调用成功。

## 需由 Desktop 任务处理或确认的情况

### 目录未给输出限额时，普通调用可能没有原生输出上限

静态调用链已确认，尚未在 Desktop 运行时复现：

1. 用合成目录提供 OpenAI、Responses 或 Gemini chat 模型，保留合法的 `max_output_tokens: null`，其余必需字段有效。
2. Desktop 导入该模型；`crates/backend/nomifun-system/src/model_gateway.rs:201` 将 null 原样变为 `output_limit: None`。仅 Anthropic 在该文件 `:192` 要求目录输出限额。
3. 发起没有调用级输出上限的聊天。`crates/backend/nomifun-chat-model-broker/src/broker.rs:473` 保留 None；`adapter.rs:504`、`:558` 和 `:628` 分别只在 Some 时写 `max_tokens`、`max_output_tokens` 和 `generationConfig.maxOutputTokens`。
4. Gateway 的 `internal/relay/service.go:122` 对所有计量 chat 生成要求显式正输出上限；预期返回原生协议的 HTTP 400，而不会提交付费上游生成。

另一条 Agent 调用链也有同样边界：`crates/backend/nomifun-ai-agent/src/factory/provider_config.rs:649` 合并调用/配置限额；`crates/agent/nomi-config/src/config.rs:402` 对 OpenAI、Responses、Gemini 的 `requires_output_ceiling` 返回 false。

本批 Gateway 的发布检查应要求完整 chat 限额，以避免新发布模型落入此状态。但 v1 nullable 字段和其他运营实例仍存在，Desktop 任务需要决定在导入、编辑或发送前如何提示并要求有界调用；不要推测上游默认值。

### unavailable 目录项仍会被默认选中并作为本地启用模型导入

静态行为，尚未运行 Desktop UI：`ui/src/renderer/pages/settings/components/ModelGatewaySetup.tsx:85` 默认选择目录中的全部 ID；`:122` 的逐项 Checkbox 只由创建中状态禁用，`:127` 展示远端状态标签；`crates/backend/nomifun-system/src/model_gateway.rs:211` 对所有合法状态构造 `enabled: true`。

复现输入：合成目录包含 `status: "unavailable"` 的模型，读取目录后直接点击导入。预期该行已选中，保存后的本地模型为 enabled。该行为不会修改远端发布状态；是否应默认取消选择或禁用 unavailable 行，由 Desktop 任务确定，避免将静态风险表述成已经完成的运行验收。

## 原生透传与账务回归

新增直接行为测试：

- `internal/relay/native_catalog_regression_test.go` / `TestNativeHTTPRejectionsPreserveEveryProtocolEnvelope`：OpenAI Chat、Responses、Anthropic、Gemini 的上游拒绝码和原生错误/未知字段保持原始字节。
- 同文件 / `TestFragmentedUnknownNativeSSEPreservesBytesAndStoresOnlyUsage`：未知事件、keepalive 注释、多行 data、CRLF、被分割的 UTF-8、thinking/encrypted content/signatures/cache 原样透传；终止用量可结算，账务证据只保留用量，排除模型内容与签名。
- `internal/billing/catalog_snapshot_test.go` / `TestCatalogRepricingDoesNotChangeAcceptedReservationSnapshot`：管理员修改持久化目录价格之后，已接受请求经 service 重建仍按原快照结算；新请求采用新价格，重放不同价格冲突，相同结算重放只生成一次账目。

已有直接回归覆盖：`TestMultipartFilesAndAzureNativePaths`、`TestMultipartImageCountTenReservesAndSettlesTen`、`TestNativeUsageSemanticsAndUnknownEvents`、`TestBillingFinalUsageVersusInterruptedHold`、`TestPartialUsagePersistsOnlyMeterEvidence`、`TestMalformedTerminalUsageHoldsActualHTTPAccounting`、`TestReplayIdsExactlyOnceAndConflict`、`TestPartialUsageIsEvidenceAndDoesNotBillOrReplaceConfirmedUsage`、`TestResponsesPreserveNativeBindBeforeFlushAndCrossKey`、`TestBoundAccountNeverFailsOverDuringCooldownOrDeletion`、`TestSessionExpiryTombstoneSurvivesServiceReopen`。

所有以上测试使用临时 SQLite 和合成上游/凭据。它们验证本地确定性行为，不证明真实供应商所有型号的协议保真、外部账单、生产容量或高可用性。真实上游验收仍需运营者在有授权凭据时独立执行。

## 本地联调实例与结果

隔离 Gateway 为 `http://127.0.0.1:18891`，合成上游为 `http://127.0.0.1:18892/v1`，Mock 为 `http://127.0.0.1:18893`。模型映射为公开 ID `provider-onboarding-chat` → 上游 ID `synthetic-chat-v1`。

主配置是忽略目录下的 `C:/Users/MINISFORUM/code/nomifun/multi/nomifun-model-gateway/.dev/provider-onboarding/synthetic.config.json`。向导发布后生成的 `desktop-client.synthetic.json` 包含 `gateway_url`、`model_id`、合成 `api_key`，用于 Desktop 公共契约接入；不在本文复述 key。生成、启动及测试都使用独立数据目录，不读取业务库。

在 Gateway 根目录执行：

```powershell
pwsh -NoProfile -File scripts/provider-onboarding-acceptance.ps1 -Action start
pwsh -NoProfile -File scripts/provider-onboarding-acceptance.ps1 -Action seed-client
pwsh -NoProfile -File scripts/provider-onboarding-acceptance.ps1 -Action verify
pwsh -NoProfile -File scripts/provider-onboarding-acceptance.ps1 -Action conformance
pwsh -NoProfile -File scripts/provider-onboarding-acceptance.ps1 -Action stop
```

启动前先完成 `console/` 的 `npm ci --ignore-scripts`、`npm run build`、`npm test`，并确认三个端口空闲；`start` 构建和启动本任务的程序。`seed-client` 应在完整向导发布后运行，`verify` 验证原生响应/SSE/cache/reasoning 和 ledger，`conformance` 面向 Mock。`stop` 只停止记录中的 PID、可执行路径和启动时间同时匹配的进程，保留测试证据。

2026-10-08 本地验收已完成。实际浏览器控制台从 StepFun 模板开始，将地址明确替换为合成上游，读取目录、选择模型、编辑公开 ID 映射、用可视表单填写任务/原生首选端点/限额/售价并完整提交。启用 `provider-onboarding-chat`，另一个 `provider-onboarding-pending` 保持停用，价格为空、限额为 null；发布检查逐项显示其未配置状态。试图启用未定价模型被字段错误阻止。已保存渠道发现和运营优先级编辑成功，账号身份字段不可编辑。

实际 UI 另验证了售价 `9007199254740993` 的填写、保存、重新打开和精确回读，随后移除测试价格恢复待配置状态；没有将缺价写为零。取消保存渠道发现不会导入映射。桌面与 390px 窄屏模型编辑表单均已检查。截图位于忽略目录下的 `ui-publication-check.png`、`ui-models-saved.png`、`ui-int64-price.png` 和 `ui-model-edit-mobile.png`。

生产 Gateway 进程的本地 HTTP 验收通过：保存渠道目录读取、Anthropic 两页去重、后页失败的部分结果、首页安全 502，以及映射/模型/售价无隐式改写，证据为 `discovery-evidence.json`。合成上游普通 JSON 与 SSE 两个请求各计量 17 token、各扣 2 USD 最小单位，保留接受时价格快照并各产生一次 usage 账目；未知请求/响应字段、cache、reasoning 和 SSE 保留。部分及缺失用量的两次请求进入 reconciliation；原生 429 错误保留并释放其预扣，证据为 `native-billing-evidence.json`。这些是隔离进程的实际本地 HTTP 验收，仍不是真实供应商调用或外部账单证据。

当前构建的 Mock/conformance：226 PASS、0 FAIL、0 SKIP，输出保存在 `conformance-evidence.log`。本批全部检查通过：Go 1.27.1；Node 24.18.0；`npm ci --ignore-scripts`、`npm run build`、`npm test`（49 项）；`go test ./...`、`go vet ./...`、`go build ./...`；`scripts/check-licenses.ps1`；前端许可证回归 5 项；`git diff --check`。未增加依赖，嵌入资产已重建，第三方 notices 保留。许可工具的现有汇编警告为 x/sys、x/crypto 的 BSD-3-Clause 和 ugorji codec 的 MIT，已有来源记录见 `docs/security/license-audit.md`。

管理员完整录入使用最小组合事务，新增/重复公开 ID 拒绝覆盖；模型 POST 创建与 PATCH 编辑明确区分。发布与渠道移除共用目录事务边界；SQLite 并发发布和停用/改映射/删除最后路线的测试通过，重复运行 10 次。PostgreSQL 路径使用事务 advisory lock 防止跨资源写偏，但本批未运行隔离 PostgreSQL 实例，该驱动和锁的原生并发验收仍是明确缺口；不据此声称 HA。

验收结束仅停止本任务启动的 Gateway、合成上游、Mock 及其 worker，保留隔离数据库、合成客户端配置和证据。后续 Desktop 任务可按上面的 start 命令恢复实例，主配置保留原 master key，启动不会重置已保存测试数据。没有真实凭据，真实供应商目录/各型号生成与外部计费核对、官方支付商户验收均未执行；实现与验收阶段未发布、提交或推送，后续 Git 操作按用户显式授权执行。
