# codevisual 组件源码接入验收

日期：2026-10-07（Asia/Shanghai）。本记录承接 `ui-redesign.md` 的第一轮视觉重做。
用户明确确认拥有参考库原创组件的版权，并授权移植到本网关、按 Apache-2.0 分发。

## 实际移植与接入

| codevisual 原组件 | 当前组件与接入位置 |
| --- | --- |
| stat-card | `visuals/stat-card.tsx`；概览余额、订阅卡 |
| usage-meter | `visuals/usage-meter.tsx`；真实 Token 配额和进度 |
| state-empty | `visuals/state-empty.tsx`；用户页及管理表格空状态 |
| state-error | `visuals/state-error.tsx`；真实 API 错误和重试 |
| toast | `visuals/toast.tsx`；复制、兑换及其他真实反馈 |
| filters | `visuals/filters.tsx`；用户和管理列表的受控搜索、条件、计数 |
| command-palette | `visuals/command-palette.tsx`；分类导航、输入及键盘选择 |
| health-check | `visuals/configuration-checklist.tsx`；实际支付凭据存在性，未声称服务健康 |

保留参考组件的双层 frame/surface、图形结构、图标与动画方案，并补上真实业务 props
和事件回调。移除演示默认数值、虚假趋势/延迟、无限循环、isometric 和 fade-out；
业务内容不再 aria-hidden。动画为一次入场，遵守 reduced-motion。
搜索与筛选内容立即可见，不依赖 IntersectionObserver；命令面板增加中文 IME 保护、
上下键/Enter、取消后焦点恢复。表格分页和业务弹窗的成熟行为继续使用现有 Arco 实现。

新增 Motion 13.4.6（MIT）和 Lucide React 1.49.0（ISC，含继承的 Feather MIT notice），
全部版本精确锁定。没有引入 Tailwind 4 / Vite 8 或不可接受的依赖。

## 来源与分发

本地 `portal/codevisual` 不是 Git 仓库，没有源提交。原始文件按精确 SHA-256 快照锁定，
没有虚构 Git commit。用户的本地源码移植授权、逐文件摘要和修改说明分别保留在：

- `licenses/codevisual/provenance.json` 和 `licenses/codevisual/LICENSE`；
- 当前组件文件头；
- `console/notices/codevisual.txt`、根 NOTICE；
- 构建后的 `internal/console/assets/THIRD_PARTY_LICENSES.txt`。

明确排除 `world-map-dots.ts` 的商业 SSR 提取资产、`plugin-slot.tsx` 的来源不明头像，
以及商业 CodedVisuals 源码和受限免费示例。未修改参考库。

## 执行结果

环境：Go 1.27.1、Node 24.18.0。

| 检查 | 结果 |
| --- | --- |
| npm ci --ignore-scripts | 通过 |
| npm run typecheck / npm run build | 最终版本通过；Go 嵌入资源更新 |
| npm test | 最终 18/18 通过；新增真实数值、未知额度、真实动作/状态回归 |
| go test ./... / go vet ./... / go build ./... | 通过 |
| 最终嵌入资源检查和单二进制构建 | 通过 |
| scripts/check-licenses.ps1 | Go 导入链/测试及 262 个锁定 npm 依赖通过 |
| check-frontend-licenses.test.mjs | 5/5 通过 |
| 分发 notices | 检查包含 8 个源摘要、授权说明及 Motion/Lucide 完整许可 |

许可工具关于现有依赖汇编代码的提示继续保留；本轮没有新增这些非 Go 源依赖。

实际浏览器验收：

1. 最终 Go 内嵌版本巡检 17 个业务页，标题正确，没有错误组件；复杂表格继续分页/排序。
2. DOM 确认概览使用两张 StatCard 和一张 UsageMeter，模型管理和用户目录使用 Filters。
3. 搜索无匹配显示新 StateEmpty；真实厂商筛选生成 chip，逐项移除恢复全部厂商。
4. 新 CommandPalette 上下键选择和 Enter 跳转通过；Esc 后焦点回到打开按钮，hash 不变。
   中文 IME 组合态保护已源审查；本轮没有模拟 OS 原生输入法候选窗口。
5. 点击真实地址复制按钮展示新 Toast；反馈仍由 React 19 当前根渲染。
6. 提交无效合成兑换码，真实 API 拒绝后展示 StateError；重试重新读取数据并清除错误。
   没有真实兑换、支付或钱包变更；测试输入已清空。
7. 支付配置页使用 ConfigurationChecklist 展示实际凭据 presence，不展示密钥或假健康/延迟。
8. 390×844 手机宽度无页面横向溢出；浅色、深色指标卡已截图核对；临时视口恢复。

本机 `19195` 预览已更新为组件版 Go 二进制。沿用原数据库与原主密钥，启动前核对无
待处理预扣并保留数据库快照；用户已有运营方设置、模型和账户数据保留。
没有提交、推送、部署外部付费实例或发起真实上游/商户交易。
