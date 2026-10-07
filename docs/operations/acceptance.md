# 实际本地验收与外部边界

记录日期：2026-10-07（Asia/Shanghai）。网关源码、Mock、数据库、商户合成测试、桌面集成与
真实上游/商户分别验证。本页记录 gateway 发布准备实际执行的本地证据；真实外部缺项不能由
复制代码、Mock 返回值、签名 fixture 或编译成功补齐。

## Docker 与真实数据库

本机 WSL 提供 Docker Engine 29.1.3 / Compose 2.40.3。使用独立项目
`nmg-m4-validation`（PostgreSQL18.6）和 `nmg-m4-sqlite-validation`（SQLite），合成随机
主密钥/引导凭据放在仓库外的临时目录，没有使用真实上游或商户 key。

实际构建 `docker build --tag nomifun-model-gateway:m4-validation .` 成功。计量完整性最后修订前
的历史快照用于下述完整数据库/负载验证；其 hash 不能当作当前源码成品：

```text
sha256:34732e8403881a7669ddb2dac80a196db6aed846986d8c5510788a5fc5ffe9af
12,109,310 bytes; Linux amd64; USER 10001:10001
```

该快照包含 native 计费上界/usage 注入修复、PG 所有权 guardian、控制台 HTTP200 修复、Go/npm
许可证与补充归属。两种 Compose 部署均实际启动为 healthy，根文件系统只读，非 root 网关
成功写入其专用 SQLite 卷；PostgreSQL 实际创建两条 gormigrate 记录。/readyz、/metrics 与
/console/ 验证可访问；控制台不再出现“404 状态但返回 HTML”的集成问题。

| 实际检查 | 结果 |
| --- | --- |
| PG 管理员登录、审计入账、API key 创建与 account | 200/201，整数余额 10000；key 列表无明文/哈希 |
| 无效 API key、已退出的复制 session | 均 401 |
| 专用 PG advisory-lock 会话被终止 | readiness 0.411 秒内丢失，26.011 秒后恢复；包含 25 秒恢复 guard |
| 重启/失锁恢复后的数据 | 余额 10000 与原 key 鉴权保持 |
| 真实 pg_dump / 新库 pg_restore | custom dump 37134 bytes；隔离新库保留 migrations2 / key1 / ledger1 / 余额10000 / 加密主密钥校验记录 |
| PG 原生转发闭环 | Chat、Anthropic、Gemini SSE 与 Responses 初始/continuation 共5次成功、5次 settled、无待对账；明确每请求1分 tariff，余额9999→9994 |

原生闭环使用项目自有 Mock，仅绑定 Docker 专用 bridge 地址，不访问真实服务。一次先行 fixture
把 Anthropic 错配到 compatible 类型渠道，正确在预扣前返回503、没有扣费/预扣；调整为各自 native 类型后
完成上述闭环。这是配置错误与修正证据，不是把失败请求从账单中隐藏。

## 严格计量镜像回归（前一界面版本）

最后追加并覆盖了每协议必需 usage、null/小数/重复计数、分项/总量/溢出、Anthropic 终态 delta
和累计回退（包括 message_start 到首个 delta）、Gemini tool prompt 两种总量表达。
完整合法零 usage 可以结算；不完整或非法计量保留有效部分证据并待对账，不能猜为零扣费。
Relay owner 报告相关单元、HTTP17类负例和 vet 通过；计量源码冻结后重建的镜像：

```text
sha256:9212378c67784de1e8acbf7ba9280fe28ed46237c87f631640e51a1900b5f249
```

该新镜像重新用于真实 PostgreSQL 和 SQLite Compose，二者实际 healthy；/readyz、/console/
与 /console/keys SPA 返回200。使用原来保存的合成 key，通过私有 Mock 完成10次 Chat SSE，
10成功/0错误，余额9944→9934。真实 PG 最终66次请求 settled、67条账本，钱包/全部 key
预扣均0、无 reconciliation。没有为此重复前一快照的广泛压测，历史短突发数字仍标明其快照。
最终 source/build/快速回归记录在临时证据目录的 docker-final-counter-build.log 和
final-counter-report.json。当前验证仍限定为 Linux amd64 和本机受控网络。

## 当前最终界面/发布镜像

