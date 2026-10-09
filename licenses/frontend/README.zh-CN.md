# 前端补充归属说明

[English](README.md) | 简体中文

应用启动方法和产品截图见[网关使用指南](../../README.zh-CN.md)与
[控制台指南](../../console/README.zh-CN.md)。本目录记录发行物收集第三方声明时所需的
补充发布者归属信息。

此处仅保留许可证和归属文本，没有复制实现源码。

| 锁定依赖 | 发布者源码 | 精确提交 | 许可证与修改情况 |
| --- | --- | --- | --- |
| UnoCSS 66.10.5 与同版本 @unocss 包 | [unocss/unocss](https://github.com/unocss/unocss) | `ccb92ea634f4dbfae0a9d8d352fac11df382da65`（解析后的 v66.10.5 提交） | MIT；保留原始 `packages-integrations/vscode/LICENSE` 文本，未修改内容 |
| number-precision 1.6.0 | [nefe/number-precision](https://github.com/nefe/number-precision) | `6e721680ca116b5b2c3c03db2b36ac57358cd595`（npm gitHead） | 发布者 `package.json` 明确声明 MIT；保留作者归属；原始源码树和归档没有独立 LICENSE |

UnoCSS 根目录的 LICENSE 是指向上述文件的符号链接。其 npm monorepo 包并非全部携带
该独立文本，因此收集时补充这份固定来源的副本。number-precision 的 MIT 声明和
发布者作者 `cam song` 均保留在其归属说明及聚合包元数据中。聚合文件包含其他 MIT
依赖的标准 MIT 授权文本，没有捏造缺失的原始版权声明或年份。

收集器也记录当前平台未安装的可选原生构建包。它们属于构建工具，不随 scratch 运行镜像分发。

## 审计与声明收集

使用 Node.js 24 或更高版本，先在 `console/` 中运行 `npm ci --ignore-scripts`，
再从仓库根目录执行：

```sh
node scripts/check-frontend-licenses.mjs .
node --test scripts/check-frontend-licenses.test.mjs
```

[声明收集器](../../scripts/collect-frontend-notices.mjs)读取控制台的精确 lockfile
和已安装的发布者文件。它先审计 lockfile，再收集 LICENSE、COPYING 和 NOTICE 文本。
默认输出为 `.tools/frontend-license-notices.txt`，执行前需要创建输出目录。

PowerShell：

```powershell
New-Item -ItemType Directory -Force .tools | Out-Null
node scripts/collect-frontend-notices.mjs
```

Bash：

```sh
mkdir -p .tools
node scripts/collect-frontend-notices.mjs
```

在 `console/` 运行 `npm run build` 也会生成运行依赖声明
`console/dist/THIRD_PARTY_LICENSES.txt`，并复制到受版本控制、由 Go 嵌入的
`internal/console/assets/THIRD_PARTY_LICENSES.txt`。Docker 将更完整的发布者声明
聚合文件保存为 `/licenses/frontend.txt`，补充来源文件保存在
`/licenses/frontend-sources/`，Go 声明保存在 `/licenses/go/`，项目 LICENSE
和 NOTICE 保存在 `/licenses/`。

分发二进制或镜像时须保留所需声明。许可标识进入允许列表不能代替来源核查与再分发审查。
完整规则见[许可证审计](../../docs/security/license-audit.md)、
[仓库规则](../../AGENTS.md)、[项目 LICENSE](../../LICENSE)与[NOTICE](../../NOTICE)。
