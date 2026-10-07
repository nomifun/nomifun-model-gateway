# M0–M4 最终本地交付记录

记录日期：2026-10-07（Asia/Shanghai）。本记录汇总本次会话的源码实现、局部检查、真实本地
HTTP/数据库/容器/WebUI 和一条真实供应商文本调用证据。**本地工程交付不等同于所有真实
上游、支付商户、原生桌面或生产运营验收已经通过**；未执行项目单独列出。

NomiFun 官方提供开源软件、开放协议和可选桌面 provider，不运营网关实例、不出售 API，
也不运营任何付费服务。社区伙伴独立运营自己的实例，承担价格、套餐、条款、隐私和运营责任。
本次没有提交、推送、发布远端仓库或部署外部付费实例，验收均在本地执行。

## 1. 交付范围与里程碑边界

独立网关目录：本仓库根目录 `nomifun-model-gateway/`；桌面接入位于
同级已有 Desktop 工作区。本项目不与桌面端的既有平台 gateway MCP 混名。

| 里程碑 | 已交付与本地证据 | 验收边界 |
| --- | --- | --- |
| M0 协议与 Mock | 协议1.0、OpenAPI3.1的14路径/27组件 schema、Go Mock、通用 HTTP 一致性客户端；Mock 226 PASS/0 FAIL/0 SKIP | 全部上游、签名、usage和错误账户为合成 fixture；不证明真实供应商行为 |
| M1 网关核心 | 同协议原生转发、账号池/冷却/亲和、用户/API key、目录/账户、流式旁路计量、取消和首字节前切换；真实PG/SQLite与生产HTTP一致性189 PASS/0 FAIL/10 SKIP | 真实Step文本已验证；真实OpenAI Responses、Claude、Gemini完整矩阵未完成 |
| M2 商业闭环 | 整数预扣/精确结算/账本/套餐/兑换码、官方Stripe/支付宝/微信支付适配、订单/回调/主动对账、运行时管理控制台及Docker Compose | 本地账务与合成签名/回调/重放校验完成；真实商户支付仍未验收，不能宣称正式商业运营闭环已获证明 |
| M3 可选桌面 provider | 零状态添加运营方/provider/8模型9能力、三份连接、目录/账户、key轮换、GUI刷新、Mock健康调用及真实Minimal Agent文本调用；真实余额不足提示/充值入口、880x600页面通过 | WebUI实际操作；原生Tauri/OS deep link未验收 |
| M4 发布准备 | 非root单二进制镜像、两种数据库持久化、健康/ready/metrics、恢复/对账/伙伴指南、运营责任与安全/来源审查、有界负载、许可门禁 | Linux amd64本地镜像/数据库已验证；真实域名、长时容量、商户/合规和HA没有被此记录认证 |

M0 当时的独立记录见 [m0.md](m0.md)，后续持续实现已获得用户授权。当前状态以本汇总和
[容器/数据库实际验收记录](../operations/acceptance.md)为准，M0阶段的停点不作为当前待确认事项。

## 2. 构建、测试与一致性

网关固定 Go1.27.1，控制台使用 Node24.18.0 和 npm 锁定依赖。最终源码检查由相应 owner/root
实际执行，以下“通过”是执行结果，不是仅提供命令供以后运行：

| 检查 | 命令/入口 | 结果 |
| --- | --- | --- |
| 网关完整测试 | `go test ./...` | 通过 |
| 网关静态检查/构建 | `go vet ./...`、`go build ./...` | 通过 |
| 最终Linux race | WSL、Go1.27.1，`go test -race -p 2 ./... -count=1` | 全部通过 |
| Mock独立HTTP进程 | `go run ./cmd/conformance`，M0 fixture | 226 PASS、0 FAIL、0 SKIP |
| 生产网关HTTP | 同一客户端，生产受控 fixture | 189 PASS、0 FAIL、10 SKIP |
| 最终控制台 | typecheck/build、控制台测试、Go embed检查 | 通过；14个控制台测试 |
| 依赖许可 | Go导入链/测试依赖与全部npm lock entries | 通过；257个npm包，未知/copyleft仍拒绝 |

生产一致性测试的10项跳过有明确原因：可选count_tokens未启用、合成工具/加密工具 replay断言
未开启、第二个授权key/限定目录key未配置、五种合成计费错误key未配置。它们不能写为生产
网关的189项通过，也不能用“0 FAIL”掩盖覆盖差异。对应行为另有Mock/聚焦单元测试，仍不替代
真实上游的工具、thinking、签名和缓存验收。

针对实际审查/运行暴露的问题，已修正并增加回归：原生输出/多候选/图像数量约束、缓存/推理
高价的保守预扣上界、Chat usage强制采集、每协议必需usage和整数/分项/总量/溢出检查、重复
计数与累计回退（含Anthropic start到首个delta）、不完整计量保留证据待对账、退出撤销会话、
PG独占锁失效停止以及嵌入控制台的HTTP200状态。

