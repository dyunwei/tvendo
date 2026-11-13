# 本地开发与测试

需要 Go（最低版本见 [go.mod](../backend/go.mod)，CI 使用 Go 1.26）、Node.js 24、PostgreSQL 17 和 Redis 7。

先从仓库根目录执行 `cp .env.example .env`，修改开发凭据，然后用 `make deps-up` 启动隔离开发数据库和 Redis。

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

## 常用命令

```sh
make cli-build       # backend/bin/tvendo，版本读取根目录 VERSION
make cli-release     # 六平台 CLI、npm 包与校验清单
node scripts/smoke-cli.mjs
node --test scripts/release.test.mjs
```

本机后端默认从 `../distribution/dist` 读取 CLI 产物，也可设置 `CLI_DIST`。直接 `go run` / `go build` 的版本为 `dev`；Makefile 与发布构建会注入 VERSION。

## 验证与 CI

GitHub Actions 配置位于 `.github/workflows/ci.yml`，推送到 `main`、提交 Pull Request 或手动触发时运行：

- Node.js 24 前端类型检查与生产构建。
- Go 1.26 静态分析及 race 测试；自动启动独立的 PostgreSQL 17 / Redis 7，执行单元与集成测试。
- Docker 多阶段镜像构建验证。

CI 使用临时测试凭据，不需要配置仓库 Secrets；数据库与 Redis 随任务结束销毁。

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

CLI 包由 `.github/workflows/cli.yml` 验证。发布工作流会重新测试 Release 标签对应的源码，并复用已经验证的 CLI 产物。正式发布见 [维护与发布](maintaining.md)，实际验证范围见 [验证记录](verification.md)。

[文档索引](README.md)
