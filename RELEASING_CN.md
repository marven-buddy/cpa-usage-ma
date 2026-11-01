# cpa-usage-ma 发布准备

准备版本为 `0.4.1`。当前不创建 tag 或 GitHub Release。仓库为 `marven-buddy/cpa-usage-ma`，插件 ID、配置键、路由、默认数据目录、浏览器存储与安装包前缀均为 `cpa-usage-ma`，作为独立插件安装。原版 `usage-report` 不自动迁移。

## 发布前检查

1. 确认 `VERSION`、前端 package/lock、Go 默认开发版本、registry、CHANGELOG 和对应 `docs/releases/v{VERSION}.md` 一致。
2. 在 `frontend` 运行 `npm ci`、`npm test`、`npm run build`；同步 `frontend/dist` 到 `plugin/web`，清除旧 hash 资源并更新 `plugin/main.go` 资源注册/处理与测试路径。
3. 根目录运行 `node scripts/verify-release.mjs`；在 `plugin` 运行 `go test ./...` 和 `go vet ./...`。Windows 本地可用 `./scripts/dev.ps1 -Action Test`。
4. 在目标 CPA v8 / Linux amd64 备份数据库、停止宿主，安装候选动态库，只保留一份同 ID 库，重启并验证加载、鉴权、采集、备注、时区筛选和重启后历史保留。此 Linux 实机验收尚未完成。
5. 审阅 README 与版本说明中的已知限制。`.dev/`、数据库、管理密钥、日志、生成的 `.h` 不进入 Release。
6. 合并开发分支到 `main` 并等待 CI 全部通过。发布 workflow 会拒绝版本号不符或不在 `main` 历史上的 tag。

Windows 本地生成 Linux amd64 候选安装包（需要 DEVELOPMENT_CN.md 中的便携 Go / Zig）：

```powershell
./scripts/build-linux.ps1 -Release
```

输出为 `dist/release/0.4.1/linux-amd64/` 下的 ZIP 和针对 ZIP 的 `checksums.txt`。ZIP 内只有版本化 `.so`、LICENSE 和第三方声明，注入版本为 `0.4.1`。默认不加 `-Release` 仍生成 `0.4.1-dev` 开发动态库。该交叉构建以 glibc 2.31 为基线；它与 CI 原生构建的字节和校验和不同，不可混用校验文件。

## 正式发布（本次不执行）

确认上述检查完成后，在已经同步的 `main` 上执行：

```sh
git switch main
git pull --ff-only origin main
git tag v0.4.1
git push origin v0.4.1
```

推送 tag 会执行发布验证、五个平台构建、打包、ZIP 校验和生成及 GitHub Release 发布。版本说明来自 `docs/releases/v0.4.1.md`。CI Linux amd64 使用 Ubuntu 22.04 / glibc 2.35，arm64 使用 Ubuntu 24.04 / glibc 2.39；其他系统需自行构建。实际构建结果以 Actions 和产物为准。

发布后检查 Release 包含五个 ZIP 和 `checksums.txt`，下载目标包验证 SHA-256；在 CPA 添加以下第三方源后测试安装：

```text
https://raw.githubusercontent.com/marven-buddy/cpa-usage-ma/main/plugin-store/registry.json
```

商店从仓库最新的正式 GitHub Release 下载匹配平台的 ZIP 并验证校验和。仅修改 registry、推送源码或创建草稿不能替代正式 Release。原版源提供 `usage-report`，两者 ID 不同，可并存；不要指向同一个 SQLite 文件。

回滚时停止 CPA、替换为上一版本动态库再启动；保留备份数据库，不删除现有数据。历史记录不自动反匿名化或重写。
