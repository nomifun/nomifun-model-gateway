# 运行与部署

本项目由社区伙伴独立运营。NomiFun 官方不部署本网关、不出售 API，也不提供付费托管服务。
以下部署仅支持**同一个数据库对应一个运行中的网关进程**。SQLite 使用文件锁，PostgreSQL
使用独占实例 advisory lock；限流和认证节流仍为进程内状态，不据此宣称多节点高可用。

## 源码启动

使用 go.mod 固定的 Go 1.27.1 和 Node.js 24.18.0。先在 console 目录执行
`npm ci --ignore-scripts`、`npm run build`，再在仓库根目录执行 `go build ./cmd/model-gateway`。
控制台编译结果会复制到 `internal/console/assets`，这些文件必须进入源代码版本控制，不能当作
普通 dist 目录忽略。Go 构建将控制台嵌入单个应用二进制。

启动前，在进程环境设置下列值。真实凭据不能进入命令参数、仓库、模型请求日志或共享验收文件。

| 环境变量 | 默认值或要求 |
| --- | --- |
| NMG_MASTER_KEY | 必填：随机 32 字节、标准 base64 编码；加密上游及商户配置，必须单独备份 |
| NMG_DB_DRIVER | sqlite；生产建议 postgres |
| NMG_DB_DSN | SQLite 默认为 data/gateway.db；PostgreSQL 必须填写自己的 DSN |
| NMG_LISTEN | 127.0.0.1:8789；容器内部使用 0.0.0.0:8789 |
| NMG_ADMIN_EMAIL / NMG_ADMIN_PASSWORD | 首次创建管理员时成对填写；密码 12–72 字节；不覆盖已有账户 |
| NMG_CONNECT_TIMEOUT | 10s，上游连接与 TLS 握手超时 |
| NMG_UPSTREAM_HEADER_TIMEOUT | 60s，上游响应头超时 |
| NMG_STREAM_IDLE_TIMEOUT | 90s，流式无数据超时；没有流式总时长超时 |
| NMG_RESPONSE_AFFINITY_TTL | 720h，Responses 账号绑定保存期限；允许最大 8760h |
| NMG_REQUEST_BODY_LIMIT | 67108864 字节 |
| NMG_ANTHROPIC_VERSION | 2023-06-01，未提供请求版本头时使用 |

运行 `go run ./cmd/model-gateway` 后打开 `http://127.0.0.1:8789/console/`。
首次登录后，在管理员控制台设置运营方名称、HTTPS 主页/控制台/购买/条款/隐私链接、币种、
是否开放注册、模型、价格和上游渠道。创建后保存的管理员密码不会被环境变量重置；确认已有
账户正常后可以移除首次引导环境变量。支付默认禁用，只有运营方提供并验证商户配置后才能启用。

## Docker Compose

复制 `.env.example` 为 `.env`，填入自己的主密钥、首次管理员凭据和数据库凭据。
`.env` 已被忽略，Docker 构建上下文也排除了它。镜像不内置任何真实密钥。

PostgreSQL 方案：

```sh
docker compose --env-file .env -f compose.yaml config --quiet
docker compose --env-file .env -f compose.yaml up -d --build
```

`NMG_DB_DSN` 指向内部服务 `database:5432`；URI 密码中的特殊字符必须按 URI 规则编码。
生产示例固定 PostgreSQL 18.6 官方镜像的 manifest digest，持久卷挂到
`/var/lib/postgresql`，与 PostgreSQL 18 的版本化 PGDATA 目录一致。
数据库端口不向宿主机发布。默认网关端口仅发布到宿主机 127.0.0.1。

SQLite 方案是独立文件，不要与 PostgreSQL 文件合并：

```sh
docker compose --env-file .env -f compose.sqlite.yaml config --quiet
docker compose --env-file .env -f compose.sqlite.yaml up -d --build
```

SQLite 数据文件在命名卷的 `/data/gateway.db`。镜像的 `/data` 属于 UID/GID 10001，权限 0700，
首次创建命名卷会复制该所有权。若使用已有卷或改用宿主机目录，先由宿主机管理员确认目录属于
10001:10001 且可写；不要为了绕过权限错误把网关容器改为 root。保留 SQLite 数据文件、WAL、
共享内存文件及运行锁的目录边界，不把不同实例指向同一份数据。

网关运行镜像采用 scratch、非 root UID/GID 10001、只读根文件系统、单个应用二进制和 CA
信任包；控制台直接嵌入应用。构建阶段固定官方 Node 24.18.0 与 Go 1.27.1 镜像 digest。
PostgreSQL 使用官方镜像的初始化生命周期。公开接入必须由运营方提供 HTTPS 终止、域名和网络策略。

## 反向代理、探针与日志

- `/healthz` 只表示 HTTP 进程存活；`/readyz` 还检查数据库可用性。它们不能证明上游额度、
  计费结算或支付回调正常。镜像 healthcheck 使用 `--healthcheck` 对本机 `/readyz` 做 5 秒检查。
- `/metrics` 当前提供 `nomifun_gateway_http_requests_total`，标签为规范化路由、方法和状态；
  不包含用户 key、URL 查询参数或模型内容。它没有延迟直方图或分布式限流指标。
- 不要把 `/metrics` 发布给不需要的公众。反向代理应放行原生 SSE，关闭响应缓冲，保留请求取消，
  使用空闲超时；不能用短的请求总超时截断正常长流。
- 服务端没有配置可信代理，IP 限制以实际对端 IP 为准。代理后的原始 IP 策略需要在运营方设计
  中明确验证，不能假定 X-Forwarded-For 自动生效。
- 默认日志只记录结构化运行/路由结果，不记录原始请求/响应、查询字符串、请求头或 SQL 变量。
  反向代理、采集器和排障工具也必须遵守该边界，尤其不能记录 Gemini 查询 key。

启动在事务化 gormigrate 步骤内执行前向迁移；当前步骤为 `202610070001_gateway_core` 与
`202610070002_access_billing_payment`。未知迁移记录会拒绝启动。上线升级前备份数据库及对应
主密钥，在隔离恢复环境验证升级；不得手工删除 migrations 记录或盲目回滚 DDL 来强行降级。

主密钥不是普通可任意替换的配置。使用不同主密钥打开已有数据库会失败；当前没有自动重加密
轮换工具。更换前必须设计、实现并验证完整重加密与恢复流程，保留旧备份对应的旧密钥。

参考 [备份与恢复](backup-and-recovery.md)、[账务对账](reconciliation.md)、
[本地验收边界](acceptance.md) 和 [安全检查](../security/review.md)。

镜像版本及 PGDATA 目录依据 [Go 官方镜像](https://hub.docker.com/_/golang)、
[Node 官方镜像](https://hub.docker.com/_/node) 与
[PostgreSQL 官方镜像说明](https://hub.docker.com/_/postgres)核对。
