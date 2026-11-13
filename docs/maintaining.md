# 维护与发布

仓库：`dyunwei/tvendo`。Docker 镜像：`dyunwei/tvendo`。npm 包：`@dyunwei/tvendo`，命令名为 `tvendo`。首次正式版为 `v0.1.0`。

## 版本来源

根目录 [VERSION](../VERSION) 是应用与 CLI 发布版本的唯一来源。构建脚本、Makefile 和 Dockerfile 读取它；Go 通过链接参数注入版本，npm 包与清单由打包脚本生成。前端 package.json 是私有构建项目，不作为产品版本来源。

正式版本格式为 `0.1.0`，预发布为 `0.2.0-beta.1`。Git 标签必须为 `v` 加 VERSION，且 GitHub 的 Pre-release 标记须与后缀一致。发布前更新 VERSION 和 [CHANGELOG](../CHANGELOG.md)，提交代码，等待 CI 通过。

## 一次性配置 Docker Hub

1. 用 `dyunwei` 账号创建公开仓库 `tvendo`。
2. 创建具有该仓库写入权限的 Docker Hub Access Token。
3. 在 GitHub 仓库 Settings → Secrets and variables → Actions 配置：

| Repository secret | 值 |
| --- | --- |
| `DOCKERHUB_USERNAME` | `dyunwei` |
| `DOCKERHUB_TOKEN` | 上一步创建的 Token |

