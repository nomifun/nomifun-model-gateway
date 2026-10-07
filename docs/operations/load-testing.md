# 有界本地压测

`go run ./cmd/load-test` 默认只向本机 `http://127.0.0.1:8789/healthz` 发送
100 次 GET，4 个并发，每次最多 10 秒，总计最多 60 秒。这不调用模型、不产生上游 token
或支付费用。响应输出仅为计数、状态码、吞吐与延迟，不能从输出获得 URL、key、请求或响应内容。

| 环境变量 | 默认值 / 范围 |
| --- | --- |
| NMG_LOAD_URL | http://127.0.0.1:8789；绝对 HTTP(S) 根地址，无凭据、查询或 fragment |
| NMG_LOAD_ENDPOINT | /healthz；不含查询的绝对路径 |
| NMG_LOAD_METHOD | GET；仅 GET / POST |
| NMG_LOAD_API_KEY | 空；以 bearer 请求头发送，非本机认证请求必须 HTTPS |
| NMG_LOAD_BODY_FILE | 空；POST 可读取最多 5 MiB 的 JSON 文件 |
| NMG_LOAD_REQUESTS | 100；1–100000 |
| NMG_LOAD_CONCURRENCY | 4；1–256 |
| NMG_LOAD_REQUEST_TIMEOUT | 10s；1ms–5m |
| NMG_LOAD_TOTAL_TIMEOUT | 60s；1ms–30m |

示例：

```powershell
$env:NMG_LOAD_URL = 'http://127.0.0.1:19189'
$env:NMG_LOAD_REQUESTS = '1000'
$env:NMG_LOAD_CONCURRENCY = '16'
go run ./cmd/load-test
```

报告字段包括 completed、successful、errors、status_counts、requests_per_second 和
latency_ms 的 min/p50/p95/max。延迟分位值覆盖已完成的所有尝试，包括失败；总截止时间到达时
completed 可能少于 requested。出现错误或没有完成全部请求时进程返回非零，不能把被取消的
请求遗漏后宣称全部成功。单次响应上限 64 MiB，客户端不跟随重定向。

目录、余额和模型压测必须显式选用自己控制的测试 key / 模型。真实推理请求可能产生费用，
不能把健康检查吞吐当作真实模型流吞吐、可支持用户数或生产容量。验证原生流应同时检查工具、
thinking 签名、usage、缓存字段、取消和亲和；检验账务应同时检查余额、额度、预扣和账本。

结果记录机器、数据库、应用版本、请求类型、并发、持续时间、p95、吞吐、错误与账务变化。
真实上游限流和付费延迟另行测量。本地合成负载或 healthz 成功不建立 HA / SLA / 商用容量证据。
