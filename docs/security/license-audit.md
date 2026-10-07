# 源码、依赖与发布归属

项目主许可证为 Apache-2.0。网关主干、协议、Mock、一致性工具和本地 release 工具为原创实现；
没有复制 new-api、one-api、Bifrost、one-hub、done-hub 或 sub2api 的网关源码。
端点公开词表与原生 HTTP 行为不是对这些项目源码的引入。需要借鉴时按 AGENTS.md 的固定提交、
明确许可、改动说明和 NOTICE 要求单独记录。

实际版本以 go.mod/go.sum 和 console/package-lock.json 为准，不用 README 的浮动版本替代锁。
当前直接采用的许可类别包括：Gin/GORM/gormigrate/SQLite driver/Stripe SDK 的 MIT，
官方微信 Go SDK、gopay 和 JSON Schema 编译器的 Apache-2.0，pgx 的 MIT，Go x/* 的 BSD-3-Clause；
前端 React、Arco、i18next、Vite 等直接依赖均有明确宽松许可。yaml.v3 原始许可为 MIT/Apache-2.0。
这些名称是依赖类别说明；完整导入链仍由工具逐包核对，不能只查看直接依赖就宣称卫生通过。

## 可重复检查

```powershell
pwsh -NoProfile -File scripts/check-licenses.ps1
node --test scripts/check-frontend-licenses.test.mjs
node scripts/collect-frontend-notices.mjs
```

Linux 使用 `bash scripts/check-licenses.sh`。Go 工具固定
`github.com/google/go-licenses/v2@v2.0.1`（Apache-2.0），检查所有导入的 transitive 和测试依赖。
前端检查所有 lockfile 包含的运行、开发、optional 和 transitive entries，并验证引用覆盖、
版本/归档完整性与许可。仅允许 Apache-2.0、MIT、BSD-2-Clause、BSD-3-Clause、0BSD、ISC、
Unlicense、CC0-1.0；未知许可、缺失元数据、copyleft 许可或包含 copyleft 的 OR 表达式均失败。

0BSD 根据 [SPDX Zero-Clause BSD 条目](https://spdx.org/licenses/0BSD.html)与实际锁定包许可
核对后显式加入，不使用宽泛的 BSD 通配，也不放宽 unknown 检查。工具的许可证识别不代替来源
核对；go-licenses 的非 Go/汇编警告需要逐项确认。当前导入的 x/sys、x/crypto 汇编属于 BSD-3-Clause
源，ugorji codec 属于 MIT；这些警告不是静默跳过新依赖的授权。

Docker 构建对应用依赖再跑严格 gate，并将 `go-licenses save` 产生的文档保存到 `/licenses/go`；
前端 publisher 的 LICENSE/COPYING/NOTICE 聚合在 `/licenses/frontend.txt`，项目许可与 NOTICE 也
随镜像保存。未安装的 optional 平台包只保留已检查的锁元数据，不声称将其二进制随本镜像分发。
UnoCSS monorepo 包缺失的独立许可文件由固定 v66.10.5 的真实 commit 许可补齐；
number-precision 1.6.0 在固定 commit 的 package.json 明确声明 MIT、author 为 cam song，
原始树/包没有独立 LICENSE，保留声明与作者而不捏造版权年份。准确来源见
[补充归属](../../licenses/frontend/README.md)。其余缺失独立许可文件的已安装条目为原生构建工具，
不分发到 scratch 运行层。单独分发应用二进制时，也必须一并分发这些第三方许可/归属文件。

构建镜像和 PostgreSQL 是独立基础设施发行物，沿用各自分发许可；应用 Go/npm 允许列表不是
对整个工具链/操作系统的许可认证。网关运行层采用 scratch/CGO0，避免附带无关 OS 工具。

许可证文本和工具行为依据 [Apache-2.0 原文](https://www.apache.org/licenses/LICENSE-2.0)、
[go-licenses 官方说明](https://github.com/google/go-licenses/tree/v2.0.1)与各锁定模块自带许可核对。
