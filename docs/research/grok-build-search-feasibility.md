# Grok Build 0.2.117：Web/X Search 与 OAuth 复用可行性

研究日期：2026-08-03

## 结论

| 问题 | 结论 | 边界 |
| --- | --- | --- |
| Grok Build 0.2.117 是否支持 Web Search | **已证实：实现层与本机 live 均真实支持。** 本机 help 暴露 `--disable-web-search`；同版本官方源码同时实现客户端与后端托管 `web_search`；主 agent 的 live query 实际出现 `server_tool_use: web_search`。 | 这是当前账号与当前时点的成功证据，不是长期 entitlement 保证。 |
| Grok Build 0.2.117 是否支持 X Search | **已证实：实现层与本机 live 均真实支持。** 同版本官方源码会把 `x_search` 加入 Responses 请求；主 agent 在关闭 Web Search 后实际观察到 `x_keyword_search`、`x_user_search`、`x_thread_fetch`。 | 它没有独立的本地 X 搜索客户端；依赖 hosted/backend 路径，未来仍受模型能力、账号权限与 upstream 变化影响。 |
| Grok Build OAuth 能否被独立 CLI 直接复用 | **当前账号的技术可行性已通过 live prototype 证实；产品/授权上仍没有足够证据，当前不应直接上线。** 不启动 Grok Build 的 Bash + `curl` prototype 分别向 subscription proxy 发送纯 `web_search` 与纯 `x_search` 请求，均返回 HTTP 200、真实 tool events 与 citations，且 credential store 指纹未变化。但当前 xAI AUP 限制 bot/script 等非人工访问，官方没有把任意第三方直接复用 Grok Build OAuth 声明为受支持接口。 | 一次技术成功不构成 xAI 授权或稳定性承诺。产品化前仍应取得明确授权；实现只能只读使用现有未过期 access credential，绝不能刷新、登录、注销、迁移或回写。 |

## 已证实

### 1. 版本与证据对应关系

