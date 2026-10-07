# 社区伙伴接入

伙伴独立部署并运营网关，独立负责套餐、价格、账单、服务条款、隐私政策和用户支持。
NomiFun 官方提供开源软件、协议和可选 provider，不运营任何付费服务。不能把伙伴自己的实例
标为 NomiFun 官方托管，也不能让桌面端的其他功能依赖本网关订阅。

## 接入顺序

1. 通过 HTTPS 发布自己的 API 根地址；不要求用户添加 `/v1` 后缀。
2. 配置 meta 中真实运营方名称和 HTTPS 链接；运行时设置品牌、目录、价格、套餐、渠道和支付。
3. 创建授权范围、额度和有效期明确的 API key，明文只在创建时展示一次。用户在桌面端填写
   网关根地址和 key；伙伴无需修改或重建桌面端。
4. catalog 只返回此 key 有权使用的模型。每个任务提供明确、可识别的 preferred_endpoint，
   Anthropic chat 路径提供正整数 max_output_tokens。不能让桌面端猜测协议。
5. 用 [协议一致性工具](../../README.md#mock-and-protocol-conformance)和自己的受控模型 fixture
   检查兼容性；再完成原生流、thinking 签名、工具、usage、缓存与亲和等真实上游验证。
6. 用户在桌面端进入可选 NomiFun 模型网关 provider 后选择模型。充值/续费通过伙伴 HTTPS 页面
   在系统浏览器中完成，API key 不随购买 URL 传递。

## 无凭据 deep link 按钮

控制台可以生成下列按钮。参数必须编码，deep link **绝对不能包含 key、token、密码或其他凭据**。

```html
<a id="add-to-nomifun" href="#">添加到 NomiFun Desktop</a>
<script>
  const params = new URLSearchParams({
    platform: 'nomifun-model-gateway',
    base_url: 'https://gateway.example',
    name: 'Example Community Operator'
  });
  document.getElementById('add-to-nomifun').href =
    'nomifun://add-provider?' + params.toString();
</script>
```

这个链接仅预填 provider 类型、根地址和名称。用户随后在桌面端确认运营方并填写 key。
普通 HTTPS base URL 和运营方名称是接入信息，不是登录凭据；把 key 编码也不会使它可以进入 URL。
不要把订阅购买链接、API key 或多个网关地址塞进 base_url 参数。

同一个 key 接受 bearer、x-api-key 与 x-goog-api-key 鉴权，支持各协议原生客户端；Gemini 的
查询 key 仅是被冻结协议中的兼容例外。新集成优先使用请求头；未来设备码登录也不允许 URL key。

伙伴首次上线前完成 [运营责任清单](../compliance/operator-checklist.md)、
[运行说明](../operations/deployment.md)和 [安全/许可证审查](../security/review.md)。
