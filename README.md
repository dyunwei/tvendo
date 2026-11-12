# T vendo

模仿 Monid 页面布局的工具售卖平台 MVP。Go + Vue 3 + PostgreSQL + Redis；前端目录为 **`frontend`**，后端目录为 **`backend`**。

## 当前功能

- 公开页面：Home、Docs、邮箱密码注册 / 登录。主页没有 Tools、Rankings、Blog 导航。
- 用户控制台：开始使用、工具目录与详情、API 密钥、预算、用量、钱包；附资源说明页。
- 管理端：新增 / 修改 / 停用上游工具、配置鉴权密钥、接口与单次售价、查看用户与人工充值、管理员操作日志。
- API：发现工具、转发同步 HTTP / JSON 调用。单次预留余额、成功结算、失败退款，使用数据库行锁防止并发超扣。
- 每日预算与单次预算，精度为整数微美元（1 USD = 1,000,000）；每日预算按 UTC 00:00 重置。
- Redis 保存会话和请求限流；密码 bcrypt，API Key 仅保存 SHA-256 哈希，上游密钥使用 AES-256-GCM 加密。
- 自动初始化数据库结构；首个管理员由首次启动的环境变量创建，后续启动不会覆盖管理员密码。

## 一次性部署

需要 Docker Engine 和 Compose 插件。

```sh
cp .env.example .env
```

编辑 `.env`，至少替换以下配置：

| 配置 | 说明 |
| --- | --- |
| `POSTGRES_PASSWORD` | 数据库密码，建议使用随机十六进制字符串，避免连接串转义问题 |
| `ADMIN_EMAIL` | 首次管理员邮箱 |
| `ADMIN_PASSWORD` | 首次管理员密码，12–72 字节 |
| `ADMIN_NAME` | 管理员显示名 |
| `ENCRYPTION_KEY` | `openssl rand -hex 32` 生成的 64 位十六进制字符串，后续必须保持稳定 |
| `PUBLIC_URL` | 浏览器访问的完整来源，例如 `https://tools.example.com`，不带路径 |
| `COOKIE_SECURE` | 线上 HTTPS 设置为 `true`；本地 HTTP 为 `false` |
| `PORT` | 宿主机端口，默认 8080 |

```sh
docker compose up -d --build
```

打开 `http://localhost:8080`（或配置的域名）。用配置的管理员账户登录，侧栏进入「上游工具配置」。数据库和 Redis 不向宿主机暴露端口。生产环境在应用前配置 HTTPS 反向代理，转发到应用 8080 端口；`PUBLIC_URL` 必须与实际浏览器来源完全一致。

```sh
docker compose logs -f app
docker compose down
```

如果构建环境不能访问默认 Go 模块代理，可在 `.env` 增加 `GOPROXY=https://goproxy.cn,direct` 后重新构建。

`down` 保留卷内数据。备份 PostgreSQL 与 `ENCRYPTION_KEY`；密钥丢失后无法解密已有上游密钥。

## 配置第一个工具

1. 登录管理员，进入「上游工具配置」。内置 **默认停用** 的 TinyFish 示例。
2. 根据你实际使用的上游官方文档，填写 Base URL、Header 名称、密钥前缀及密钥。例如：`Authorization` + `Bearer `（末尾空格），或 `X-API-Key` + 空前缀。
3. 编辑「接口与定价」。每个接口配置唯一 ID、显示名称、HTTP 方法、路径、售价和参数示例。可点击「添加接口模板」生成一项再编辑。
4. 核实地址、参数及定价后启用。用户目录仅显示已启用工具。
5. 在「用户与充值」给注册用户入账，用户即可创建平台 API Key 并调用。

接口 JSON 示例：

```json
[
  {
    "id": "search",
    "name": "Web Search",
    "path": "/search",
    "method": "POST",
    "description": "根据关键词搜索网页",
    "price_micros": 1000,
    "parameters": { "query": "AI agent tools" }
  }
]
```

