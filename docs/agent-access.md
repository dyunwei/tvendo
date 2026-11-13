# Agent 接入

## 使用中文 Skill

将以下提示发送给 Agent，替换成你的部署地址：

```text
请读取 https://<服务地址>/SKILL.md，帮我配置 Tvendo 工具接入。
```

Agent 会读取实际部署提供的说明，检查 CLI 与连接配置，再搜索和调用工具。读取文档不等于持久安装 skill；安装位置和授权规则由宿主 Agent 决定。

## 安装 CLI

需要 Node.js 18+：

```sh
npm install -g @dyunwei/tvendo
# 临时运行
npx --yes @dyunwei/tvendo --help
# 固定版本
npx --yes @dyunwei/tvendo@0.1.0 --version
```

公共包在首版 Release 工作流成功后可用。npm 包包含 Linux、macOS、Windows 的 amd64 / arm64 Go 程序，无需 Go 或本机编译，安装与启动不额外下载二进制。

也可以读取部署的 `/v1/info`：`data.cli_available=true` 时，`data.npm_url` 是该部署自带的实际安装包地址：

```sh
npm install -g '<npm_url>'
npx --yes --package '<npm_url>' tvendo --help
```

没有 Node.js 时，下载 `data.install_url` 指向的脚本并检查后运行；默认安装到 `~/.local/bin`，无需 sudo，支持 `--dir <目录>` 和 `--version <版本>`。脚本校验 SHA-256。Windows 可从 `/cli/manifest.json` 获取 exe，或从 [GitHub Releases](https://github.com/dyunwei/tvendo/releases) 下载对应文件，再对照 SHA256SUMS 校验。不要绕过 HTTPS 证书验证。

## 服务地址与密钥

平台密钥在目标部署 `/app/keys` 创建，按该账户余额计费。在本机配置环境变量：

```sh
export TVENDO_SERVER='https://<服务地址>'
# 在本机设置 TVENDO_API_KEY 后验证，不必把密钥发到聊天中
tvendo doctor --json
```

`tvendo setup --server <url> --key-stdin` 从标准输入接收并保存密钥；CLI 不进入交互问答。配置文件位于系统用户配置目录下 `tvendo/config.json`，也可用 `TVENDO_CONFIG` 指定。Unix 文件权限为 0600；Windows 还取决于用户目录 ACL。环境变量优先，环境变量密钥不会被 setup 自动写入配置文件。

只管理一个活动服务配置。切换服务地址后配置新服务的密钥；CLI 拒绝将已有密钥自动转发到其他服务，也不跟随 HTTP 重定向。

## 命令

| 命令 | 用途 |
| --- | --- |
| `tvendo setup --server <url> [--key-stdin]` | 配置并验证连接 |
| `tvendo doctor` | 检查服务、API 兼容性与鉴权 |
| `tvendo tools search "需求" --limit 10` | 搜索可调用工具 |
| `tvendo tools show <tool_ref>` | 查看 Schema、示例和单次价格 |
| `tvendo run <tool_ref> --input-file input.json` | 同步调用 |
| `tvendo runs list` / `tvendo runs get <id>` | 查询本人的调用记录 |
| `tvendo runs list --request-id <id>` | 响应丢失后关联核对 |
| `tvendo wallet balance` | 查看余额 |
| `tvendo budget show` | 查看预算与 UTC 重置时间 |

全局支持 `--help`、`--version` 和 `--json`。JSON 模式 stdout 仅输出一个对象，诊断写 stderr，不使用 ANSI 颜色；成功为 `{ok:true,data:...}`，失败为 `{ok:false,error:{code,message,...}}`。

## 完成一次调用

```sh
tvendo tools search '网页搜索' --json
tvendo tools show '<搜索返回的tool_ref>' --json
tvendo run '<tool_ref>' --input-file input.json --max-cost-micros 1000 --output result.json --json
```

`tool_ref` 是平台返回的不透明引用，不自行拼接。按详情中的真实 Schema 填写参数；HTTP 工具缺少 Schema 时不能将示例当作完整定义。搜索是名称与描述的文本匹配，可以尝试更具体的中文或英文关键词。

输入来源三选一：`--input '<JSON>'`、`--input-file <文件>`、`--input-file -`（stdin）。输入必须为 JSON 对象。`--output` 保存结果正文，不覆盖已有文件；文件已存在时在收费调用前终止。

金额单位为整数微美元，1000000 = 1 USD。`--max-cost-micros` 在服务端预留余额前检查实际单次价格。Agent 应在用户授权的任务和费用范围内操作，平台账户预算不自动代表用户同意消费。多次调用需自行累计任务费用。

## 失败与退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 其他失败，包括调用成功后的本地保存失败 |
| 2 | 参数或配置无效 |
| 3 | 鉴权失败 |
| 4 | 余额、预算或本次费用上限拦截 |
| 5 | 工具或记录不存在 |
| 6 | 上游失败，预留退款已确认 |
| 7 | 网络、响应或结算状态不明确 |

收费调用前 stderr 输出请求标识。超时、断网、结算未知时，保留该标识并查询记录，不自动重试。请求标识不提供幂等保证；退款也不表示上游动作已经撤销。调用成功但本地保存失败时，JSON 错误保留结果与费用，不能通过重新收费调用来重试保存。

余额不足时到 `/app/wallet` 查看，当前充值由管理员处理。预算超限时到控制台核对设置。服务器不保存可恢复的完整结果正文，记录查询用于核账。

[API 参考](api.md) · [兼容性](compatibility.md) · [文档索引](README.md)
