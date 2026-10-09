# 控制台产品截图

[English](README.md) | 简体中文

这些截图拍摄于 **2026-10-09（Asia/Shanghai）**，来自当前工作区构建后真实运行的嵌入式
控制台。安装启动请看[网关使用指南](../../README.zh-CN.md)，各页面操作请看
[控制台使用指南](../../console/README.zh-CN.md)。

## 截图目录

| 页面 | 中文 | 英文 | 展示内容 |
| --- | --- | --- | --- |
| 账户概览 | [overview.png](zh-CN/overview.png) | [overview.png](en/overview.png) | 钱包余额、Desktop 接入步骤、账户信息及已结算调用 |
| 模型目录 | [models.png](zh-CN/models.png) | [models.png](en/models.png) | 公开模型 ID、任务标签、上下文与输出限额 |
| API 密钥 | [api-keys.png](zh-CN/api-keys.png) | [api-keys.png](en/api-keys.png) | 密钥前缀、额度与撤销操作 |
| 管理员上游渠道 | [channels.png](zh-CN/channels.png) | [channels.png](en/channels.png) | 上游渠道、映射、发现、探测与编辑操作 |
| 供应商快捷录入 | [channel-onboarding.png](zh-CN/channel-onboarding.png) | [channel-onboarding.png](en/channel-onboarding.png) | 原生模型发现与四步配置向导 |

## 拍摄范围

网关与 `cmd/provider-onboarding-fixture` 运行在独立的本机回环实例上，端口为
**18921 / 18922**，SQLite 数据库位于忽略目录 `.dev/readme-screenshots/`。
控制台读取真实 API 响应，没有拦截替换前端响应，也没有启用示例数据回退。

图中的运营方、`example.test` 账号、API 密钥、三个公开对话模型、两个渠道和钱包余额
都是合成演示数据。三个公开模型均映射到本地 fixture 的 `synthetic-chat-v1`。
四个请求实际经过本地网关转发，并在本地账本结算。拍摄过程没有联系真实模型供应商或
商户，也没有产生付费上游调用。这些图片展示产品界面，不代表真实上游、支付、容量或
高可用验收。

浏览器视口为 **1440 × 1040**，设备缩放比为 1，采用浅色主题、通过界面切换中英文，
并启用减少动画。概览采用整页截图，因此图片高度大于视口。密码和上游凭据保持遮蔽；
密钥列表仅显示前缀，没有拍摄一次性完整密钥弹窗。

## 更新截图

1. 按[根目录快速启动指南](../../README.zh-CN.md)构建控制台和网关。
2. 使用现有[供应商录入验收脚本](../../scripts/provider-onboarding-acceptance.ps1)与
   [本地 fixture 说明](../partners/provider-onboarding-desktop-handoff.md)启动隔离合成实例。
   该脚本使用 18891 / 18892 / 18893 端口及自己的忽略数据目录；启动前检查其状态与端口归属。
3. 使用本地 fixture 配置中的合成管理员账号登录，通过真实控制台创建渠道、模型映射、
   能力及明确售价。需要独立用户账号时先开启注册。仅使用合成密钥和本地 fixture 地址。
4. 为演示账号执行有审计记录的合成钱包加款、创建受限密钥，并发送有界的本地原生请求以
   生成用量记录。不要使用生产账号、客户数据或商户交易。
5. 分别切换中英文，打开上述五个页面，按相同视口截图。将 PNG 保存到已有的语言目录，
   拍摄前关闭完整密钥弹窗，提交前逐张检查。
6. 仅停止本次 fixture 拥有的进程，保留忽略目录中的证据；同步更新本文件与英文版的
   拍摄日期、页面说明，保持根目录与控制台 README 的图注、链接一致。

这些截图属于文档资源，不是运行时必需文件。更新截图不需要修改公开协议或 Go 嵌入式
控制台资源。
