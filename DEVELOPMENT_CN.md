# 本地二次开发

Fork：<https://github.com/marven-buddy/cpa-usage-ma>。开发分支：`dev/v8-local`。

发布版本为 0.4.1，独立插件 ID 为 `cpa-usage-ma`。生产默认数据库、页面/API 和浏览器存储均与原版隔离；本机开发脚本继续显式指定 `.dev/data/usage.sqlite` 以保留已有模拟数据，不代表生产安装会自动迁移原插件。发布步骤见 [RELEASING_CN.md](RELEASING_CN.md)。

本机为 Windows amd64，部署目标为 CLIProxyAPI v8 / Linux amd64。Windows 本地环境运行真实 CPA v8 宿主与插件 DLL；Linux 产物独立构建，不能在 Windows 上完成 .so 加载验证。

## 已准备的环境

父目录 `.tools/` 存放便携工具，不修改系统 PATH：

- Go 1.27.3。
- w64devkit 2.11.0 / GCC，用于 Windows CGO。
- npm 11.6.2；Node 使用本机已有的 24.22.1。
- CLIProxyAPI 8.0.48（提交 dbb70621）。
- Zig 0.14.1 用于 Linux 交叉构建，已核对官方 SHA-256。

`frontend/node_modules` 已按 package-lock.json 安装。若后续重新安装，在仓库目录运行：

```powershell
node ../.tools/npm/package/bin/npm-cli.js ci --prefix frontend --no-audit --no-fund
```

## 常用操作

在仓库根目录使用 PowerShell 7：

```powershell
./scripts/dev.ps1 -Action Start
./scripts/dev.ps1 -Action Status
./scripts/dev.ps1 -Action Smoke
./scripts/dev.ps1 -Action Test
./scripts/dev.ps1 -Action Stop
```

Start 编译插件并启动三个隐藏后台进程，仅监听 127.0.0.1：

| 地址 | 用途 |
| --- | --- |
| http://127.0.0.1:15173 | Vite 页面，支持前端热更新 |
| http://127.0.0.1:18317 | 真实 CPA v8 调试宿主 |
| http://127.0.0.1:18318 | 本地固定响应的 OpenAI-compatible 测试上游 |

开发页面通过 Vite 代理调用真实 CPA 插件 API。仅在 Vite DEV 且页面位于 loopback 时，从 VITE_CPA_DEV_KEY 读取本地开发密钥；生产构建自动读取同源管理面板的 v1/v2 凭据，或接受插件内手动输入的管理密钥。

右上角“管理密钥”可验证并切换凭据。手动输入优先于自动凭据，仅在验证成功后写入当前标签的 sessionStorage；清除后重新尝试自动凭据，不会删除主面板登录信息。浏览器禁用会话存储时仅在页面内存保留，并提示刷新需重新输入。无需为了使用插件而强制在主面板勾选“记住密码”。

Smoke 经 CPA 发送客户端 A 的普通请求、SSE 请求和故意失败请求，以及客户端 B 的普通请求；验证四条新 Usage 记录、两个独立客户端分组（3 条 / 1 条）、Token 数及未认证数据访问被拒绝。只使用本地测试上游，不消耗真实提供商额度。

本地运行文件全部位于 `.dev/`，已加入 .gitignore：

- `data/usage.sqlite`：开发数据库，停止/重启保留。
- `plugins/windows/amd64/cpa-usage-ma.dll`：开发插件。
- `config.yaml`、`management-key`：隔离配置与随机开发管理密钥。
- `*.log`、`*.error.log`：服务日志。
- `processes.json`：服务 PID 和启动时间，Stop 仅终止匹配的本任务进程。

修改 Go 代码后先 Stop 再 Start。前端源码保存后 Vite 自动更新。开发不读取用户原有 CPA 凭证目录。

## 前端生产构建

```powershell
node frontend/node_modules/vite/bin/vite.js build frontend
```

生产页面仍由 plugin/web 内的构建资源提供。修改页面后需要同步 dist 产物，并更新插件资源路由中的 hash 文件名；本次没有重做这套上游打包机制。

## Linux amd64 构建

Windows 便携交叉构建：

```powershell
./scripts/build-linux.ps1
# 生成注入正式版本号的候选 ZIP 与校验和（不发布）
./scripts/build-linux.ps1 -Release
```

输出 `dist/linux/amd64/cpa-usage-ma.so`，默认 glibc 2.31 基线。应在目标 Linux 宿主完成真实加载验收；Alpine/musl 需要单独构建，不能混用 glibc 产物。

也可在 Linux amd64 安装 Go 1.27 与 GCC 后原生构建：

```bash
cd plugin
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go build -buildmode=c-shared -o cpa-usage-ma.so .
```

## 本次必要适配

- 兼容 v8 Base64 编码的 config_yaml，同时保留旧明文输入。
- 自动创建数据库父目录；失败重配置返回错误并保留原连接。
- 修复 Windows 测试路径转义和 SQLite 连接清理。
- 增加隔离开发宿主、测试上游、启动/停止与端到端冒烟脚本。

已知产品改进项仍保留：同步写库与写入错误处理、全历史扫描、Gemini 独立推理计数和 YAML 解析。客户端与上游身份已分开记录，管理 JSON 已声明 schema 6 以保留原始字符串。自动读取仍依赖主面板内部格式，但已兼容 v1/v2，读取失败可手动输入；这些改进不等于其他审查问题已经修复。

