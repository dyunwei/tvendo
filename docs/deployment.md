# 部署说明

## 使用发布镜像

镜像：`dyunwei/tvendo`，目标平台为 Linux amd64 / arm64。正式版本使用 `:0.1.0` 等固定标签；`:latest` 跟随最近发布的正式版，预发布版本不更新 latest。首版产物需等发布工作流成功后才可拉取。

```sh
mkdir tvendo && cd tvendo
curl -fsSLO https://raw.githubusercontent.com/dyunwei/tvendo/v0.1.0/compose.yaml
curl -fsSL https://raw.githubusercontent.com/dyunwei/tvendo/v0.1.0/.env.example -o .env
```

编辑 `.env`：

| 配置 | 说明 |
| --- | --- |
| `TVENDO_VERSION` | 发布镜像标签，生产建议固定为 `0.1.0` 等版本 |
| `POSTGRES_PASSWORD` | 随机数据库密码；建议十六进制字符串，避免连接串转义问题 |
| `ADMIN_EMAIL` | 首次管理员邮箱 |
| `ADMIN_PASSWORD` | 首次管理员密码，12–72 字节 |
| `ADMIN_NAME` | 管理员显示名 |
| `ENCRYPTION_KEY` | `openssl rand -hex 32` 生成的 64 位十六进制字符串，后续保持稳定 |
| `PUBLIC_URL` | 浏览器访问的完整来源，例如 `https://tools.example.com`，不带路径 |
| `COOKIE_SECURE` | HTTPS 为 `true`，本机 HTTP 开发为 `false` |
| `PORT` | 宿主机端口，默认 8080 |

```sh
docker compose pull
docker compose up -d
docker compose logs -f app
```

访问 `PUBLIC_URL`，用首次配置的管理员账户登录。首次启动初始化数据库结构和管理员；后续启动不覆盖管理员密码。初始 TinyFish 配置默认停用，核实真实上游后再开放，见 [上游接入](upstreams.md)。

PostgreSQL 与 Redis 不向宿主机暴露端口。数据分别保存在 Compose 的 `postgres_data` 和 `redis_data` 命名卷。`docker compose down` 保留卷；`down -v` 会删除卷，不能作为普通升级步骤。

## HTTPS

在应用前配置 Caddy / Nginx 等反向代理，转发到应用 8080 端口。`PUBLIC_URL` 须与用户浏览器来源完全一致，并设置 `COOKIE_SECURE=true`。证书应被浏览器、npm 和 CLI 正常信任，尤其是通过 IP 地址访问的部署。外部非本机 CLI 服务地址要求 HTTPS。

## 升级

平台统一使用人民币（CNY），金额为整数微元（1 元 = 1,000,000 微元）。首版发布前的美元标识已移除；经确认现有数据均为测试数据，本次保留余额、售价、预算和流水的原整数值，不执行汇率换算或金额重写。

1. 备份数据库与原 `ENCRYPTION_KEY`，阅读目标版本 [更新日志](../CHANGELOG.md)。
2. 在 `.env` 更新 `TVENDO_VERSION`，保留原数据库密码、加密密钥与数据卷。
3. 执行 `docker compose pull app`，再执行 `docker compose up -d app`。
4. 检查日志、`/api/health`、登录与工具目录。

数据库结构由启动事务自动升级，不需要清库。需要回退时按目标版本的兼容说明操作；数据库变更后不保证旧程序能直接使用新结构，必要时恢复配套备份。

## 备份与恢复

将 `.env` 与数据库备份分别妥善保存。原 `ENCRYPTION_KEY` 丢失后无法解密已有上游密钥；只备份数据库不足以恢复这些凭据。

```sh
# 在部署目录执行，备份文件仅当前用户可读
umask 077
docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > tvendo.dump
```

恢复到新部署的空数据库：先还原原数据库密码与 `ENCRYPTION_KEY`，保持应用停止，仅启动 PostgreSQL / Redis，待数据库就绪后恢复：

```sh
docker compose up -d postgres redis
# 等待 postgres 健康；目标数据库必须为空
docker compose exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --exit-on-error' < tvendo.dump
docker compose up -d app
```

Redis 保存会话和限流信息，恢复数据库后可重新登录；不要将其当作余额和账目的权威数据源。

## 从源码部署

在仓库根目录复制并编辑 `.env` 后：

```sh
make source-up
# 等价于：docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

该方式构建本地 `tvendo:local` 镜像，版本读取根目录 VERSION。无法访问默认 Go 模块代理时可设置 `GOPROXY=https://goproxy.cn,direct`。默认发布镜像部署不需要 Go / Node.js；源码开发见 [开发文档](development.md)。

## CLI 分发

镜像包含六平台 CLI、npm 安装包、清单及校验文件。`GET /v1/info` 返回实际安装地址，`/SKILL.md` 引导 Agent 接入。直接运行 Go 服务时须先构建 CLI 产物，或通过 `CLI_DIST` 指向产物目录；没有产物时不会宣称已提供安装包。

[文档索引](README.md)