Token 只填到 GitHub Secrets，不写入仓库。发布使用 Docker 官方 Actions 登录和构建，见 [Docker 文档](https://docs.docker.com/build/ci/github-actions/)。

## 一次性配置 npm

发布工作流支持两种方式：配置了 `NPM_TOKEN` 时使用 Token；没有 Token 时使用 OIDC Trusted Publishing。Token 发布失败时不会静默切换到 OIDC。

### 使用 NPM_TOKEN（可直接完成首次发布）

在 GitHub Repository secrets 中添加 `NPM_TOKEN`。工作流仅在发布步骤将它映射为 npm 使用的 `NODE_AUTH_TOKEN`，不会输出 Token，也不会在打包和测试步骤使用它。

Token 应属于 `dyunwei` 账号，未过期，并在 npm 网站的 Packages and scopes 中具有 **Read and write (publish and stage)** 权限，授权范围覆盖 `@dyunwei` scope，包括创建 `@dyunwei/tvendo` 新包。用于无人值守发布时还需启用 **Bypass two-factor authentication**。只有 Read-only 或 stage-only 权限不能直接发布，见 [npm Token 配置说明](https://docs.npmjs.com/creating-and-viewing-access-tokens/)。

使用有效 Token 时，首个 v0.1.0 Release 就会创建并发布正式包，无需手动创建初始化预发布，也无需先绑定 Trusted Publisher。截图中的 Secret 名称只能确认配置项存在，实际账号与权限需由发布结果验证。

### 后续改用 OIDC（可选）

首次 Token 发布成功、包已经存在后，可以绑定 Trusted Publisher，移除 GitHub 的 `NPM_TOKEN`，让后续 Release 使用 OIDC。工作流必须位于公开仓库，并在 GitHub-hosted runner 执行；不需要另外创建初始化包。

### 绑定 GitHub Actions

到 npm 包页面 → Settings → Trusted publishing，添加 GitHub Actions：

| 字段 | 值 |
| --- | --- |
| Organization or user | `dyunwei` |
| Repository | `tvendo` |
| Workflow filename | `release.yml` |
| Environment | 留空（工作流未配置 environment） |
| Allowed actions | 必须允许 `npm publish`；仅允许 stage publish 无法全自动发布 |

新建绑定应在 2 天内完成首次成功发布，过期则删除并重建。完成绑定后，后续 Release 即可使用 OIDC，详见 [npm Trusted Publishing](https://docs.npmjs.com/trusted-publishers/)。工作流赋予 `id-token: write`，使用 Node.js 24 和支持 OIDC 的 npm；包元数据中的 repository 必须保持指向此仓库。

## 发布一个版本

1. 更新 VERSION，并在 CHANGELOG 中写明变更、配置变化及升级注意事项。
2. 验证源码、构建 CLI 并测试安装：

   ```sh
   make test
   make build
   make cli-release
   node --test scripts/release.test.mjs
   node scripts/smoke-cli.mjs
   node scripts/publish-npm.mjs distribution/dist --dry-run
   ```

   完整计费集成测试须使用专用空数据库和 Redis，见 [开发文档](development.md)。dry-run 只检查本地包，不发布、不查询 registry 中的版本。
3. 提交并推送代码，等待 CI / CLI packages 通过。
4. 创建并推送匹配 VERSION 的标签，如 `v0.1.0`。
5. 在 GitHub Releases 新建 Release，选择该标签，填写 CHANGELOG 对应内容；正式版不勾 Pre-release。
6. 点击 Publish release 后查看 `Publish release` 工作流。

只推标签或保存草稿不会发布。工作流检出 Release 标签重新执行版本检查、后端 race 集成测试、前端构建、CLI 六平台构建和安装 smoke test，再并行执行：

- Docker Hub：构建 Linux amd64 / arm64 镜像，复用已测试的 CLI 产物。
- npm：优先用 NPM_TOKEN，未配置时用 OIDC 发布同一份 npm tarball。
- GitHub Release：附加六平台 CLI、npm tarball、SHA256SUMS、manifest.json、CLI-README.md、LICENSE、COPYING.GPL-3，以及部署用的 compose.yaml 和 env.example。

部署附件从该标签的源码生成：compose.yaml 原样保留镜像源等配置，仓库中的 .env.example 仅将 TVENDO_VERSION 固定为本次 VERSION，并以 env.example 附件名上传，其余保持模板内容。不会上传本地 .env。README 使用 `releases/latest/download/` 获取最新正式版的附件，后续发版无需修改下载链接；预发布附件可通过具体标签下载。GitHub 会重命名带前导点的附件，因此附件使用 env.example，下载时保存为 .env，见 [GitHub 附件命名说明](https://docs.github.com/en/rest/releases/assets#upload-a-release-asset)。

Docker 本地构建默认自己生成 CLI。发布构建设置 `CLI_SOURCE=cli-release`，使用下载到 `distribution/dist` 的同一份产物，并在镜像构建中再次验证 VERSION 和 SHA-256。

## 发布标签

| GitHub | Docker Hub | npm |
| --- | --- | --- |
| `v0.1.0` | `dyunwei/tvendo:0.1.0`，更新 latest | `@dyunwei/tvendo@0.1.0`，tag 为 latest |
| `v0.2.0-beta.1` | `dyunwei/tvendo:0.2.0-beta.1`，不更新 latest | `@dyunwei/tvendo@0.2.0-beta.1`，tag 为 next |

根 VERSION、Git 标签与 Pre-release 标记不一致时，在任何公开发布之前终止。工作流串行执行，避免多个 Release 同时更新 latest；不要将旧正式版重新作为最新版发布。

## 失败与重跑

若报错为「Release 标签应为 vX，实际为 vY」，说明发布标签指向的提交中 VERSION 与标签不一致。先修正 VERSION 和 CHANGELOG，提交并推送。仅更新 main 或点击 Re-run 不会改变已有标签对应的源码，因此不能修复此错误。若要保留同一版本号，须确认该版本尚未成功发布产物，再删除失败的 Release 及其标签，基于修正后的提交重新创建标签和 Release；也可以使用新的版本号发布。不要移动已经成功发布版本的标签。

Docker Hub、npm 和 GitHub Release 不支持跨平台原子发布，可能部分成功。检查各 job 后在 Actions 中重跑失败任务；构建 job 的产物保留 30 天。npm 已有同版本且 tarball 的 SHA-512 一致时跳过；不同则停止，不能覆盖版本或把差异静默当作成功。重新运行全部构建时应特别留意工具链变化造成的产物差异。

GitHub 附件使用同名覆盖上传，Docker 失败可重跑相应 job。若 Release 已启用资产不可变策略，发布后可能无法再附加文件；当前流程要求允许 published 后上传附件，与本项目首次发布方式一致。

npm 版本不可覆盖。确需改变已发布内容时增加版本号，重新走发布流程。自动更新 Docker Hub 页面描述暂未配置；项目首页说明维护在 README。

## 维护入口

用户文档放在 [docs 索引](README.md) 对应的主题文件；README 保持简短。内部设计与验证记录不作为最终用户操作步骤。变更行为时同时维护 API、CLI 帮助、中文 Skill 和相关操作文档。项目许可证为 LGPL-3.0-only。打包时保留根 LICENSE、COPYING.GPL-3 与 CLI README。