真实浏览器进一步发现 React19 与 Arco 静态 Message/Modal 的旧式 render 不兼容：后端兑换
已成功，反馈提示却抛错而被显示成失败。Console owner 将7个静态调用替换为 React 管理的
声明式反馈/确认，报告 typecheck/build、14个测试、257包许可与 Go embed 检查通过。
Go API、schema、迁移和上述计量算法没有再次变更，重新构建当前最终发布镜像：

```text
sha256:a134585689ffab1f77186cdcf301efea4c31fb6775458763ce2e8c176eaa1e98
12,116,422 bytes；Linux amd64
JS index-DEGTvusM.js；CSS index-C4U-HF4R.css
```

该镜像实际用于 PG 与 SQLite，二者 healthy、UID10001:10001、根目录只读。/readyz、/console/
和 SPA 为200，HTML及实际静态资源与上述名称一致。额外1次私有 Mock Chat SSE成功、0错误，
余额9934→9933；最终PG67次 settled、68条账本、钱包/key预扣0、无 reconciliation。
没有重复前一快照的计量压力验证。最终记录为 docker-react19-final-build.log 与 final-ui-report.json；
真实浏览器和外部 Step 证据由 root 另写交付验证记录，不能用本节基本 HTTP smoke替代交互验收。

## 有界负载与许可证

以下为上述历史快照镜像的短突发数据，不是持续容量、真实上游吞吐、用户数或 SLA：

| 请求 | 请求/并发 | 成功/错误 | 耗时 | 吞吐 | p95 |
| --- | --- | --- | --- | --- | --- |
| healthz GET | 1000 / 16 | 1000 / 0 | 0.226s | 4425 req/s | 17.05ms |
| Mock 上游的 Chat SSE，真实 PG 预扣/结算 | 50 / 4 | 50 / 0 | 0.385s | 129.88 req/s | 87.67ms |

运行工具为 `go run ./cmd/load-test`，密钥通过环境传入，输出只含数值指标。检查流式负载后的
余额/预扣/账本，不能只看 HTTP200。该50次请求实际使余额9994→9944，最终钱包/全部 key 预扣为0，
合计56次请求 settled、57条账本（含首次信用入账），没有 reconciliation。
进一步持续、取消、故障和真实供应商限制仍需独立验证。

`go test ./cmd/load-test -count=1`、`go vet ./cmd/load-test` 通过。
`pwsh -NoProfile -File scripts/check-licenses.ps1` 在最终源代码上通过：Go 导入链/测试依赖与
257 个锁定 npm 包。前端 gate 回归测试 5/5，未知/copyleft/缺失引用/完整性错误仍拒绝。
已核对 x/sys/x/crypto 的 BSD-3-Clause 和 ugorji codec 的 MIT 汇编警告。Docker 构建也执行
生产 Go 严格许可检查与 notices 收集。当前没有执行或发布远程 GitHub workflow。

其余核心单元/原生/商户与 Desktop 检查由相应模块 owner 报告；root 最终汇报应同时列出
这些证据，不能用本文的打包检查覆盖未跑的交叉平台、原生桌面或真实商户验收。

## 仍未建立的外部证据

- 真实 OpenAI、Anthropic、Gemini 和国内供应商经过 Desktop Agent 的全链路、thinking 签名、
  工具、缓存分项 usage、原生 Responses 账号 continuation；Mock 字节/计量不等同真实上游。
- Stripe 官方测试环境、支付宝沙箱、微信支付 v3 真实商户交易/回调/主动查询。微信 v3 不宣称
  官方沙箱；合成签名/加密回调不是商户验收。
- 真实域名 HTTPS、业务规模持续压测、多节点 HA、运营法律/支付资质、事故响应与服务承诺。

不因这些凭据/外部条件缺失而要求用户中途补齐，也不将未执行项目标为通过。当前软件可以继续
由运营方按 [责任清单](../compliance/operator-checklist.md)在自己的受控环境验收。

本地证据、合成配置、dump 位于执行机的
系统临时目录下的 `nmg-m4-validation-20261007/`；其中配置/状态文件含
合成凭据，不作为公开发行物。执行后只停止自有项目服务，保留数据库卷及此证据，不删除其他
Docker 项目、业务数据或秘密。
