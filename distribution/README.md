# Tvendo CLI

让 Agent 通过 Tvendo 搜索和调用工具。CLI 使用 Go 编写，npm 包自带 Linux、macOS、Windows 的 amd64 / arm64 程序，无需 Go 或本机编译。

## 安装

```sh
npm install -g @dyunwei/tvendo
# 或不做全局安装
npx --yes @dyunwei/tvendo --help
```

需要 Node.js 18+。npm 是启动器，安装和启动时不执行额外下载脚本。没有 Node.js 时，可从 [GitHub Releases](https://github.com/dyunwei/tvendo/releases) 下载对应平台程序并使用 SHA256SUMS 校验；也可使用 Tvendo 部署提供的安装脚本。

## 配置与调用

在本机设置 `TVENDO_SERVER=https://<服务地址>` 和 `TVENDO_API_KEY`，密钥在该部署的控制台创建。

```sh
tvendo doctor --json
tvendo tools search '网页搜索' --json
tvendo tools show '<tool_ref>' --json
tvendo run '<tool_ref>' --input-file input.json --max-cost-micros 1000 --output result.json --json
tvendo runs list --json
tvendo wallet balance --json
```

`setup --server <url> --key-stdin` 支持从标准输入保存密钥。`TVENDO_CONFIG` 可指定本地配置文件。切换服务地址时请为新服务配置密钥。

金额单位为整数微元：1000000 = 1 CNY。调用同步执行，不自动重试；超时或响应丢失时，使用输出的请求标识运行 `runs list --request-id <id>` 核对。`--output` 不覆盖现有文件。调用记录用于核账，不恢复历史结果正文。

全部命令见 `tvendo --help`。详细说明见 [Agent 接入文档](https://github.com/dyunwei/tvendo/blob/main/docs/agent-access.md)，平台源码见 [GitHub](https://github.com/dyunwei/tvendo)。本项目采用 GNU LGPL v3（LGPL-3.0-only），包内附带 LICENSE 及 COPYING.GPL-3。
