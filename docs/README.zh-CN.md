# 使用文档导航

[English](README.md) | 简体中文

安装、配置、运营者与用户流程、原生 API 示例从[项目 README](../README.zh-CN.md)开始；
各控制台页面及前端开发流程见[控制台 README](../console/README.zh-CN.md)。下表提供
协议、运维操作和注明日期的验收证据入口，部分专题文档目前使用中文。

NomiFun 提供独立的 Apache-2.0 网关项目，社区伙伴自行运营实例，并负责售价、条款、
隐私、支付及售后。NomiFun 官方不运营网关实例，也不出售 API。

## 按目标查找

| 目标 | 入口 |
| --- | --- |
| 本地安装并启动网关 | [根目录快速启动](../README.zh-CN.md) |
| 安装前了解产品界面 | [根目录产品说明与截图](../README.zh-CN.md)、[截图目录与来源](screenshots/README.zh-CN.md) |
| 录入供应商并发布模型 | [运营者操作指南](../README.zh-CN.md)、[供应商预设与目录发现边界](operations/provider-presets.md) |
| 使用用户工作台或管理员控制台 | [控制台使用指南](../console/README.zh-CN.md) |
| 创建密钥并调用模型 | [用户流程与原生 API 示例](../README.zh-CN.md) |
| 连接 NomiFun Desktop 或其他客户端 | [根目录 Desktop 接入说明](../README.zh-CN.md)、[伙伴接入指南](partners/integration.md) |
| 不使用付费上游进行协议开发 | [根目录 Mock 与符合性说明](../README.zh-CN.md) |

## 协议与客户端接入

| 文档 | 内容 |
| --- | --- |
| [Protocol v1](protocol/v1.md) | 冻结的公开元信息、模型目录、账户、原生端点、认证与错误合同 |
| [OpenAPI](../openapi.yaml) | 可供工具读取的公开 API 合同 |
| [伙伴接入指南](partners/integration.md) | 运营方与客户端边界、Desktop 运行时接入、不含凭据的接入链接 |
| [供应商录入与 Desktop 交接](partners/provider-onboarding-desktop-handoff.md) | 本地合成 fixture 操作、供应商录入证据及 Desktop 待验收项 |

客户端应依赖公开协议，而不是控制台内部 `/api/console/v1` 会话 API 或 Mock 的合成实现
细节。原生调用必须使用所选公开模型声明支持的任务与端点。

## 部署与运维

| 文档 | 内容 |
| --- | --- |
| [部署指南](operations/deployment.md) | SQLite/PostgreSQL、Docker/Compose、监听与环境变量、HTTPS 及单进程边界 |
| [供应商预设](operations/provider-presets.md) | 上游产品地址、原生认证、模型发现限额与手动映射 |
| [备份与恢复](operations/backup-and-recovery.md) | 数据库备份、主密钥单独保存及恢复演练 |
| [对账指南](operations/reconciliation.md) | 中断请求预扣、核实用量与商户订单对账 |
| [负载验收](operations/load-testing.md) | 有界本地 HTTP 验收、默认免费健康请求与数值报告 |
| [验收边界](operations/acceptance.md) | 确定性本地测试、真实上游/商户验收与生产证据的区分 |
| [运营者责任清单](compliance/operator-checklist.md) | 独立服务的条款、隐私、售后及上线责任 |

## 安全、许可证与验收证据

| 文档 | 内容 |
| --- | --- |
| [安全审查](security/review.md) | 凭据、访问、转发、支付、运维控制及其限制 |
| [许可证审计](security/license-audit.md) | 仅允许宽松许可证的依赖门禁、来源及发行声明 |
| [前端补充归属](../licenses/frontend/README.zh-CN.md) | 固定版本的发布者归属与声明收集命令 |
| [M0 验收记录](validation/m0.md) | 协议、Mock 与符合性检查的阶段证据 |
| [M0–M4 交付记录](validation/delivery.md) | 网关、Desktop、容器、许可证证据及未完成的外部验收 |
| [控制台 UI 重做验收](validation/ui-redesign.md) | 第一轮布局与交互验收 |
| [UI 组件验收](validation/ui-components.md) | 后续组件移植、授权归属与交互证据 |
| [截图采集说明](screenshots/README.zh-CN.md) | 当前中英文产品图片、合成数据范围及刷新方法 |

本地验证命令见[根目录 README](../README.zh-CN.md)，贡献时遵守[仓库规则](../AGENTS.md)。
验收文档都有自己的日期和范围。本地 Mock 通过、合成签名支付回调通过或产品截图完整，
都不能代替真实上游保真度、商户入账、持续生产容量或多节点高可用验收。
