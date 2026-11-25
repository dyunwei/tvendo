# API 参考

API 使用平台密钥，通过 `Authorization: Bearer <key>` 鉴权。密钥在 `/app/keys` 创建；不可用供应商密钥代替。账户数据仅对该账户开放。

所有金额均为人民币（CNY），以整数微元表示：`1 元 = 1,000,000 微元`。`price_micros`、`cost_micros` 等字段名保留，单位统一为微元。API 响应头 `X-Tvendo-Currency: CNY` 也用于标识旧数组接口的币种。

## 基础调用与旧版兼容

```sh
export TVENDO_API_KEY='tv_live_...'
curl http://localhost:8080/v1/tools \
  -H "Authorization: Bearer $TVENDO_API_KEY"

curl http://localhost:8080/v1/run \
  -H "Authorization: Bearer $TVENDO_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"tool":"tinyfish","endpoint":"search","parameters":{"query":"AI agents"}}'
```

成功返回 `{ "id": 42, "cost_micros": 1000, "data": ... }`。错误返回 `{ "error": "..." }`。响应 `503` 时先查询用量，避免无意重复调用。

- 同步 HTTP / JSON 和远程 MCP 工具；60 秒超时，8 MB 响应上限，1 MB 平台请求体上限。
- GET 上游参数编码为查询字符串；POST / PUT / PATCH / DELETE 上游参数作为 JSON 请求体。平台不转发用户提供的 Authorization 到上游。
- 不跟随上游重定向；默认只允许公网 HTTPS 上游，并在每次建立连接时验证解析地址。
- 本地集成测试可设置 `ALLOW_PRIVATE_UPSTREAM=true` 允许 HTTP / 私网，请勿在线上启用。
- 调用成功按配置的固定单次价格结算。上游失败、超时或超出响应限制退回预留款；崩溃遗留的预留款在 5 分钟后由后台恢复退款。上游可能已执行的操作无法撤销，平台不自动重试。
- 每个密钥可以读取该账户可用工具并发起调用，不能访问管理端；没有细粒度权限配置。

## 工具目录与详情

`GET /v1/info` 为公开接口，返回 `{ok:true,data:{brand,api_version,version,cli_available,...}}`。存在 CLI 产物时还返回 `cli_version`、`npm_package`、`npm_url`、`install_url`。

以下接口需要 API Key：

| 接口 | 参数 / 返回 |
| --- | --- |
| `GET /v1/catalog` | q 为可选需求或关键词；limit 默认 10，范围 1–50；cursor 为上页 next_cursor。配置模型后混合检索，详情见 [工具搜索](catalog-search.md) |
| `GET /v1/catalog/{tool_ref}` | 对引用做 URL 路径段编码；返回 Schema、示例、类型及价格 |
| `GET /v1/runs` | limit 默认 20，范围 1–50；cursor 分页；request_id 可筛选 |
| `GET /v1/runs/{id}` | 本账户单次调用记录，其他账户记录返回 404 |
| `GET /v1/runs/{id}/result` | 恢复本账户通过幂等键保存的成功结果，成功体与调用接口相同 |
| `GET /v1/balance` | balance_micros 与 currency=CNY |
| `GET /v1/budgets` | daily_limit_micros、call_limit_micros、daily_enabled、call_enabled、spent_micros、held_micros、resets_at、currency |

除结果恢复接口外，查询成功返回 `{ok:true,data:...}`。目录与调用记录列表的 data 包含 `items`、`next_cursor`；空游标表示没有后页。目录条目含 `currency`、`tool_ref`、name、provider、description、price_micros、schema_available；详情额外含 `input_schema`、`input_example`、`upstream_type`（若已配置）。

目录响应新增 `data.search`，含 mode（directory/keyword/hybrid）、degraded、reason、indexed、total。端点可设置并返回可选 `search_description`。非空查询游标绑定查询和候选顺序，结果顺序改变时返回 400，需从第一页重新搜索。

目录只显示已开放提供商下已启用、未被上游移除的工具。搜索按名称和描述匹配全部空格分隔关键词。`tool_ref` 是稳定的不透明引用，不依赖显示名称，不能自行拼接。

## 使用工具引用调用

```json
{
  "tool_ref": "<目录返回的引用>",
  "parameters": { "query": "AI agents" },
  "max_cost_micros": 1000
}
```

`POST /v1/run` 保留旧 tool / endpoint 参数形式，与 tool_ref 互斥。`max_cost_micros` 为可选非负整数，实际售价超限时不创建调用、不扣费；不传时仍执行账户预算限制。

建议客户端在调用前生成 `X-Request-ID`，要求为 8–80 位字母、数字、下划线或连字符。响应头返回该标识，已创建的调用记录也会保存它。成功体为 `{id,cost_micros,data,request_id,currency}`。请求标识用于查询核对。

