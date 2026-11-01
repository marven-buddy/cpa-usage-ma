# cpa-usage-ma

**本地优先，轻量运行。** A local-first, lightweight **CLIProxyAPI plugin** for token usage analytics.

- **本地优先**：用量数据保存在本机 SQLite，不上传第三方分析平台；插件仅随你的 CLIProxyAPI 运行。
- **实时采集**：通过 `usage.handle` 插件回调，请求完成的瞬间写入数据库——无需轮询。写入错误处理仍有待完善，不能作为无损审计日志。
- **零额外进程**：原生动态库（`.dylib` / `.so`）运行在 CPA 进程内，无需 Python、Node.js、Docker 或独立服务。
- 真实 shadcn/ui 界面：Token 趋势、模型 / 匿名 Key / 请求明细与筛选。

**版本：v0.4.1 / CLIProxyAPI v8。MIT 开源。**

基于 [andyWang1688/cpa-usage](https://github.com/andyWang1688/cpa-usage) 二次开发，作为独立插件发布，插件 ID 为 `cpa-usage-ma`，默认数据库为 `~/.cli-proxy-api/cpa-usage-ma/usage.sqlite`，页面和浏览器配置使用独立命名空间。不会自动读取或迁移原插件数据；如需保留历史，请停止相关宿主后将旧 SQLite 完整备份复制到新目录，再配置新插件。已在 CPA v8.0.48 / Windows amd64 验证加载；Linux amd64 产物已交叉构建，仍需在目标服务器完成加载验收。

## 安装

### 插件商店

以下插件源从本仓库的正式 GitHub Release 安装对应平台包，并验证 `checksums.txt`。新插件可以与原版 `usage-report` 并存；两者应配置不同数据库，避免同时写入同一文件。也可在管理面板设置中添加下方第三方源，无需重复修改源配置。

1. 在 `cliproxyapi.conf` 中添加插件商店源与插件配置：

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/marven-buddy/cpa-usage-ma/main/plugin-store/registry.json"
  configs:
    cpa-usage-ma:
      enabled: true
      db_path: "~/.cli-proxy-api/cpa-usage-ma/usage.sqlite"
```

2. 重启 CLIProxyAPI，打开管理面板 → **插件商店** → 找到 **cpa-usage-ma** → 安装。
3. 打开 **插件 → cpa-usage-ma**（或访问 `/v0/resource/plugins/cpa-usage-ma/report`）。页面会自动复用同源后台已保存的管理凭据（兼容 v1/v2 格式）；未保存后台密码时，可直接在插件右上角的 **管理密钥** 中输入 **CPA 管理密钥**，无需勾选后台的“记住密码”。输入的密钥验证成功后仅保存在当前标签会话；可随时清除，不影响后台登录。浏览器禁用会话存储时仅在当前页面内存保留，刷新需重新输入。

> CPA 访问 GitHub API 查插件信息有匿名频率限制；若遇到 rate limit，可为 CPA 进程配置 `GITHUB_TOKEN` 环境变量，并在 `plugins.store-auth` 添加 `type: github-token`、`token-env: GITHUB_TOKEN` 的规则。

### 手动安装（离线）

从 [Releases](https://github.com/marven-buddy/cpa-usage-ma/releases) 下载对应平台的 `cpa-usage-ma_{version}_{goos}_{goarch}.zip`，解压后放入插件目录：

```bash
unzip cpa-usage-ma_0.4.1_linux_amd64.zip
mkdir -p <plugins-dir>/linux/amd64
cp cpa-usage-ma-v0.4.1.so <plugins-dir>/linux/amd64/
# 目录内仅保留一份 cpa-usage-ma 动态库，然后重启 CPA
```

或从源码构建：

```sh
cd plugin
CGO_ENABLED=1 go build -buildmode=c-shared -ldflags "-X main.version=$(cat ../VERSION)" -o cpa-usage-ma.so .  # Linux amd64
```

## 使用

- 页面：`/v0/resource/plugins/cpa-usage-ma/report`
- 数据 API（需认证）：`/v0/management/plugins/cpa-usage-ma/api/usage?start=YYYY-MM-DD&end=YYYY-MM-DD&bucket=day|hour|month`，请求头 `Authorization: Bearer <管理密钥>`
- 新页面使用 `start_at`、`end_at`（含时区的 RFC3339 时间）与 `timezone`（浏览器 IANA 时区，例如 `Asia/Shanghai`）；范围为包含开始、不包含结束，三者必须一起传入，不能和旧 `start`/`end` 混用。旧日期参数继续按服务器本地时区解释。
- 任何经过该 CPA 实例的请求（Codex CLI、OpenCode、其他客户端）都会自动记录。

界面能力：

- 今天、近 7/30 天与自定义日期范围；模型筛选同时作用于 KPI、趋势和明细。
- 日期和趋势统一使用浏览器时区，页脚显示统计时区；查询使用当地零点对应的 UTC 边界，夏令时切换当天也会完整统计。
- 缓存输入 / 净输入 / 输出堆叠趋势，小时 / 天 / 月粒度。
- 模型、匿名 Key、请求三个视角；搜索、排序与分页。
- 在“按 Key”视图点击铅笔填写备注；留空保存可清除。备注保存在 SQLite，刷新后保留，可搜索，并同步显示在请求明细中。
- 已采集请求参与成功率统计，包括零 Token 失败；输入按协议归一化，独立推理 Token 的限制见下文。
- 错误、空数据与采集异常明确显示，不把空白或旧数据当实时成功。
- 预估费用卡片、Token / 费用趋势切换；模型、匿名 Key、请求明细显示费用并支持费用排序。
- 价格表弹窗可查看已调用模型的公开单价、更新价格、设置自定义价格；未定价与部分费用明确标注。
- 支持 USD / CNY 切换，默认汇率 1 USD = 7.20 CNY，可手动修改并保存在当前浏览器；费用、趋势和价格表同步换算。人民币编辑单价会换算为美元保存，历史费用按当前显示汇率换算，不采用历史结算汇率。

### 费用口径

费用为 **USD 标准文本 Token 估算**，不代表订阅支出或供应商实际账单。计算公式为：

```text
费用 = (普通输入 × 输入价 + 缓存读取 × 读取价 + 缓存写入 × 写入价 + 输出 × 输出价) / 1,000,000
普通输入 = 归一化输入 - 缓存读取 - 缓存写入
```

- 单价单位为 USD / 百万 Token。提供方已把推理计入输出时不重复计费；Gemini / Antigravity 单列的推理 Token 目前可能漏计，费用会偏低。失败请求按实际返回的 Token 计算，零 Token 请求费用为 0。
- 内置 [LiteLLM 公开价格快照](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json)，仅匹配明确的模型名或提供方前缀，不猜测模型别名。页面使用期间每 24 小时检查更新，也可手动更新；失败保留本地价格并显示错误。只向固定价格源请求公开数据，不发送本地用量或 Key。
- 价格更新遵循运行进程的 `HTTPS_PROXY`、`HTTP_PROXY`、`NO_PROXY` 环境变量；需要代理访问 GitHub 时，在启动服务或 CPA 前设置这些变量。
- 支持价格源中的长上下文阶梯；使用标准服务档位与默认缓存写入单价。现有采集没有缓存 TTL、服务档位、工具用量等信息，无法还原 1 小时缓存、Batch / Priority / Flex、图片 / 音频 / 视频 / 工具调用等额外费用。
- 自定义价格按“模型 + 提供方”保存，覆盖该组合的公开价格；恢复公开价格会创建新版本。自定义为固定单价，不保留公开阶梯。缓存单价留空表示未知，0 表示免费。
- 每次价格变动保存不可变版本，已计价请求按当时版本计算。首次接入前或当时缺价的历史请求使用首个完整可用价格，并标注“历史估算”；后续改价不会改变这些已计价历史费用。历史用量原始记录不修改。
- 任一实际使用的 Token 类别缺价，该请求显示“未定价”；总额只累计完整计价的请求，并显示覆盖数量与“部分费用”。全缺价的趋势桶保留空值。

## 配置

| 字段 | 说明 |
|---|---|
| `plugins.configs.cpa-usage-ma.enabled` | 是否启用插件 |
| `plugins.configs.cpa-usage-ma.db_path` | SQLite 路径，默认 `~/.cli-proxy-api/cpa-usage-ma/usage.sqlite` |
| `plugins.store-sources` | 插件商店 registry 地址（安装/更新用） |

## 数据与安全

- 用量事件保存在 `db_path` 指向的本地 SQLite（表 `usage_events`）。新记录按客户端 APIKey 与鉴权来源生成稳定的完整 SHA-256 标识，调用不同模型或轮换上游账号不会拆分同一客户端；不同客户端共用上游账号也不会合并。
- 页面显示短标签 `key-xxxxxxxxxx`，内部使用完整标识分组；缺少 APIKey 时显示“未知客户端”，不会回退使用上游 Source。上游 AuthID/Source 单独生成匿名来源标识，新记录不保存这些字段或客户端密钥的原文。
- 升级前记录保留原样，标为“历史来源 · 客户端未知”，与新客户端统计分开；仍计入模型、Token 和费用汇总。历史记录可能包含密钥或账号来源原文，不要将数据库公开上传。
- 用量 API 中 `key` 是完整分组标识，`key_label` 用于显示，`key_kind` 为 `client`、`unknown` 或 `legacy_source`；`upstream_id` 为独立上游标识。`key_note` 为手动备注，仅修改显示，不改变统计归属；相同备注不会合并不同 Key。未添加原 Key 匹配或脱敏尾号功能。
- 插件的资源页面无独立鉴权，仅在 CPA 管理域内可达；请勿将 CPA 管理端口暴露到公网。

## 已知限制

- 用量写入错误的传播尚未完善，磁盘满等场景可能漏记，不能承诺无损采集。
- Gemini / Antigravity 的独立推理 Token 可能漏计；费用仅供估算。
- 配置仍使用简化 YAML 解析；建议使用示例的多行形式，避免流式写法及值尾部注释。
- 查询会扫描历史记录，大库可能变慢；请求时间倒序及价格种子恢复仍有待改进。
- CI Linux amd64 包使用 Ubuntu 22.04（glibc 2.35），arm64 包使用 Ubuntu 24.04（glibc 2.39）；不支持 Alpine/musl。Windows 本地交叉构建脚本可生成 glibc 2.31 基线的 amd64 包。

## 开发

```sh
cd plugin
GOPROXY=https://goproxy.cn,direct go test ./...       # 单元测试
go build -buildmode=c-shared -o cpa-usage-ma.dylib .  # macOS；Linux 为 .so
```

页面资源位于 `plugin/web/`，来自本仓库 `frontend/` 的构建产物：

```sh
cd frontend && npm ci && npm run build
cp dist/index.html ../plugin/web/index.html
cp dist/assets/* ../plugin/web/assets/
```

> 替换前端后需同步更新 `plugin/main.go` 中 embed 的文件名与 `management.register` 的资源路由（构建产物带 hash 文件名）。

### 发布新版本

发布前检查、版本同步、安装包校验与正式发布命令见 [RELEASING_CN.md](RELEASING_CN.md)。推送 `v{VERSION}` 标签会触发五个平台构建和 Release 发布。

## License

[MIT](LICENSE)。界面组件基于 [shadcn/ui](https://github.com/shadcn-ui/ui)（MIT），见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