## 3. Desktop WebUI实际接入

桌面侧已执行的定向检查如下。不同 UI 套件包含重叠案例，不能把这些计数相加为独立用例总数。

| 检查 | 结果 |
| --- | --- |
| `nomifun-model-invoke` 全 crate | 444 PASS（431 unit、11 manifest、2 URL） |
| `nomifun-api-types` 全 crate / authoritative ts-rs 导出 | 566 PASS，17 份新增绑定 |
| `nomifun-system` 全 crate；精度更新后的 `model_gateway` 定向检查 | 312 PASS；后续 11 PASS |
| DB `provider_repository` | 31 PASS，含 7 个原子 provider/model/connection 图案例 |
| `nomifun-chat-model-broker` 最后完整检查 | 65 PASS（22 unit、43 conformance），包含生产 sanitizer 修复 |
| `nomifun-net provider_gateway_error` / `nomifun-ai-agent gateway_` | 2 PASS / 7 PASS |
| App HTTP 业务错误、provider control 字段剔除、AI 精确协议配置 | 各 1 PASS |
| 相关 UI 基础 / 临时提示 / 精度套件 | 8 文件 37 PASS / 4 文件 50 PASS / 2 文件 10 PASS |
| 最终真实 mapper 身份 / 账务入口 / 提示 UI 回归 | 3 文件 22 PASS、62 assertions；覆盖 wire reference 的空 platform |
| TypeScript、i18n、desktop UI boundary、Agent Session boundary、diff 空白检查 | 通过；最终 UI 2004 sources、880x600 contract、无 canonical Session 存储改动 |

Root在实际WebUI完成零状态添加，运营方为 `Local Acceptance Partner`，平台为
`nomifun-model-gateway`，落库8个模型、9项能力。模型/任务/协议/连接角色来自目录，并保留
context/output上限；不是仅渲染一个未接入后端的示例表单。

| 连接角色 | 鉴权 | 本地验收地址 |
| --- | --- | --- |
| default | bearer | http://127.0.0.1:18789/v1 |
| anthropic | header_key:x-api-key | http://127.0.0.1:18789 |
| gemini | header_key:x-goog-api-key | http://127.0.0.1:18789 |

账户接口的余额/额度以精确字符串进入桌面DTO，相关安全证据记录类型为String；未先转为
JavaScript浮点再做金额计算。API方式轮换key后GUI刷新，并验证各命名连接都使用新key：
Anthropic/Gemini Mock健康调用成功，对应账务记录的 `api_key_id` 均为3、状态settled、用量分别
26/20 tokens。健康检查本身没有写入Agent Session，不把它当作Agent完整会话验收。

**真实Agent文本证据：** root使用Minimal Agent，经桌面可选provider→本地网关→StepFun Coding
Plan的 `step-3.5-flash` 实际返回 `OK`。安全账务证据：

```text
request_id: fd574b71be2a4acd161c32b5da53c36c
public model: step-plan-real
api_key_id: 3
upstream_accepted: true
state: settled
actual_tokens: 1590
actual_amount: 0
```

该条只证明真实文本聊天和网关计量落账；不声称真实工具调用、thinking签名或缓存全矩阵通过。
`amount=0` 是本地user2的active套餐覆盖该模型，1590 tokens消耗套餐quota而不扣钱包；模型本身
测试定价仍为每请求2个USD最小货币单位。它不表示真实上游没有消耗或官方提供免费付费服务。
另一次真实网关Chat SSE探针为HTTP200、终态DONE、原生usage17/128/145，网关同样记录145
tokens并settled、测试金额2，原生/规范化计量一致。

### 最小桌面视口与计费提示验收

- Desktop最小支持视口880x600：root已实际验证账户卡可用；添加弹窗中匿名meta、运营方和
  key发送目标可见，滚动能到读取目录/取消/添加所选模型底部按钮。已取消操作并重置视口。
  此时使用wallet测试fixture（余额0、key ID6、无套餐），不是把该测试账户状态写成上文key3
  套餐会话状态。截图为 `m3-provider-880x600.jpg`、`m3-add-880x600.jpg`、
  `m3-add-footer-880x600.jpg`；没有手机/平板仿真或低于880px的验收。
- 真实余额不足提示与购买/续费CTA：**最终 GUI 已通过**。user3 无订阅、钱包0，key6无配额
  上限；真实生成请求返回402 `insufficient_balance`，无上游用量记录、无扣费。界面同时显示
  本地固定余额不足说明、`Recharge / renew` 按钮和 `Open model settings` 入口，仍维持原有
  canonical Paused 状态。880x600 截图为 `m3-billing-final-880x600.png`，结构化证据为
  `gui-desktop-billing-evidence.json`。购买地址来自所选运营方 meta 的 HTTPS 链接；没有打开
  合成购买目的地或把此检查写为原生系统浏览器验收。