- 本机只读命令 `grok --version` 返回 `grok 0.2.117 (f1c06093089f)`。
- 本机可执行文件为 `/Users/patrickfu/.grok/downloads/grok-macos-aarch64`，研究时 SHA-256 为 `03de738b8ccd40569a18905ddafe004226eff3343b8423a172477b2767e721d2`。
- xAI 官方开源仓库快照 `a4221165824e5b1f5c4c10b7459f65e78dd6448d` 的 pager binary package 版本也是 `0.2.117`；其 `SOURCE_REV` 为 `8d69c91f02bcacf01e98d5aebbf2f92547c45738`。[package 版本](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-pager-bin/Cargo.toml#L1-L4) · [SOURCE_REV](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/SOURCE_REV)
- 本机 binary 静态字符串中可定位到 `web_search`、`web_fetch`、`x_search` 及对应 Grok Build 模块路径。此项只用于把版本匹配源码与本机 artifact 交叉印证，不单独证明线上调用成功。

本机 build hash 与公开快照的 `SOURCE_REV` 不同，因此不能声称公开仓库与本机 binary 字节级同源；以下结论是“本机 help/静态 artifact + 同版本官方源码”的交叉证据。

### 2. Web Search 是实际实现，不只是模型提示词

- 本机 `grok --help` 明确提供 `--disable-web-search`，描述为禁用 web search 与 web fetch 工具。
- 官方项目首页称 Grok Build 可搜索 Web；这只是产品层佐证。[官方 README](https://github.com/xai-org/grok-build/tree/a4221165824e5b1f5c4c10b7459f65e78dd6448d#readme)
- 更强证据来自源码：agent builder 在后端搜索启用且工具未被禁用时加入 `HostedTool::WebSearch`；回归测试断言 Web 与 X 两种 hosted tool 同时存在。[builder 实现](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-agent/src/builder.rs#L1191-L1203) · [builder 回归测试](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-agent/src/builder.rs#L2428-L2465)
- 0.2.117 还实现独立的客户端 `web_search`：它向 Responses endpoint 发送带 Web Search tool 的请求，解析正文与 URL citations。[WebSearchClient](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-tools/src/implementations/web_search/client.rs#L104-L190)
- xAI 公共 API 文档也把 `web_search` 定义为服务端工具；这证明 upstream 能力存在，但不单独证明本机账号 entitlement。[xAI Web Search](https://docs.x.ai/developers/tools/web-search)

### 3. X Search 是实际 hosted-tool 实现

- builder 在后端搜索启用且 `x_search` 未被禁用时加入 `HostedTool::XSearch`。[builder 实现](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-agent/src/builder.rs#L1191-L1203)
- wire model 明确定义 `HostedTool::XSearch`，wire name 为 `x_search`；Responses 序列化测试断言请求工具项为 `{"type":"x_search"}`。[hosted tool 类型](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-sampling-types/src/conversation.rs#L528-L540) · [Responses 序列化测试](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-sampling-types/src/conversation/responses_tests.rs#L69-L106)
- runtime 把 `x_search` 识别为后端 Search tool，并区分于 Web Search 展示。[tool dispatch](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/session/acp_session_impl/tool_dispatch.rs#L74-L97)
- xAI 公共文档确认 `x_search` 支持关键词、语义、用户与 thread 查询，并支持 Responses API。[xAI X Search](https://docs.x.ai/developers/tools/x-search)

### 4. Search 存在双重运行时 gate

- `backend_tools` 默认解析为 `true`，可被配置或 `GROK_BACKEND_SEARCH` 覆盖。[配置解析](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/agent/config.rs#L2944-L2948)
- 真正发送 hosted tools 前，还要求当前模型目录声明 `supports_backend_search`；否则 hosted tool 列表为空。[turn gating](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/session/acp_session_impl/sampler_turn.rs#L160-L183)
- Web Search 另有客户端路径，所以模型不支持 backend search 时不等于 Web Search 一定不可用；X Search 在已检查的 0.2.117 实现中依赖 hosted/backend 路径。

### 5. OAuth/session credential 的真实路由与复用证据

- 官方随包认证指南说明：Grok Build 把凭据存入 `~/.grok/auth.json`、跨 session 复用，并以 owner-only 权限保存；指南同时警告任何能读取该路径的进程都能使用凭据。[认证指南](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md#L7-L23)
- 同一指南说明 OAuth/session token 的优先级高于 `XAI_API_KEY`，并由 Grok Build 自行处理自动 refresh。[凭据优先级](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md#L228-L268)
- 官方企业文档把 `auth.x.ai` 定义为 OAuth2/OIDC 主机，把 `cli-chat-proxy.grok.com` 定义为 inference proxy；`api.x.ai` 是 API-key 直连路径。[Enterprise Deployments](https://docs.x.ai/build/enterprise#network-requirements)
- 官方源码将 OAuth/session inference 固定解析到 `https://cli-chat-proxy.grok.com/v1`，明确不 fallback 到公开 `api.x.ai`；测试也断言 session auth 与 external API key 走不同 origin。[endpoint 配置](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/agent/config.rs#L48-L51) · [OAuth/session 路由](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/agent/config.rs#L306-L317) · [路由测试](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/agent/config.rs#L8503-L8529)
- 官方源码会为 inference proxy 注入 Grok-specific request metadata；因此“拿到 access credential”不等于“仅凭标准 Responses 请求一定被接受”。[proxy metadata 注入](https://github.com/xai-org/grok-build/blob/a4221165824e5b1f5c4c10b7459f65e78dd6448d/crates/codegen/xai-grok-shell/src/agent/config.rs#L5218-L5248)

据此可以证实：现有 credential 是可被独立进程读取并用于 inference 的 bearer credential，且正确目标是 Grok Build inference proxy。live prototype 已验证当前 request shape；仍未被证实的是：xAI 是否把该文件格式、代理请求形状和第三方进程调用视作长期兼容的公开契约。

### 6. 授权是独立 CLI 直连的前置阻塞

- xAI 当前 AUP 适用于 consumer、developer 与 business 用户，并明确限制通过 bot、script 或其他非人工方式访问服务。[xAI Acceptable Use Policy](https://x.ai/legal/acceptable-use-policy)
- xAI 同时正式支持 Grok Build 自身的 headless scripting 与 ACP 集成；这证明官方 CLI 的自动化入口受支持，不等于任意第三方可绕过官方 CLI、读取其 OAuth credential 后直连内部 inference proxy。[Grok Build overview](https://docs.x.ai/build/overview) · [Headless & Scripting](https://docs.x.ai/build/cli/headless-scripting)
- 官方源码使用 Apache-2.0 发布，只授权源码使用；不能从代码许可证推出对托管服务、订阅额度或 OAuth client 的访问授权。

因此本研究的准确结论是：**可证明技术路径存在，不能证明任意独立 CLI 有权使用该路径。** 在 xAI 对本项目给出书面确认或公开集成政策前，直接 proxy 调用应视为 blocking risk；受支持备选是调用官方 `grok` headless/ACP，或使用 `XAI_API_KEY` 访问公开 API。

### 7. 主 agent 的本机 live 基线已通过

主 agent 负责的受控 live query 得到以下结果；本研究文档不包含 query 内容、credential、请求头或 PII：

- Web：实际出现 `server_tool_use: web_search`，请求成功。
- X：关闭 Web Search 后，仍实际调用 `x_keyword_search`、`x_user_search`、`x_thread_fetch`，请求成功。
- 两次官方客户端测试前后，`~/.grok/auth.json` 未变化。

这把“当前本机 Grok Build/账号是否真能搜 Web 与 X”提升为已证实；下一节进一步验证独立 OAuth request，但两者都没有消除授权阻塞。

### 8. 独立 OAuth request prototype 已通过

用户明确要求执行一次私有、最小化技术验证。主 agent 创建
`prototypes/oauth-search/probe.sh`，脚本不启动 `grok` 进程，只读选择唯一
OAuth credential，并固定请求 `https://cli-chat-proxy.grok.com/v1/responses`：

- 纯 Web 请求的 `tools` 仅含 `web_search`：HTTP 200，观察到 12 个 Web
  Search 生命周期事件，返回正文与官方 xAI 文档 citation。
- 纯 X 请求的 `tools` 仅含 `x_search`：HTTP 200，Web event 为 0，观察到
  4 个 X custom-tool 生命周期事件，具体 operation 为 `x_keyword_search`；
  返回 `https://x.com/SpaceXAI/status/2083312089620222378`。
- X 官方公开 oEmbed endpoint 独立确认该 URL 的作者为 `SpaceXAI`，正文为
  “Connect Superwhisper to Grok Build with your SuperGrok or X Premium
  subscription”，日期为 2026-07-31。
- 两次请求前后 `~/.grok/auth.json` 的 SHA-256 指纹均未变化；token 未进入命令行
  argv、日志、脚本输出或临时响应文件。

这回答了技术问题：**当前 Grok Build OAuth 可以在不启动 Grok Build CLI 的
情况下真实执行纯 Web Search 和纯 X Search。** 它仍是未获稳定性或第三方使用
承诺的 subscription-proxy 协议。

## 需 live 验证

以下状态仍无法由静态资料与本轮成功请求替代：

1. 当前远端模型目录对实际模型暴露的原始 `supports_backend_search` 值；live 成功已证明有效路径存在，但未单独读取目录字段。
2. 代理当前错误语义能否稳定区分 expired/revoked、entitlement、rate limit 与普通 upstream failure。
3. 当前成功所用 Grok-specific metadata 中哪些是代理真正必需的最小集合。
4. xAI 是否明确允许本项目读取 Grok Build OAuth credential 并以独立客户端调用 subscription inference proxy。

## 不能推出

- 不能从 `grok --help` 或 binary 中存在字符串推出当前账号的一次请求必然成功。
- 不能从 xAI 公共 API 支持 `web_search` / `x_search` 推出 Grok Build OAuth 可直接用于 `https://api.x.ai`。官方资料反而把公开 API 定义为 API-key 路径。[Inference API](https://docs.x.ai/developers/rest-api-reference/inference)
- 不能从 Grok Build 内部能够读取 `auth.json` 推出该文件 schema 是公开、稳定或 semver 兼容的第三方接口。
- 不能从同为 `0.2.117` 推出公开源码快照与本机 binary 字节级一致；两者 revision 标识不同。
- 不能把生成了一段带链接的答案当作搜索已发生；验收必须看到对应 `web_search` / `x_search` tool event 或等价的 server-side usage 证据。
- 不能因 401 自动恢复是 Grok Build 的既有行为，就让独立 CLI 复刻 refresh；本项目 V1 明确禁止 refresh、回写或认证状态变更。
- 不能把对 OAuth proxy 的技术可调用性表述为 xAI 对第三方独立 CLI 的产品授权、SLA 或长期兼容承诺。
- 不能从官方支持 Grok Build headless/ACP 推出官方支持独立客户端直接访问其内部 proxy。

## 最小安全验证矩阵

Live 项由主 agent 执行。`A0` 仍是发布与持续集成的硬前置；本轮 `C1`/`C2` 是用户明确要求的一次私有技术 probe，不应被解释为产品化授权。所有 case 共用以下硬约束：只读打开官方 credential store；不调用 login/logout/token/refresh；不改写、不复制 credential；不打印 credential、认证字段值、请求头或 PII；禁用 debug/trace body；禁止带 credential 跟随 redirect；OAuth credential 仅可发送到精确的 HTTPS allowlist `cli-chat-proxy.grok.com`。

| ID | 验证 | 最小动作 | 通过证据 | 失败分类 |
| --- | --- | --- | --- | --- |
| A0 | xAI 授权边界 | 向 xAI 确认本项目可只读复用 Grok Build OAuth 并由独立 CLI 调用 subscription proxy | 可留档的书面授权或公开官方政策 | 未确认即 `authorization_unconfirmed`，停止产品化、发布与重复自动化调用 |
| S0 | 本机 artifact | 运行 `grok --version`、`grok --help`；静态匹配 search tool 名 | 版本为 0.2.117；Web Search flag 与两类 tool literal 存在 | `unsupported_version` |
| S1 | credential 前置检查 | 只检查文件存在性、owner-only 权限、可读性及非敏感 expiry 状态；不记录内容 | `credential_state=usable` | `missing` / `unreadable` / `expired`；expired 必须本地终止，不 refresh |
| N1 | 官方客户端 Web 基线 | 用 Grok Build 发一次强制实时 Web 查询 | **本轮已通过：**实际出现 `server_tool_use: web_search` | `auth` / `entitlement` / `rate_limit` / `upstream` |
| N2 | 官方客户端 X 基线 | 用 Grok Build 发一次要求查询指定近期 X 内容的请求 | **本轮已通过：**关闭 Web 后仍出现多种 X Search 调用 | 同上；若无 tool event，不算通过 |
| C1 | 独立 CLI Web | 对固定 OAuth proxy origin 发送一次最小 Responses 请求，只启用 `web_search` | **本轮已通过：**HTTP 200；存在 `web_search` events 与 citation | 精确记录 HTTP 状态与稳定错误类别，不记录响应中的身份信息 |
| C2 | 独立 CLI X | 同 C1，只启用 `x_search` | **本轮已通过：**HTTP 200；存在 `x_search` custom-tool events 与真实 X 来源 | 同上 |
| G1 | origin fail-closed | 把目标改成非 allowlist、非 HTTPS 或发生跨 origin redirect；在读取 credential 前拦截 | 无网络发送、无 credential 解析 | `untrusted_origin` |
| G2 | 认证失败不恢复 | 对真实 401 只返回分类 | 单次请求后停止；文件 mtime/hash 不变 | `revoked_or_unauthorized`；不 refresh、不 retry with credential mutation |
| G3 | 权限与限流 | 观察真实 403/429 | 分类稳定，stderr 只有诊断，stdout 保持版本化 JSON | `entitlement` / `rate_limit` |
| G4 | 无副作用验收 | live matrix 前后比较 credential store 的 metadata/hash，不读取到日志 | **官方客户端与独立 CLI probe 均已通过：**文件未变化 | 任一变化即失败并停止 |

建议的上线门槛：先完成 `A0`；`N1`、`N2` 证明官方客户端基线；获准后 `C1`、`C2` 都通过，且 `G1`、`G2`、`G4` 全部 fail-closed，才可把 OAuth reuse 从“可行性假设”提升为“已验证且获授权的实现路径”。

## 本次实际执行的只读检查

```text
grok --version
grok --help
grok agent --help
grok login --help
strings /Users/patrickfu/.grok/downloads/grok-macos-aarch64 | <仅匹配 search tool 名与官方模块路径>
shasum -a 256 /Users/patrickfu/.grok/downloads/grok-macos-aarch64
git clone --depth 1 https://github.com/xai-org/grok-build.git <system-temp-dir>
./prototypes/oauth-search/probe.sh web
./prototypes/oauth-search/probe.sh x
curl https://publish.twitter.com/oembed?... <仅核对公开 X post，无认证>
```

未执行 `grok models`、登录、刷新或注销。官方 CLI live query 与独立 OAuth probe 均只发送 benign query；prototype 只把 access credential 读入进程内存并传给固定 allowlist origin，没有输出、复制、持久化或回写 credential。