可选的 `Idempotency-Key` 请求头同样接受 8–80 位字母、数字、下划线或连字符。每次逻辑调用使用一个新键，并在发送前保存键和请求标识。账户内相同键和参数只执行并结算一次。请求体字段顺序不影响比较，工具引用形式、参数值与费用上限须保持一致。重复调用返回原调用的请求标识、状态或结果。变更参数会返回 409；未创建调用记录的请求（例如预算超限）允许用原键再次提交。成功结果加密保存 24 小时，其后同一键仍会去重，结果返回 410。旧版本服务器可能忽略该请求头，重发前应确认部署版本支持幂等。

工具调用每账户每分钟 120 次、每把 API Key 90 次、全站 900 次，并发每账户 16、全站 128。目录查询每账户每分钟 60 次、每把 API Key 40 次、全站 480 次。启用模型时，非空搜索另外受每账户每日 500 次、全站每日 5,000 次请求配额限制，按 UTC 日计算，并发上限为每账户 4、全站 32。模型查询配额包含命中缓存和降级的请求。已存在的幂等调用可以查询或重放原结果，不消耗新的执行额度。

## 错误和结算

HTTP 错误保留字符串 `error`；费用相关错误增加 code、settlement、id、request_id。字段可能缺失，客户端不能仅靠中文文案判断。

| HTTP / code | 处理 |
| --- | --- |
| 400 | 参数或配置错误，未开始调用时修正输入 |
| 401 | 密钥无效或撤销 |
| 402 / INSUFFICIENT_BALANCE | 余额不足，settlement=not_started |
| 403 / BUDGET_EXCEEDED | 账户预算拦截，settlement=not_started |
| 403 / COST_LIMIT_EXCEEDED | 实际售价超出本次上限，settlement=not_started |
| 404 | 工具不可用或记录不存在 |
| 409 / RUN_PENDING | 原调用尚未完成，查询原调用记录 |
| 409 / IDEMPOTENCY_CONFLICT | 该键已经用于另一组参数 |
| 410 / RESULT_UNAVAILABLE | 调用已结算，结果未保存或已过期 |
| 429 | 请求频率、并发或模型限额用完，响应头 `Retry-After` 为建议等待秒数 |
| 502 / UPSTREAM_FAILED | 只有 settlement=refunded 才表示预留已退回 |
| 503 / UPSTREAM_OUTCOME_UNKNOWN | 上游结果待核实，维持余额预留，查询原调用记录 |
| 503 / SETTLEMENT_UNKNOWN | 先查询记录，禁止自动重试 |

调用记录状态为 running、success、failed、unknown。running 表示仍在执行或结算，unknown 表示上游结果待核实。收到成功响应但无法读取完整结果、发送后网络中断或进程中断时，余额保持预留并按预留当日计入预算。失败记录的 cost_micros 表示原预留金额，不代表仍收取该笔费用，应结合状态和账目判断。客户端超时或丢失响应时，用请求标识核对记录，不把「没有马上查到」当作可以安全重试的证明。

管理员可在「用量记录 → 待核实」查询上游记录，并在调用详情中选择确认成功扣费或确认失败退款，也可用管理员会话和 CSRF 令牌调用 `POST /api/admin/runs/{id}/resolve`，请求体传 `outcome`（`success` 或 `failed`）和 `note`（核实依据，最多 500 字节）。结算会记录操作人及核实依据，同一笔只能处理一次。

[Agent 接入](agent-access.md) · [文档索引](README.md)

## 控制台历史记录

`GET /api/usage` 和 `GET /api/transactions` 使用登录会话。调用记录支持按 `status` 筛选，资金流水支持按 `kind` 筛选。两个接口都接受 RFC 3339 的 `from`（包含）和 `before`（不包含）时间边界。指定筛选条件或 `limit` 即返回 `{items,next_cursor}`，`limit` 范围 1–50，默认 25。要查询下一页，沿用筛选条件并传入 `cursor`。无参数的旧接口仍返回最近 200 条数组。

使用 `format=csv` 导出所有符合条件且有权查看的记录，不传分页参数。CSV 金额为整数微元。管理员的调用记录可包含所有账户，资金流水仅可查看当前登录账户。

创建 API Key 时可选 `expires_at`（RFC 3339）、`allowed_tools`（最多 200 个目录的 `tool_ref`）和 `daily_limit_micros`。空权限列表允许全部工具，独立日预算与账户预算同时生效并在 UTC 00:00 重置。限定工具的 Key 仅能发现和调用这些端点，也只能查看自己执行过的运行记录。密钥过期时返回 401，工具权限或密钥预算拦截分别返回 `KEY_TOOL_DENIED`、`KEY_BUDGET_EXCEEDED`。
