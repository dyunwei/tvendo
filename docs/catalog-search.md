# 工具目录混合搜索

`tvendo tools search` 使用关键词和跨语言向量召回候选，配置 Rerank 后再按端点实际能力重排、过滤低相关结果。精确 `tool_ref` 和端点 ID 查询直接返回当前可用端点，不调用模型；空查询列目录。搜索候选不等于能力承诺，调用前仍需查看详情、Schema 和价格。

## 百炼接入（推荐）

使用 `qwen3.7-text-embedding`（1024 维）和 `qwen3-rerank`。从百炼控制台复制本业务空间、地域的完整接口地址，分别设置地址和密钥。以下为北京地域的地址结构示例，需替换 `<workspace>`，不能直接照抄。无需在聊天中提供密钥。

```dotenv
SEARCH_EMBEDDING_URL=https://<workspace>.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/embeddings
SEARCH_EMBEDDING_MODEL=qwen3.7-text-embedding
SEARCH_EMBEDDING_FORMAT=auto
SEARCH_EMBEDDING_DIMENSIONS=1024
SEARCH_EMBEDDING_REVISION=1
SEARCH_EMBEDDING_API_KEY=<本机设置的百炼密钥>

SEARCH_RERANK_URL=https://<workspace>.cn-beijing.maas.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank
SEARCH_RERANK_MODEL=qwen3-rerank
SEARCH_RERANK_FORMAT=dashscope
SEARCH_RERANK_API_KEY=<本机设置的百炼密钥>
SEARCH_RERANK_CANDIDATES=30
SEARCH_RERANK_MIN_SCORE=0.55
```

Embedding 使用 OpenAI 兼容协议，发送原始文本、`encoding_format=float` 和指定维度，每批最多 10 条。`auto` 仅对名称包含 `e5` 的模型添加 `query: ` / `passage: ` 前缀；可显式设置 `openai`（原始文本）或 `e5`。`SEARCH_EMBEDDING_DIMENSIONS=0` 或非千问模型留空表示不指定维度，使用模型服务默认值。

Rerank 默认使用 DashScope HTTP 协议：查询与候选放在 `input`，排序参数放在 `parameters`。也支持设置 `SEARCH_RERANK_FORMAT=openai`，配合控制台提供的完整 `/compatible-api/v1/reranks` 地址，使用扁平请求体。两种协议的响应均按 `index` 映射回本地端点，不使用模型返回的文档覆盖工具信息。维度不符、结果缺失、重复或越界索引、无效分数都视为模型服务异常。

