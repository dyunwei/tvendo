# 部署说明

## 使用发布镜像

镜像：`dyunwei/tvendo`，目标平台为 Linux amd64 / arm64。正式版本使用 `:0.1.0` 等固定标签；`:latest` 跟随最近发布的正式版，预发布版本不更新 latest。首版产物需等发布工作流成功后才可拉取。

```sh
mkdir tvendo && cd tvendo
curl -fsSLO https://github.com/dyunwei/tvendo/releases/latest/download/compose.yaml
curl -fsSL https://github.com/dyunwei/tvendo/releases/latest/download/env.example -o .env
```

附件来自最新正式版，配置模板中的 `TVENDO_VERSION` 已固定为该 Release 的版本。需要旧版本时，将 `releases/latest/download/` 换为 `releases/download/v0.1.0/` 等具体标签路径。

默认 Compose 通过 `docker.1ms.run` 镜像源拉取应用、PostgreSQL 和 Redis；官方发布仍推送 Docker Hub。需要直连 Docker Hub 时，将三个 image 分别改为 `dyunwei/tvendo:${TVENDO_VERSION:-latest}`、`postgres:17-alpine` 和 `redis:7-alpine`。

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
| `TRUSTED_PROXY_CIDRS` | 可选，逗号分隔的可信反向代理 CIDR |
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

通过代理提供服务时，将应用实际连接到的代理地址加到 `TRUSTED_PROXY_CIDRS`，例如同一宿主机代理可配置为 `127.0.0.1/32,::1/128`。代理需覆盖或正确追加 `X-Forwarded-For`，并限制客户端直连应用端口。未配置时只使用直连来源，代理后的登录和注册会共用代理地址的限流额度。仅将确实由本部署控制的代理加入名单。

## 升级

在线充值可选启用微信 Native 扫码支付，商户参数、密钥挂载和回调配置见 [微信支付](wechat-pay.md)。默认关闭，保留人工充值。

平台统一使用人民币（CNY），金额为整数微元（1 元 = 1,000,000 微元）。首版发布前的美元标识已移除；经确认现有数据均为测试数据，本次保留余额、售价、预算和流水的原整数值，不执行汇率换算或金额重写。

1. 备份数据库与原 `ENCRYPTION_KEY`，阅读目标版本 [更新日志](../CHANGELOG.md)。
2. 在 `.env` 更新 `TVENDO_VERSION`，保留原数据库密码、加密密钥与数据卷。
3. 执行 `docker compose pull app`，再执行 `docker compose up -d app`。
4. 检查日志、`/api/health`、登录与工具目录。

数据库结构由启动事务自动升级，不需要清库。需要回退时按目标版本的兼容说明操作；数据库变更后不保证旧程序能直接使用新结构，必要时恢复配套备份。

## 邮箱验证与密码重置

配置 `SMTP_ADDR`（如 `smtp.example.com:587`）、`SMTP_FROM`（完整发件邮箱），需要登录时同时填写 `SMTP_USERNAME` 和 `SMTP_PASSWORD`。端口 465 使用隐式 TLS，其他端口要求 STARTTLS。邮件链接使用 `PUBLIC_URL`，生产环境需配置 HTTPS。配置后，新注册用户先验证邮箱，再登录。已有账户沿用当前状态，可使用邮箱找回密码。未配置 SMTP 时仍可注册和登录，登录后也可修改密码。

修改或重置密码会注销此前所有浏览器会话。管理员可停用普通账户，此时会话失效、API Key 暂停使用。恢复账户后未过期且未撤销的密钥恢复使用。需要轮换密钥时，可在控制台撤销并新建。

## 指标与告警

用 `openssl rand -hex 32` 生成独立的 `METRICS_TOKEN`，重启服务后用 Prometheus 抓取 `/metrics`。保持监控网络和令牌受控，并通过 HTTPS 跨网络传输。将令牌写入监控端可读的文件，加载 [告警规则](../ops/alerts.yml)，将告警通过现有 Alertmanager 路由发送给值班人员。

```yaml
rule_files: ["/etc/prometheus/tvendo-alerts.yml"]
scrape_configs:
  - job_name: tvendo
    scheme: https
    authorization:
      type: Bearer
      credentials_file: /run/secrets/tvendo_metrics_token
    static_configs:
      - targets: ["tools.example.com"]
```

调用次数与延迟分布按成功、失败、结果未知和结算错误聚合。数据库指标报告待核实调用、过期未确认的微信订单、异常或停滞的退款，以及昨日对账结果。在多实例部署中，调用计数按实例求和，数据库状态各实例读取同一份数据，应按部署去重。

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
