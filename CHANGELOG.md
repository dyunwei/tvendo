# 更新日志

公开版本从 0.1.0 开始；发布状态以 [GitHub Releases](https://github.com/dyunwei/tvendo/releases) 为准。

## 0.1.0

首个公开版本。

- 自托管控制台、邮箱密码注册登录、平台 API 密钥与管理员操作日志。
- HTTP / JSON 和远程 MCP 工具接入，MCP 工具同步、上下架与固定单次定价。
- 工具目录、中文文本搜索、参数详情与同步执行 API。
- 人民币（CNY）计价，金额精度为微元；余额预留、成功结算、失败退款、单次和每日预算。
- 中文 Agent Skill、Go CLI、六平台二进制、npm 与自托管安装入口。
- Docker Compose 部署；GitHub Release 自动发布 Docker Hub、npm 和 CLI 下载附件。

首次部署需要 PostgreSQL、Redis、管理员初始化配置和稳定的 ENCRYPTION_KEY。上游凭据由部署管理员提供；当前为人工充值，不含在线支付与异步任务。
