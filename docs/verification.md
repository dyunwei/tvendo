# 第一版验证记录

已完成：

- 前端 TypeScript 检查及 Vite 生产构建。
- Go 编译、`go vet`、单元测试。
- 使用独立 PostgreSQL / Redis 运行 `go test -race` 集成测试，包含权限、CSRF、充值、预算、并发扣费、上游失败退款、密钥撤销和异常预留恢复。
- 12 个并发请求在 ¥5.50 余额、每次 ¥1 的条件下成功 5 次，拒绝 7 次；余额和账目一致。
- Chromium 桌面（1440×1000）与手机（390×844）检查：首页、文档、登录、控制台、工具详情 / 调试弹窗、密钥创建撤销、预算保存、管理端配置保存、人工充值和账目查询。
- 手机导航与页面横向溢出检查。
- Compose 配置校验。
- Docker 多阶段镜像构建（当前网络使用 `--build-arg GOPROXY=https://goproxy.cn,direct`），并实际启动镜像验证健康检查、SPA 路由和 Skill 文档。
- 更新 Vue 类型检查器后，`npm audit` 返回 0 条已知漏洞。

账户、余额、充值记录和已启用工具属于隔离浏览器测试数据，不是部署时的默认数据。实际首次启动只初始化管理员和默认停用的 TinyFish 配置示例。

上游转发使用受控 HTTP 测试服务器验证；没有真实 TinyFish 密钥，未做真实 TinyFish 调用。上线前请用实际供应商接口核对参数和价格。

## MCP 批量管理验证

- 官方 Go SDK 对接测试：Streamable HTTP 的 JSON / SSE 响应、旧版 HTTP+SSE、鉴权和跨域消息端点保护。
- 工具分页同步、默认价 / 覆盖价、免费覆盖价、批量状态、参数变化标记、删除 / 重新出现状态的单元测试。
- 隔离 PostgreSQL / Redis 的 race 集成测试：管理员权限、同步失败不修改清单、版本冲突、默认价及覆盖价实际扣费、MCP isError 退款。
- Chromium 使用 65 个工具的本地 MCP 测试服务验证：保存连接、分页拉取、跨页全选、批量改价和启用、单独售价、再次同步保留设置、新增 / 删除 / 参数变化标记、用户目录过滤与手机布局。
- Go 编译 / vet、Vue TypeScript / 生产构建，以及包含 MCP SDK 的 Docker 镜像构建。

为隔离测试数据，未连接真实 TinyFish，也未改动用户现有数据库。

## Agent CLI 与安装分发验证

- Go 静态分析、race 单元与隔离 PostgreSQL / Redis 集成测试通过；保留原有 12 并发请求的余额与账目一致性验证。
- 新 API 验证：工具搜索与详情、费用上限预检不产生调用记录、tool_ref 执行、退款状态、请求标识关联、余额与预算查询、其他账户不可读取调用记录。
- CLI 验证：单对象 JSON 输出、参数与退出码、密钥脱敏及配置权限、切换服务地址不转发旧密钥、拒绝跟随重定向、上游失败不自动重试、结果文件不覆盖、JSON 整数精度保留。
- Linux / macOS / Windows 的 amd64 / arm64 交叉编译通过；本机实际运行 Linux amd64，其他平台未执行运行时验证。
- 实际 npm tarball 的 npx 启动、中文搜索、Unix 安装脚本、SHA-256 清单损坏时拒绝安装、调用结果保存验证通过。测试入口：`node scripts/smoke-cli.mjs <产物目录>`。
- 中文 skill 元数据校验、前端 TypeScript / 生产构建，以及包含六平台二进制和 npm 包的 Docker 镜像构建通过。当前构建网络使用 部署文档支持的 `--build-arg GOPROXY=https://goproxy.cn,direct`。

- 实际启动测试镜像后，通过它的自托管 npm URL 完成 npx 下载、创建临时账户并验证 doctor / 搜索 / 余额 / 记录查询；镜像生成的安装脚本成功下载并安装对应二进制。临时应用、数据库与 Redis 容器均已清理。

上述测试使用受控上游和临时数据库，不代表真实供应商调用或线上部署已经完成；公共 npm 包尚未发布。

## 首版发布与文档重组验证

- 根 VERSION 为 0.1.0，CLI、npm 包、Docker 构建及 Release 标签检查读取同一版本来源；稳定版 / beta 标签与 GitHub Pre-release 标记不符时拒绝发布。
- `@dyunwei/tvendo` 的正式版与隔离目录中的 `0.2.0-beta.1` 包均完成六平台编译、清单验证及安装 smoke test。npm tarball 包含独立中文 README 和 LICENSE；重复构建当前正式版产物的校验值一致。
- npm 离线 dry-run 通过，未上传包；重复发布逻辑用模拟 registry 响应验证：404 才视为新版本，同内容可跳过，不同内容或 registry 失败必须终止。
- actionlint 和 YAML 检查通过；测试了发布及源码 Compose 配置，README / docs 的本地链接可解析。README 截图来自本次隔离测试环境。
- Go vet、race 单元及隔离 PostgreSQL / Redis 全量集成测试、前端 TypeScript / 生产构建通过。
- Docker 源码构建和复用预构建 CLI 产物的发布构建均通过；发布镜像实际提供 scoped npm 下载地址、安装脚本与可鉴权查询的 CLI API。ARM64 服务端交叉编译产物已确认为 aarch64 ELF；未在 ARM64 主机运行完整应用。

未执行 GitHub 上的真实 Release、Docker Hub 推送或 npm 正式发布，也未验证真实账号的发布权限。首次发布前按 [维护指南](maintaining.md) 配置 Docker Secrets 和具有发布权限的 NPM_TOKEN；首次发布成功后可改用 Trusted Publisher。

- npm 鉴权选择测试通过：配置 Token 时只用 Token，无 Token 时保留 OIDC，离线预检不使用发布凭证。此检查不读取或验证 GitHub Secret 的实际值。

## 人民币计价验证

- 平台币种统一为 CNY，界面使用人民币符号；1 元 = 1,000,000 微元。API 的金额字段继续使用原整数精度，返回币种标识，CLI 调用结果保留服务端币种。
- 按用户确认，已有金额均为测试数据，保留余额、售价、预算和账目整数，不执行汇率换算；集成测试验证再次启动不会缩放余额。
- Go vet、race 单元及隔离 PostgreSQL / Redis 全量集成测试通过，包含人民币金额下的余额预留、并发限额、退款、调用记录币种与账目一致性。
- 前端 TypeScript / 生产构建和浏览器验证通过：余额、预算符号、微元售价（¥0.000001）、API 的 CNY / 精度标识；已更新控制台截图。
- 六平台 CLI 构建和实际 npm 包安装 smoke test 通过，验证结果包含 CNY。所有测试使用临时服务与数据，未修改线上数据库。