- 实际运行修复了两层断点：生产 Broker 清洗器原先抹掉已验证的安全 action；会话模型引用
  的空 platform 原先阻止 CTA。前者仅保留精确本地 action/code/retry，完整 Broker 65项通过；
  后者仅在 React 账户展示 context 中按 provider ID 从当前目录解析身份，22项 UI 回归通过。
  未修改 canonical DTO、model reference 或持久化事实链。
- 最终配对 WebUI 构建 ID 为 `cd7d9a21-132f-4373-b4bc-a1f0a2e83500`，主资源
  `index-DquuKKIx.js`。最后 UI build、typecheck、desktop boundary 与 static Web host build 均通过。

## 4. 最终界面与Docker成品

实际浏览器曾发现React19与Arco静态Message/Modal的旧式render不兼容：后端兑换成功后，
toast异常被显示成API失败。已删除7个静态调用，改为React管理的声明式反馈/确认并重建
嵌入资产。不能把修复前的成功后端操作算作当时UI反馈成功。

当前最终本地镜像及资源：

```text
nomifun-model-gateway:m4-validation
sha256:a134585689ffab1f77186cdcf301efea4c31fb6775458763ce2e8c176eaa1e98
12,116,422 bytes；Linux amd64；UID10001:10001
JS index-DEGTvusM.js
CSS index-C4U-HF4R.css
```

真实PG18.6与SQLite Compose均使用该镜像并healthy，根目录只读；ready/console/SPA及实际JS/CSS
资源均200。最后1次私有Mock Chat SSE成功、0错误，PG余额9934→9933，最终67次settled、68条
账本、钱包/所有key预扣均0、无reconciliation。

历史计量镜像 `9212378c...` 的10次回归、较早 `34732e84...` 的四类原生/Responses续接、PG锁会话
故障恢复、dump/新库恢复和短突发负载保留其原hash，不伪写成新JS镜像重新执行了全部压力测试。
PG锁失效ready在0.411秒内丢失、26.011秒后恢复（含25秒guard），数据库和key保存；真实备份
restore保留迁移/用户/key/账本及主密钥校验记录。详见 [operations/acceptance.md](../operations/acceptance.md)。

## 5. 安全证据与工作区保留

本次公开记录仅保留模型ID、请求ID、数字计量、HTTP结果、资源名和镜像hash；不包含API key、
管理员密码、主密钥、商户材料或完整请求/响应内容。整理此文时**没有读取或打印**
`.tools/acceptance-state.json`。

执行机的 `.tools` 安全证据包括 `m0-conformance.log`、`production-conformance.log`、
`gui-desktop-provider-evidence.json`、`gui-desktop-rotation-evidence.json`、
`gui-desktop-real-step-usage.json`、`stepfun-real-report.json`，以及相应GUI截图；这些是本地
运行资料，不自动成为公开发行物。容器/数据库/build报告和合成配置位于
执行机系统临时目录下的 `nmg-m4-validation-20261007/`。

Root另以真实Step凭据进行最终内部安全扫描：4773个源文件、16个日志（12个非空），结果0次明文
泄漏；扫描时没有把凭据放入命令参数或报告正文。此记录不公开匹配用的秘密值。

完成 GUI 后已恢复桌面测试连接为轮换 key3，并移除临时真实 Step 上游渠道。按进程记录、
二进制路径、隔离数据目录和端口归属验证后，仅停止本任务 WebUI、生产网关和私有 Mock；
18788、18789、18887 均已无监听，原 PID 31664、8840、11300 均已退出。
PG/SQLite 运维容器此前也已停止；保留两份数据库卷、隔离 restore 库、私有测试数据、备份和
证据。安全收尾记录为 `cleanup-report.json`；没有清理其他业务数据或运行栈。
源改动仍未提交/推送。应用发行保留Apache-2.0 LICENSE、
NOTICE、Go/npm许可归属，官方不运营付费服务的文案边界保持一致。

## 6. 尚未建立的外部/原生证明

- 真实OpenAI Responses、Claude、Gemini的流式、工具、thinking签名、缓存/推理usage和
  stateful/account affinity全矩阵；已完成的Step文本不能替代这些项目。
- Stripe官方测试环境、支付宝沙箱、微信支付v3真实商户交易、通知和主动查询；合成签名/加密
  fixture不是商户验收，微信v3不声称有官方沙箱。
- 原生Tauri窗口/OS协议关联/deep-link实际唤起、安装包与系统浏览器集成；WebUI测试不能替代。
- 长时间真实业务容量、SLA/事故恢复、运营法律/支付资质和高可用证据；多节点HA本身为v1排除项，
  单进程锁/限流或短突发数字不建立HA承诺。

这些项目保持“未验证/范围外”的准确状态，不因用户无法提供多数外部条件而要求中途补齐，
也不因代码被复用或本地测试通过就标为已验收。
