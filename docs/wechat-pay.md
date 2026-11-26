# 微信扫码充值

第一版使用微信支付 APIv3 普通商户 Native 支付，通过官方 Go SDK 签名、验签和解密。支持 ¥0.01、¥10、¥50、¥100、¥200 等额充值，默认选中 ¥0.01 方便真实付款测试；平台内部仍使用整数微元，1 分 = 10,000 微元。保留管理员人工充值。钱包余额卡的「充值」打开充值方式弹窗：微信支付为当前账户充值；管理员还可选择「管理员直接充值」，指定用户、自定义金额和备注，直接入账并记录操作日志，不依赖微信支付是否开启。原管理端的用户充值按钮继续保留。

## 配置

商户需开通 Native 支付权限，并将 AppID 与商户号绑定。参考[官方接入准备](https://pay.wechatpay.cn/doc/v3/merchant/4015614538)。本实现支持微信支付平台证书模式和[微信支付公钥模式](https://pay.wechatpay.cn/doc/v3/merchant/4012154180)，通过 `WECHAT_PAY_VERIFY_MODE` 显式选择，默认 `public_key` 兼容已有部署。

在部署环境中填写 `.env.example` 的 `WECHAT_PAY_*`：

| 配置 | 内容 |
| --- | --- |
| `WECHAT_PAY_NOTIFY_URL` | 开启微信支付时必填：完整公网 HTTPS 回调地址，可使用独立域名 |
| `WECHAT_PAY_APP_ID` | 已绑定商户号的 AppID |
| `WECHAT_PAY_MCH_ID` | 普通商户号 |
| `WECHAT_PAY_API_V3_KEY` | 商户平台设置的 32 字节 APIv3 密钥 |
| `WECHAT_PAY_SERIAL_NO` | 商户 API 证书序列号 |
| `WECHAT_PAY_KEYS_DIR` | 仅 Compose 使用：宿主机密钥目录，默认 `./secrets/wechat-pay` |
| `WECHAT_PAY_PRIVATE_KEY_PATH` | 商户 API 私钥 `apiclient_key.pem` 的路径；Docker 部署填写容器内路径 |
| `WECHAT_PAY_VERIFY_MODE` | `certificate`（平台证书）或 `public_key`（默认，微信支付公钥） |
| `WECHAT_PAY_PLATFORM_CERT_PATH` | 仅 `certificate` 模式必填：微信支付平台证书 PEM 文件或多证书 PEM 合集路径 |
| `WECHAT_PAY_PUBLIC_KEY_ID` | 仅 `public_key` 模式必填：微信支付公钥 ID，`PUB_KEY_ID_` 开头 |
| `WECHAT_PAY_PUBLIC_KEY_PATH` | 仅 `public_key` 模式必填：从商户平台获取的微信支付公钥 PEM 路径 |
| `WECHAT_PAY_ENABLED` | 参数齐备后设置 `true` |

### 使用 CertificateDownloader 下载的平台证书

[CertificateDownloader](https://github.com/wechatpay-apiv3/CertificateDownloader) 下载的是 X.509 微信支付平台证书（`BEGIN CERTIFICATE`），不是裸公钥（`BEGIN PUBLIC KEY`）。将下载的文件复制到宿主机密钥目录并命名为 `wechatpay_platform.pem`，在 `.env` 中设置：

```env
WECHAT_PAY_VERIFY_MODE=certificate
WECHAT_PAY_PLATFORM_CERT_PATH=/run/secrets/wechat-pay/wechatpay_platform.pem
WECHAT_PAY_PUBLIC_KEY_ID=
WECHAT_PAY_PUBLIC_KEY_PATH=
```

其他商户通用参数仍需填写。程序从平台证书读取验签序列号，无需手动提供。`apiclient_cert.pem` 是商户 API 证书，不能用于此路径；若平台证书与商户证书序列号相同，程序将拒绝启动并提示修正。`WECHAT_PAY_SERIAL_NO` 始终填写商户 API 证书序列号，也就是下载命令的 `-s` 参数，不能换成平台证书序列号。

平台证书模式从本地 PEM 加载，不自动下载或更新证书。证书到期或微信更换签名证书前，需通过官方工具下载新证书，替换文件并重启应用。轮换期间可将新旧证书拼接到同一 PEM 文件，程序按响应或回调头中的 `Wechatpay-Serial` 选择证书。启动时至少需要一张当前有效的 RSA 平台证书；每次验签也会检查该证书的有效期。未知序列号、过期证书或签名错误均拒绝通过，不会跳过验签。平台证书应来自可信的官方下载过程，应用不会自行验证其 CA 信任链。

### 使用微信支付公钥

如果从商户平台「微信支付公钥 → 管理公钥」下载的是公钥，则设置：

```env
WECHAT_PAY_VERIFY_MODE=public_key
WECHAT_PAY_PUBLIC_KEY_ID=PUB_KEY_ID_实际公钥ID
WECHAT_PAY_PUBLIC_KEY_PATH=/run/secrets/wechat-pay/wechatpay_public_key.pem
WECHAT_PAY_PLATFORM_CERT_PATH=
```

选择一种与商户当前验签配置匹配的模式；两组参数无需同时填写。配置错误时会报错，不会自动切换模式。

### 部署与回调

微信支付使用独立的完整回调地址，不会从 `PUBLIC_URL` 拼接或回退：

```env
PUBLIC_URL=https://example.com
WECHAT_PAY_NOTIFY_URL=https://payments.example.com/api/payments/wechat/notify
```

`PUBLIC_URL` 继续用于网站地址、来源校验和 Agent 接入链接。`WECHAT_PAY_NOTIFY_URL` 可使用独立域名或网关路径，必须是无账号密码、查询参数和片段的完整 HTTPS URL。应用接收通知的路由仍为 `POST /api/payments/wechat/notify`；使用不同的外部路径时，反向代理需将其改写并转发至该路由。不要求登录，不添加浏览器挑战；签名验证负责认证通知。

升级已有支付部署时，也必须显式填写 `WECHAT_PAY_NOTIFY_URL`，否则开启支付后服务会拒绝启动。未来其他支付渠道分别配置自己的回调地址。

密钥无需发送到聊天、提交到仓库或填入前端。`.env`、`secrets/` 和 PEM 文件均已忽略；Docker 构建上下文也排除密钥目录和 PEM。

Compose 部署将商户私钥与所选模式的验签 PEM 放入宿主机 `WECHAT_PAY_KEYS_DIR` 指向的目录（默认 `./secrets/wechat-pay`），文件名与示例路径对应。确保容器用户 UID 10001 可读取文件，其他用户无须访问。使用下方命令启用密钥只读挂载；通过 Release 附件部署时，先下载对应版本的 `compose.wechat.yaml`（示例为最新版本）：

```sh
curl -fsSLO https://github.com/dyunwei/tvendo/releases/latest/download/compose.wechat.yaml
docker compose -f compose.yaml -f compose.wechat.yaml up -d
```

`compose.wechat.yaml` 将 `WECHAT_PAY_KEYS_DIR` 整个目录只读挂载到容器内固定的 `/run/secrets/wechat-pay`。例如宿主机私钥为 `/opt/tvendo/keys/apiclient_key.pem`，则配置：

```env
WECHAT_PAY_KEYS_DIR=/opt/tvendo/keys
WECHAT_PAY_PRIVATE_KEY_PATH=/run/secrets/wechat-pay/apiclient_key.pem
```

通常只需修改 `WECHAT_PAY_KEYS_DIR`；私钥文件名不变时，`WECHAT_PAY_PRIVATE_KEY_PATH` 保持默认。微信支付公钥与平台证书的 `*_PATH` 同样填写容器内路径。宿主机目录须预先创建，Compose 不会自动创建它。

本地 Go 开发将所选模式的 `*_PATH` 改为本机实际路径，并手动导出 `.env`；应用不会自动读取该文件。支付关闭时无需任何支付密钥；显式开启但缺少或填错配置会阻止服务启动。

## 订单和入账

- 钱包下单接口仅接受已登录浏览器会话和 CSRF 校验。客户端生成 `request_key`；同一账户、同一请求标识只创建一个订单，不同金额复用标识返回冲突。每账户最多 5 个未完成订单。
- 订单先写入 PostgreSQL，再向微信下单，二维码有效期 15 分钟。下单超时仍保留待确认订单，避免将网络错误误判为支付失败。
- 通知必须通过官方 SDK 的签名验证和 AES-GCM 解密，且为 `TRANSACTION.SUCCESS`。业务层校验 AppID、商户号、订单号、Native 类型、CNY 和订单总金额。
- 入账时锁住订单，在同一个数据库事务中写入微信交易号、支付时间、余额和充值流水。订单、微信交易号与流水唯一约束保证重复通知和补查只入账一次。按订单总额入账，兼容微信优惠导致实付金额小于订单总额的情况。
- 后台每 5 秒领取最多 20 个待查订单，同单每 30 秒最多领取一次；成功查询与通知共用入账函数。过期未付订单向微信关单成功后才标记关闭；微信确认订单不存在时，过期再等待 1 分钟后关闭。网络故障继续补查，服务重启后继续处理。
- 页面每 3 秒读取本地订单状态，不由用户轮询直接请求微信。到账后自动更新余额与充值流水，关闭弹窗也继续检查已加载的待付订单。
- 管理端「用户与充值」查看最近 200 条在线订单及微信交易号。钱包显示自己的最近 100 条订单，账户之间无法读取彼此的订单。更换商户配置前需处理完旧商户的未结订单。

## 测试支付与后台日志

钱包「充值」的微信支付默认选择 ¥0.01，这是实际支付，确认成功后给当前账户入账 10,000 微元。其他充值金额仍可选；管理员直接充值不经过微信。

后台日志统一带 `wechat_pay` 前缀，后接 JSON 字段。Compose 部署可查看：

```sh
docker compose -f compose.yaml -f compose.wechat.yaml logs -f app
```

- `order_request_received` / `order_authenticated` / `order_db_begin` / `order_user_lock` / `order_created`：从请求进入、鉴权通过、数据库事务与用户锁到订单落库的处理阶段。`order_request_finished` 记录总耗时；超时或取消会记录当前阶段。若点击后没有 `order_request_received`，先检查浏览器网络面板及前端代理是否将请求转发到当前后端。
- `request` / `response`：微信下单、查单、关单的操作类型、接口路径、订单号、耗时、HTTP 状态、微信 Request-ID；微信返回错误时附错误码和经过脱敏的错误说明。下单额外记录金额、币种、商户号、AppID 和回调 URL。
- `notify_received`：收到回调后立即记录，即使支付关闭或验签失败也能看到通知到达。
- `notify_verified` / `notify_rejected`：通知验签与解密结果；验证通过后才记录其中的订单号、微信交易号及交易状态。
- `settlement_succeeded` / `settlement_duplicate`：实际入账金额及重复通知跳过入账。
- `notify_acknowledged` / `notify_settlement_failed`：成功应答或入账失败；`reconciliation_deferred` 表示后台补查本次未完成、等待后续重试。

前端支付请求最多等待 30 秒；服务端下单入口含鉴权最多等待 25 秒，订单处理阶段最多等待 20 秒。超时后先刷新订单确认结果，微信下单重试沿用同一个请求标识，避免重复创建订单。开发时通过 HTTP 局域网地址访问也支持生成请求标识；生成失败会显示错误并恢复按钮。

不记录原始请求/响应正文、完整请求头、支付二维码链接、签名、私钥或付款人信息；日志中的已配置密钥会替换为 `[REDACTED]`。按 `order_id` 可以关联同一笔充值的各阶段，按 `request_id` 可向微信侧定位请求。

## 验证与边界

自动测试覆盖公钥及平台证书两种模式的响应验签、伪造签名、未知序列号、证书过期和多证书轮换，并使用测试 RSA 密钥生成签名、加密通知，配合隔离 PostgreSQL 验证重复/并发通知、金额篡改、交易号重复、事务回滚、订单隔离、幂等下单及漏通知补查。测试不调用真实微信商户接口。

上线前仍需使用真实商户完成一次付款验收：确认二维码可用、支付通知可达、到账金额正确、刷新与重启不会重复入账、过期订单能关闭。只有真实验收才能确认商户权限、AppID 绑定和公网回调链路。

管理员可在「用户与充值 → 在线充值订单」申请原单全额退款。系统先冻结等额可用余额，后台使用同一个微信商户退款单号发起和补查。微信确认成功后写入退款流水，确认关闭后释放余额，处理中或异常时维持冻结。余额不足、未支付及非当前商户订单不能申请退款。当前支持每笔订单一次全额退款。微信侧显示退款异常时，应按该退款单号在商户平台处理，系统继续补查状态。

系统在北京时间每天 11:00 后自动核对前一日微信账单。管理员也可在「微信账单对账」选择近 90 天内的已结束日期，点击「筛选」查看当天报告，再点击「手动对账」或「重新对账」。系统比较当前 AppID 和商户号下的订单、成功退款和资金流水，记录缺失、重复、金额或交易号差异，供管理员核实。当服务停机错过账单日时可手动补查。外部商户平台退款可能造成差异，报告不会自动调整余额。

暂不提供 H5 / JSAPI 同机支付、充值赠送和部分退款。工具调用失败的余额退回属于调用结算。

[文档索引](README.md)

## 开发环境 SSH 中转回调

SSH 反向转发传输原始 TCP 数据，不会把 HTTP 转成 HTTPS。开发后端为 HTTP 时，建议公网 Nginx 在 443 端口终止 HTTPS，再转发到仅监听公网服务器回环地址的 SSH 端口：

```sh
ssh -N -T -R 127.0.0.1:19216:127.0.0.1:8080 \
  -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
  zwtec@你的公网服务器 -p 22020
```

在已有 HTTPS 虚拟主机的 `server` 中添加：

```nginx
location = /api/payments/wechat/notify {
    proxy_pass http://127.0.0.1:19216;
    proxy_set_header Host $host;
}
```

设置 `WECHAT_PAY_NOTIFY_URL=https://你的域名/api/payments/wechat/notify`，使用已有有效 TLS 证书。此转发直接连接本机 Go 后端 8080，无需经过 Vite 5173。公网非默认端口也必须实际提供 HTTPS，不能仅将 HTTP 地址前缀改为 `https://`。

下单响应验签使用本机直连微信的链路，和上述回调中转是两个阶段。下单 HTTP 200 但 `ok=false` 时，先查看 `validation_reason`、`wechatpay_serial` 与启动时 `verifier_ready` 的证书列表；`clock_skew_seconds` 超过正负 300 秒时检查本机系统时间。不要通过关闭验签排障。