官方参考：[Embedding 接口](https://help.aliyun.com/zh/model-studio/text-embedding-synchronous-api/)、[Rerank 接口](https://www.alibabacloud.com/help/zh/model-studio/text-rerank-api)。实际模型、地址、配额和可用地域以业务空间控制台为准。

配置后重启后端；Docker 部署可运行 `docker compose up -d app` 重新应用环境配置。模型服务凭据独立于用户的平台密钥及工具上游凭据；目录描述和搜索查询会发送到所配置的模型服务，费用由该模型服务账号承担，不计入用户工具调用记录。

## 通过 New API 转接百炼

Tvendo 连接的是 New API 时，使用 New API 的令牌和地址，不要填写百炼上游密钥。当前已验证的配置形式：

```dotenv
SEARCH_EMBEDDING_URL=https://<new-api-host>/v1/embeddings
SEARCH_EMBEDDING_MODEL=qwen3.7-text-embedding
SEARCH_EMBEDDING_FORMAT=openai
SEARCH_EMBEDDING_DIMENSIONS=1024
SEARCH_EMBEDDING_API_KEY=<New API 令牌>
SEARCH_RERANK_URL=https://<new-api-host>/v1/rerank
SEARCH_RERANK_MODEL=qwen3-rerank
SEARCH_RERANK_FORMAT=openai
SEARCH_RERANK_API_KEY=<New API 令牌>
SEARCH_RERANK_MIN_SCORE=0.55
```

这里的 `openai` 表示扁平 JSON 请求与顶层 `results` 响应，Rerank 路径为 New API 的 `/v1/rerank`。确认令牌允许访问两种模型。部分 New API 版本不会转发 `instruct`，不能依赖任务指令作为能力过滤保证；实际阈值需按转接服务返回的分数验证。

当前三个端点的真实测试中，“地图”最高分约 0.386，天气查询对应天气工具约 0.581–0.781，网页搜索约 0.591–0.631，网页抓取约 0.620–0.665；`0.55` 能将这些样例的有效工具与无匹配情况分开。“地址查询”等简短泛化需求也可能被过滤，需改为更具体的查询；这些结果不代表已验证任意目录和任意需求。

## 召回、重排和过滤

关键词匹配名称、端点描述、供应商名称与描述、端点检索补充说明。语义文本只包含端点名称、补充说明与端点描述；端点描述为空时回退供应商描述，避免供应商的其他能力污染该端点。端点 ID、供应商标识不加入语义文本。文本保留前 3000 个 UTF-8 字节，确保截断不破坏字符。

关键词和向量每路最多召回 50 个候选，按 RRF 合并。精确名称匹配在召回阶段优先；最终重排仍以能力相关性为准。配置 Rerank 时不使用旧的余弦阈值过滤向量候选，最多选 `SEARCH_RERANK_CANDIDATES` 个（默认 30，范围 1–100）进行重排。未配置 Rerank 时保留原混合排序，使用 `SEARCH_MIN_SCORE`（默认 0.75）。

Rerank 的任务指令强调工具是否直接具备所需能力，而非仅主题相近。最终按重排分数排序，剔除低于 `SEARCH_RERANK_MIN_SCORE` 的候选，允许返回空列表，不强制凑满结果数。分数相同时按 `tool_ref` 排序，再进行分页。

**`0.55` 是通过当前 New API 与小规模真实查询验证的起点，不是通用阈值或正确概率。** 百炼文档说明该分数用于请求内相对排序，不能把同一个分数当作跨查询的正确性保证。需用当前目录的真实查询验证“地图不返回天气”“不存在的能力返回空”等案例，根据误召回与漏召回调整；更换模型或任务指令后重新验证。接入模型本身不保证消除所有误召回。

单条重排文档最多 3000 字节，查询超过 3000 字节时跳过重排并降级（原搜索入口最多接受 4096 字节）。按查询重复计入每个候选后的总输入预算限制为 100000 字节，必要时减少候选数，低于 `qwen3-rerank` 的 120000 Token 请求上限。语义阶段最多等待 3 秒，重排阶段独立最多等待 5 秒；不自动重试收费模型请求。

重排成功且没有降级的结果在进程内缓存 30 秒，最多 128 个查询；包含查询、目录描述、价格及 Schema 的版本信息。目录变化立即使后续查询不再命中旧缓存，重启清空；并发首次未命中的查询可能各调用一次模型。错误或索引不完整的结果不缓存。

## 索引与元数据

数据库迁移自动创建 `catalog_embeddings`，无需 pgvector。启动后后台批量补建向量，每 30 秒检查目录，只计算新增或能力文本变化的端点。每批最多 10 条，后台请求最多等待 30 秒；搜索请求不重新向量化整个目录。

向量按完整能力文本哈希验证，文本变更期间旧向量不参与检索；未索引端点仍可被关键词召回和重排。模型地址、名称、revision、输入格式、维度和文本处理版本共同隔离缓存；本次升级会自动建立新的索引。更换模型权重、pooling 等未体现在配置中的行为时，修改 `SEARCH_EMBEDDING_REVISION`。旧缓存不会作为新模型向量使用，可在确认不再使用后按 `model_key` 手动清理。

目录和详情返回的 `description` 优先使用端点原始描述，端点描述为空时才回退供应商描述。HTTP 端点可在管理员接口 JSON 中设置 `search_description`；MCP 端点展开后可编辑“检索补充说明”。上游同步不会覆盖本地补充说明。接口管理 API 也支持：

```json
{"revision": 3, "ids": ["endpoint-id"], "search_description": "查找机构公开地址和营业时间，不提供设备定位。"}
```

将其 POST 到 `/api/admin/tools/<id>/bulk`（MCP；正常管理员会话及 CSRF 鉴权）。空字符串清除，单次仅编辑一个端点。补充说明应如实描述能力，不堆砌相关但不具备的功能词。

## 无匹配时推荐网页搜索

管理员可设置 `SEARCH_FALLBACK_TOOL_REF=<当前部署网页搜索端点的完整 tool_ref>`。留空关闭；引用格式错误会在启动时报告配置错误。使用真实目录返回的引用，不根据供应商名称或关键词自动猜测。CLI 的 `tools search --json` 会透传新增字段，无需升级命令用法。

只在非空查询、完整检索结果为空且 `search.degraded=false` 时返回独立的 `suggested_tools`。示例：

```json
{
  "items": [],
  "suggested_tools": [{
    "tool_ref": "<管理员配置的引用>",
    "name": "Web search",
    "price_micros": 1000,
    "currency": "CNY",
    "reason": "no_relevant_tool",
    "message": "未找到匹配工具，可尝试用此工具搜索公开信息；不代表它具备原任务所需能力。调用前查看工具详情，并确认费用授权。"
  }],
  "next_cursor": ""
}
```

推荐项带有工具的当前名称、描述、价格等目录信息，省略 Schema 和调用示例；完整字段以实际响应为准。未触发推荐时 `suggested_tools=[]`。即使只配置关键词搜索也可推荐；已配置模型发生故障或索引未完成时不推荐，避免把故障伪装为无匹配。

推荐不会混入 `items`、影响正常排序或分页，也不会自动调用工具。由 Agent 判断网页搜索能否帮助查找公开信息，先读取详情并遵守费用授权。网页搜索不代表平台已具备地图、设备定位、订票等操作能力。

推荐项每次从当前可用目录检查，独立于排序缓存；供应商停用、端点禁用、缺失或删除后不再推荐。配置了尚不存在的合法引用时也不推荐，可重新指定当前可用端点。修改配置后重启后端。

## 搜索状态与降级

`/v1/catalog` 的 `data.search` 示例：

```json
{"mode":"hybrid","degraded":false,"indexed":12,"total":12,"reranked":true,"candidates":12,"filtered":9}
```

- `mode`：`directory` 空查询目录；`keyword` 关键词或精确 ID 查询；`hybrid` 混合检索。
- `reranked`：是否成功执行重排；`candidates` 为提交重排的候选数，`filtered` 为分数过低剔除的数量。
- `degraded`：已配置的模型服务故障或索引不完整。`reason` 为 `embedding_unavailable`、`index_unavailable`、`index_pending`、`dimension_mismatch` 或 `rerank_unavailable`；同时存在故障时保留先发现的原因。
- `indexed`：实际参与语义比较的有效向量数，不是相关结果数；`total` 为当前可用端点数。

Embedding 不可用时使用关键词候选，仍可进行重排。Rerank 不可用时仅返回关键词结果并标记降级，不恢复未经过相关性检查的语义候选。降级时的空列表不能解释为平台确实没有合适工具。

候选始终只包含已启用供应商下未禁用、未缺失的端点；即使存在查询缓存，停用后也不会继续返回该端点。

空查询按 `tool_ref` 排序并保留原游标。非空查询在重排与过滤后分页，游标绑定查询和候选顺序；必须原样传递。索引、重排或降级改变顺序时返回 400，要求从第一页重新搜索，避免遗漏或重复。

## 兼容本地 E5

继续使用 CPU TEI 服务时显式配置：

```dotenv
SEARCH_EMBEDDING_URL=http://127.0.0.1:7860/v1/embeddings
SEARCH_EMBEDDING_MODEL=intfloat/multilingual-e5-small
SEARCH_EMBEDDING_FORMAT=e5
SEARCH_EMBEDDING_DIMENSIONS=0
SEARCH_RERANK_URL=
SEARCH_MIN_SCORE=0.75
```

Docker 中 `localhost` 指向当前容器，需改用共享网络服务名和容器端口，或仓库 Compose 提供的 `http://host.docker.internal:7860/v1/embeddings`。E5-small 最长 512 token，TEI 应启用超长输入自动截断，且不要重复添加前缀。留空 `SEARCH_EMBEDDING_URL` 则关闭语义搜索，此时也须留空 `SEARCH_RERANK_URL`。

## 验证与诊断

本地模拟测试覆盖两种模型协议、输入格式和维度、天气误召回过滤、无匹配空结果、无效响应、HTTP 错误、取消、重定向、输入预算及精确 ID 绕过模型。数据库集成测试还覆盖索引更新、停用过滤、查询缓存失效和降级恢复。模拟分数只验证流程，不证明真实模型检索质量。

配置环境后，可对真实模型运行小规模固定案例（不修改目录）：

```bash
cd backend
TEST_EMBEDDING_URL="$SEARCH_EMBEDDING_URL" \
TEST_RERANK_URL="$SEARCH_RERANK_URL" \
go test ./cmd/server -run '^TestLiveBailianRetrieval$' -count=1 -v
```

运行前在 shell 导出与服务一致的模型、格式、维度、阈值和密钥。测试目录包含网页搜索、网页抓取、天气端点，验证中文与英文天气查询、搜索与抓取的区分，以及地图路线规划和订机票的无匹配情况；执行会产生少量模型调用费用。

现有数据库的只读诊断：

```bash
TEST_CATALOG_DATABASE_URL="$DATABASE_URL" \
TEST_EMBEDDING_URL="$SEARCH_EMBEDDING_URL" \
TEST_CATALOG_QUERY='地图' \
go test ./cmd/server -run '^TestLiveCatalogSearchDiagnostics$' -count=1 -v
```

诊断输出关键词得分、余弦相似度、召回顺序；配置 Rerank 时还输出候选的重排分数与过滤情况。不修改目录或索引；若新模型索引尚未生成，应先让后端完成后台索引，不能用旧模型向量判断新模型效果。

新模型尚未在业务数据库建好索引时，也可只读验证当前目录：

```bash
TEST_CATALOG_DATABASE_URL="$DATABASE_URL" \
TEST_EMBEDDING_URL="$SEARCH_EMBEDDING_URL" \
TEST_RERANK_URL="$SEARCH_RERANK_URL" \
go test ./cmd/server -run '^TestLiveCatalogRerankScores$' -count=1 -v
```

该诊断会向已配置模型服务发送当前工具能力描述，临时向量仅存于内存，逐项输出真实重排分数和过滤结果，不改写业务索引。