Base URL 与 path 直接拼接，例如 `https://api.example.com/v1` + `/search`。这不是某个提供商的有效 API 承诺；TinyFish 示例也需要按实际文档核对。项目不附带上游 API 凭证，不会自动激活示例工具。首页展示的生态品牌不代表已配置集成。

资源 JSON 是**说明信息**，例如 `[{"name":"Cloud browser","description":"浏览器会话服务","price":"$0.002 / 分钟"}]`；本版本不提供资源的创建、租赁与生命周期管理。

## 本地开发

启动可连接的 PostgreSQL / Redis，并导出环境变量（Go 进程不会自动加载 `.env`）：

```sh
set -a
. ./.env
set +a
```

根目录的 `.env.example` 里 `DATABASE_URL` / `REDIS_URL` 用于宿主机开发，需与数据库实际地址和密码匹配；Compose 内的应用连接串由 Compose 自动构造。

安装和编译：

```sh
cd frontend
npm ci
npm run build
cd ../backend
go mod download
go build -buildvcs=false -o bin/server ./cmd/server
./bin/server
```

Go 入口在 `backend/cmd/server/`，编译产物在 `backend/bin/`。从 `backend` 目录运行 `./bin/server`，不要切换到 `bin` 目录执行。也可以在仓库根目录用 `make backend-build` 只编译后端。

Go 默认提供 `../frontend/dist` 静态文件，可用 `FRONTEND_DIST` 覆盖。

需要前端热更新时，后端继续运行，设置后端 `PUBLIC_URL=http://localhost:5173`，另一终端运行：

```sh
cd frontend
npm run dev
```

访问 `http://localhost:5173`，Vite 将 API / Skill 请求转发到 `http://localhost:8080`。开发阶段 `COOKIE_SECURE=false`。

## API

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

- 仅同步 HTTP / JSON；60 秒超时，8 MB 响应上限，1 MB 平台请求体上限。
- GET 上游参数编码为查询字符串；POST / PUT / PATCH / DELETE 上游参数作为 JSON 请求体。平台不转发用户提供的 Authorization 到上游。
- 不跟随上游重定向；默认只允许公网 HTTPS 上游，并在每次建立连接时验证解析地址。
- 本地集成测试可设置 `ALLOW_PRIVATE_UPSTREAM=true` 允许 HTTP / 私网，请勿在线上启用。
- 调用成功按配置的固定单次价格结算。上游失败、超时或超出响应限制退回预留款；崩溃遗留的预留款在 5 分钟后由后台恢复退款。上游可能已执行的操作无法撤销，平台不自动重试。
- 每个密钥可以读取该账户可用工具并发起调用，不能访问管理端；没有细粒度权限配置。

## 验证

```sh
cd frontend && npm run build
cd ../backend && go test -buildvcs=false ./...
```

并发扣费等集成测试使用 **专用空数据库**（测试拒绝使用已有 users 表的数据库）和专用 Redis：

```sh
TEST_DATABASE_URL='postgres://.../tvendo_test?sslmode=disable' \
TEST_REDIS_URL='redis://localhost:6379/15' \
go test -buildvcs=false -race -v ./...
```

集成测试包含注册登录、管理员隔离、CSRF、上游密钥加密、充值、预算拦截、并发余额预留、失败退款、账目一致性、密钥撤销、崩溃预留恢复及管理员初始化幂等性。

## 第一版范围

人工充值；没有在线支付、邮件验证 / 找回密码、团队空间、MCP 服务、独立 CLI、异步任务、流式响应、动态路径参数或用量驱动的变动费率。账户密码问题暂由部署方处理。上游接口配置和费率由管理员维护。用量与账目各返回最近 200 条，用户管理最多显示最近 1000 位用户。

页面参考 Monid 的公开页面与用户提供的截图；品牌使用 T vendo，文案与功能根据本项目范围调整。