本地 fork 通过官方 GitHub 归档与 API 恢复原始签名提交和 Git 树并逐项校验哈希，当前是浅检出。Git 网络恢复后可运行 `git fetch --unshallow origin` 补齐历史；origin 指向自己的 fork，upstream 指向 andyWang1688/cpa-usage。

## 本次验证结果（2026-11-01）

- 插件完整 Go 测试通过，前端 10 项测试通过，Vite 生产构建通过。
- CPA v8.0.48 Windows amd64 实际加载新构建的插件并注册成功。
- 普通请求、SSE、失败请求经真实 CPA 路由后正确写入 SQLite；禁用内置 Usage 统计时仍正常采集。
- 未认证统计 API 访问被拒绝；浏览器页面能读取开发数据库，未出现 JavaScript 运行错误。
- 服务停止、重启与历史保留验证通过；生产构建没有包含开发管理密钥或开发认证入口。
- Linux amd64/glibc 交叉构建成功，产物为 `dist/linux/amd64/cpa-usage-ma.so`。尚未在真实 Linux 宿主加载该产物。

## 认证修复验证（2026-11-01）

- 7 项认证回归测试及原有 10 项前端测试全部通过，Go 测试通过。
- v1 使用 host + userAgent 派生混淆键，v2 仅使用 host；两者和旧明文格式均受支持。
- 真实 v8 宿主只读验证：无凭据被拒绝；手动密钥验证、用量查询、同标签恢复、清除，以及 v1/v2 自动凭据均通过；原有开发记录仍为 9 条。
- 浏览器确认生产页面会在缺少凭据时打开输入框；错误密钥显示认证失败且不保存；开发页面正常读取用量。
- 新前端产物已同步 plugin/web 并更新资源注册路径，本地宿主已重启加载；Linux amd64/glibc 2.31 产物已重新构建。

## 客户端归属修复（2026-11-01）

- 新事件标记 `event_version: 2`，`client_id` 为带鉴权来源命名空间的完整 SHA-256；上游身份优先使用 AuthID，否则使用独立的 Source 哈希，不把客户端 Key 的 Source 回退值误认为上游。
- 新记录不保存客户端 APIKey、上游 AuthID 或 Source 原文。历史行及 dedup 不迁移、不猜测归属；查询按历史来源分组并标明客户端未知。
- API 的 `key` 用于完整分组，`key_label` 仅用于显示，`key_kind` 区分客户端、未知客户端和历史来源；按模型、趋势及费用汇总继续包含历史记录。
- 去重纳入完整客户端、provider、model、RequestID、SelectionID 等事件字段，相同事件重放去重，不将共享 RequestID 的不同模型事件合并。
- 新增 4 项 Go 归属回归测试与 2 项前端测试；完整 Go 测试和 19 项前端测试通过，生产构建通过。
- 真实 v8 双客户端冒烟新增 4 条模拟记录，分别归为 3 条 / 1 条；原有 9 条记录逐行校验值未变，目前开发库共 13 条。
- 浏览器验证 Key 视图显示两个客户端组及单独的历史来源组，并支持按显示标签搜索。生产页面和 Linux amd64/glibc 2.31 产物已更新，Linux 实机加载仍待验证。

## Key 备注（2026-11-01）

- “按 Key”视图的铅笔按钮可保存、修改备注，留空保存可清除；备注保存在 SQLite 的 `key_notes` 表，重启后保留。请求明细和搜索同步使用备注，分组仍使用完整匿名标识。
- 备注写入接口受 CPA 管理鉴权保护；限制 80 字符，拒绝控制字符，写入失败返回错误。历史来源和未知客户端可备注，类型与归属不变。
- 管理契约提升至 schema 6，避免 v8 将备注中的 `&`、`<`、引号等变成 HTML 实体；页面通过 React 文本渲染。
- 完整 Go 测试、20 项前端测试和生产构建通过；真实宿主验证未认证写入被拒、特殊字符原样返回、备注恢复后用量仍为 13 条且 Token/费用未变。
- 浏览器验证编辑、保存、刷新保留、搜索和清空；临时测试备注已恢复为空。Windows 宿主已加载新插件，Linux amd64/glibc 2.31 产物已重建，尚未完成 Linux 实机加载验证。

## 日期时区修复（2026-11-01）

- 前端将所选本地日期转换为 `[开始日零点, 结束日次日零点)` 的绝对 UTC 时间，用 `start_at`、`end_at` 和浏览器 IANA `timezone` 查询。按日历计算次日，兼容 23/25 小时的夏令时日期。
- 后端按绝对时间筛选，返回的小时/天/月趋势桶使用请求时区；页脚显示完整统计时区。内嵌 Go 时区数据，部署不依赖额外安装系统 tzdata。
- 新参数缺失、格式错误、反向范围、无效时区或混用旧日期参数返回 400；旧 `start`/`end` 调用继续使用服务器时区和包含结束日的原约定。历史无时区时间戳仍按原服务器时区解析，不改写历史记录。
- 新增 4 项 Go 回归测试和 3 项前端测试：覆盖 UTC/洛杉矶服务器与上海浏览器、零点边界、连续日期、小时/日/月分桶、纽约春秋夏令时、闰日、参数校验和旧 API。完整 Go 测试、23 项前端测试及 `go vet` 通过，生产资源已同步。
- 本地真实 v8 宿主已重启加载，13 条原有用量事件重启前后完全一致；同一绝对范围分别按上海、UTC、纽约查询，事件不变、趋势时区正确，无效时区返回 400。浏览器确认今天/近 7 天切换正常，页脚显示 `Asia/Shanghai`。Linux amd64/glibc 2.31 产物已重建，仍需在 Linux 宿主实机加载验收。
