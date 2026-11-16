# 工具目录混合搜索

`tvendo tools search` 在名称、端点描述、供应商描述和端点检索补充说明中搜索。配置 embedding 服务后，关键词结果与跨语言语义结果使用 RRF 合并；精确名称、端点 ID 和 tool_ref 命中优先。每路最多召回 50 个候选。候选只包含已启用供应商下未禁用、未缺失的端点。相似度不代表实际能力，调用前仍需查看详情、Schema 和价格。

## 接入已有 CPU embedding 服务

支持 OpenAI 兼容的 `POST /v1/embeddings`，请求使用批量 `input` 字符串数组，响应需要 `data[].index` 和 `data[].embedding`。

本地运行 Go 后端时，配置：

```dotenv
SEARCH_EMBEDDING_URL=http://127.0.0.1:7860/v1/embeddings
SEARCH_EMBEDDING_MODEL=intfloat/multilingual-e5-small
SEARCH_EMBEDDING_REVISION=1
SEARCH_MIN_SCORE=0.75
```

Docker 中运行 Tvendo 时，`localhost` 指向 Tvendo 自己的容器。应改成共享 Docker 网络中 TEI 的服务名和容器端口，或宿主机的可达地址。例如宿主机 7860 映射到 TEI 容器 80 时，共享网络使用 `http://tei:80/v1/embeddings`。不要把宿主机映射端口误作容器端口。随仓库提供的 Compose 已添加 `host.docker.internal:host-gateway`，可直接配置 `http://host.docker.internal:7860/v1/embeddings` 访问同一宿主机上的服务；模型服务需要监听容器可达地址。

`SEARCH_EMBEDDING_API_KEY` 可选，只发给配置的模型服务。此服务是管理员配置的可信内部服务，可以使用私有网络；不会接收到平台密钥或工具上游凭据。服务不跟随重定向。

此版本按 E5 编码规则添加 `query: ` / `passage: ` 并归一化向量，不要在模型服务中重复配置默认前缀。配置其他模型前需确认编码兼容性。E5-small 最长 512 token，TEI 应启用超长输入自动截断；把端点核心能力写在描述前部。TEI 的截断选项见 [官方 CLI 文档](https://huggingface.co/docs/text-embeddings-inference/cli_arguments)。

重启后端后生效。留空 `SEARCH_EMBEDDING_URL` 则仅使用关键词检索。

## 索引与元数据

数据库迁移自动创建 `catalog_embeddings`，无需 pgvector。启动后后台批量补建向量，每 30 秒检查已启用的目录，只计算新增或文本变化的端点。向量持久化到 PostgreSQL，搜索请求不会计算整个目录。每批最多 16 条，后台请求最多等待 30 秒；查询整体语义阶段最多等待 3 秒。

HTTP 端点可以在管理员接口 JSON 中设置 `search_description`；MCP 端点展开后可编辑“检索补充说明”。原始 `description` 保留，上游同步不会覆盖本地补充说明。接口管理 API 也支持：

```json
{"revision": 3, "ids": ["endpoint-id"], "search_description": "查找机构公开地址和营业时间，不提供设备定位。"}
```

将其 POST 到 `/api/admin/tools/<id>/bulk`（MCP；正常管理员会话及 CSRF 鉴权）。空字符串清除补充说明，单次仅编辑一个端点。

索引文本包含端点名称、补充说明、端点原始描述、供应商名称、标识、分类和描述。按完整文本哈希验证索引，文本变更期间旧向量不会参与检索；未索引端点仍可被关键词找到。更换模型、权重、pooling、截断规则时修改 `SEARCH_EMBEDDING_REVISION`，新旧模型索引隔离。停用或移除端点立即从候选排除；缓存中残留的旧向量不参与结果。旧模型缓存可在确认不再使用后按 `model_key` 手动清理。

## 搜索状态与分页

`/v1/catalog` 的 `data` 新增 `search`：

```json
{"mode":"hybrid","degraded":false,"indexed":12,"total":12}
```

- `mode`：`directory` 空查询目录；`keyword` 关键词；`hybrid` 混合检索。
- `degraded`：已配置语义搜索，但服务故障或索引不完整。
- `reason`：`embedding_unavailable`、`index_unavailable`、`index_pending`、`dimension_mismatch`。
- `indexed`：本次语义检索使用的有效向量数；关键词/目录模式为 0。`total` 是当前可用端点数。

阈值 `SEARCH_MIN_SCORE=0.75` 只是 E5 的初始值，不是正确性保证，也不是概率。需根据真实查询调优，过高容易漏掉工具，过低会返回无关候选。

空查询保留按 tool_ref 排序和原来的游标。非空查询按相关性排序，使用新的不透明游标并绑定规范化查询和候选顺序；必须原样传递。当索引更新或降级导致候选顺序变化时返回 400，要求从第一页重新搜索，避免跳过结果或重复结果。

## 验收

准备真实需求，标注可接受的端点及不可接受的替代工具。重点测 Recall@5、无合适工具时的误召回、热启动延迟和并发；分别对比纯关键词与混合搜索。

必须覆盖中文匹配英文描述、精确名称、同供应商不同端点、公开地址与设备定位的区别、索引更新、重启、模型断网，以及未启用工具不可见。仓库测试中的模拟向量验证流程；可通过 `TEST_EMBEDDING_URL=http://127.0.0.1:7860/v1/embeddings` 运行 `go test ./cmd/server -run TestLive -v` 验证实际服务的前缀、批量接口和基本跨语言效果。

实际模型测试使用固定的示例工具目录，不代表已验证部署中的完整目录。`TestLiveToolDiscovery` 覆盖“位置查询”“地图”“地址查询”等需求的前五名召回，其中网页搜索端点带有明确的能力补充说明。
