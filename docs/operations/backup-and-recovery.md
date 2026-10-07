# 备份、恢复与升级

数据库备份和主密钥备份必须成对管理。数据库包含加密的上游及商户配置；只恢复数据库或误换
主密钥会导致这些数据无法解密。主密钥备份放在运营方控制的秘密存储中，与数据库备份分开授权，
记录每份备份对应的应用版本、迁移版本和主密钥标识，不把密钥本身写进备份清单。

## PostgreSQL

使用容器内与服务器版本一致的 pg_dump。下面以 Compose 默认数据库和用户为例；应用仍运行
时备份获得数据库一致快照，但在途请求的预扣状态也会包含在快照内。

```sh
mkdir -p backups
docker compose --env-file .env -f compose.yaml exec -T database \
  sh -c 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > backups/gateway.dump
```

将备份文件加密、限制访问并复制到另一故障域。验证文件能被 `pg_restore --list` 读取，并定期
在**新建隔离数据库**恢复，核对用户、模型、渠道、账本、订单、亲和记录和迁移记录。
不要在仍接受请求的业务数据库上执行恢复或 clean 操作。

隔离恢复示例先选择新的 Compose project 和独立端口、创建新 `.env`，其中主密钥必须与备份匹配：

```sh
docker compose --project-name nmg-restore --env-file .env.restore -f compose.yaml up -d database
docker compose --project-name nmg-restore --env-file .env.restore -f compose.yaml exec -T database \
  sh -c 'exec pg_restore --no-owner --no-privileges -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < backups/gateway.dump
docker compose --project-name nmg-restore --env-file .env.restore -f compose.yaml up -d gateway
```

恢复目标必须为空；不要用 `--clean` 擦除其他数据。校验 `/readyz`、登录、目录、账本恒等关系及
一次受控请求，再决定是否切换业务。切换前停止原实例，确保同一数据库只有一个网关进程。

## SQLite

停止本项目的网关后，再复制整个数据目录。不要在进程仍写 WAL 时只复制 gateway.db。

```sh
docker compose --env-file .env -f compose.sqlite.yaml stop gateway
mkdir -p backups
docker compose --env-file .env -f compose.sqlite.yaml cp gateway:/data backups/sqlite-data
docker compose --env-file .env -f compose.sqlite.yaml start gateway
```

保留整个目录副本和对应主密钥。恢复时先停止目标网关，将备份恢复到一个新的专用目录或卷，
确认 UID/GID10001 的所有权与写权限，再启动一次。使用宿主机目录挂载时先验证其绝对路径与
目标目录，不将恢复操作用于其他应用的数据目录。锁文件是运行协调数据，不是账本；原进程
停止后才能恢复，不能删除一个仍被运行实例持有的锁来启动第二个进程。

## 重启与在途账务

启动恢复不凭估计量收费。发送上游前中断的预扣可释放；上游可能已接受的请求转入
`reconciliation` 并保留预扣，直到运营方取得真实计量或拒绝证据。恢复后应主动检查
管理员控制台的 reservations 和待支付订单，按 [对账流程](reconciliation.md)处理。
主密钥错误、未知迁移或数据库异常都应先停止并调查，不能以清库或自动转换“修复”。

升级以保留数据库与已部署迁移 ID 的前向迁移为准。先在隔离副本完成升级和恢复演练，记录
运行版本与验证证据。版本回退需要确认旧应用能理解新 schema；不能仅替换二进制就认定可回退。
