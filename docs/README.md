# Tvendo 文档

Tvendo 是让 Agent 发现和调用工具的自托管平台。仓库首页提供最短的部署与接入示例，详细操作按角色拆分如下。

| 文档 | 适合谁 / 内容 |
| --- | --- |
| [部署说明](deployment.md) | 部署者：镜像、Compose、环境变量、HTTPS、升级、备份与恢复 |
| [Agent 接入](agent-access.md) | 用户与 Agent：Skill、npm、二进制安装、配置、命令与故障处理 |
| [上游工具接入](upstreams.md) | 管理员：HTTP / MCP 工具、同步、上下架与定价 |
| [API 参考](api.md) | 开发者：鉴权、工具目录、调用、记录、余额与预算 |
| [本地开发与测试](development.md) | 贡献者：开发环境、源码构建与测试 |
| [维护与发布](maintaining.md) | 维护者：版本、GitHub Release、Docker Hub 与 npm |
| [兼容性](compatibility.md) | 当前支持范围与限制 |
| [更新日志](../CHANGELOG.md) | 公开版本变更 |

实现依据与测试证据：[Agent 接入设计](agent-access-design.md) · [验证记录](verification.md)。设计记录包含尚未实现的扩展，实际使用以操作文档和 CLI 帮助为准。
