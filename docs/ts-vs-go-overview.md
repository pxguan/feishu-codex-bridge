# TS → Go 重写 · 整体对比

> 目的：把 `feishu-codex-bridge` 从 TypeScript（`src/`）向 Go（`internal/` + `cmd/feishu-codex-bridge`）重写后的**整体**模块 / 架构 / 实现度做一次对照。
> cli-bridge 专项差异（markdown 路线、thread 归并、delivery 路由）单独写在 [`ts-vs-go-clibridge.md`](./ts-vs-go-clibridge.md)，本文只做摘要。

---

## 0. 一句话结论

模块布局**几乎 1:1 对齐**，目录改名即可映射（`cli-bridge/`→`clibridge/`、`service/`→`daemon/`、Lark SDK 隔离进 `feishu/`）。**功能上不缺模块**，`web/admin/platform/utils` 在 Go 侧都已实装。两处本质差异：① **进程模型**（TS 多进程 / Go 单进程）；② **bot 编排层的飞书建群 SDK 调用尚未 port**（cli-bridge 的自动建群已接通，不受影响）。

---

## 1. 架构模型差异（最重要的两点）

| 维度 | TypeScript（原版） | Go（重写） |
|---|---|---|
| **进程模型** | 多进程家族：常见一个 `run` 进程拥有整个栈；注册 ≥2 个 bot 时 `bot/supervisor.ts` 为**每个 bot fork 一个子进程**（指数退避重启），父进程聚合 Web 控制台 | **单进程单体服务**：`run` 启动单一进程跑所有活跃 bot；按 appId 维度靠 `core.AcquirePIDLock` 保证单实例（不再有「每 bot 一进程」） |
| **Lark SDK 位置** | 直接 `import { createLarkChannel } from '@larksuiteoapi/node-sdk'`，散落在 `bot/bridge.ts`、`card/managed.ts` | 全部封装进独立 `internal/feishu/`（Channel / message / cardkit / chat），业务层不直接碰 SDK |
| **目录重命名** | `cli-bridge/` | `clibridge/` |
| | `service/`（OS 后台服务） | `daemon/` |
| | `index.ts` + `cli/index.ts` | `cmd/feishu-codex-bridge/main.go` |
| **多后端协议** | codex：`codex app-server` JSON-RPC；claude：`@anthropic-ai/claude-agent-sdk`（持久 query） | codex：同样 JSON-RPC over stdio；claude：`claude --print --output-format stream-json`（一次性进程 + `--resume` 续会话，无原生状态机） |

---

## 2. 模块映射总表

| 职责 | TS（`src/`） | Go（`internal/`） | TS 文件数 | Go 文件数 | 状态 |
|---|---|---|---|---|---|
| 子 agent 对接（claude / codex） | `agent/` | `agent/`（+`claude/`、`codex/`） | ~20 | 52 | ✅ 等价 |
| 飞书 bot 编排 / 项目查找 / session / presence | `bot/` | `bot/` | ~15 | 17 | ✅ 等价（含全部 DM/GS 卡片 handler） |
| 项目 / 群注册表与生命周期 | `project/` | `project/` | ~8 | 较小 | ✅ 等价（建群已接；AddManagers 空壳，见 §3.10） |
| 卡片构造（schema 2.0 / CardKit） | `card/` | `card/` | ~20 | 43 | ✅ 等价 |
| 配置 schema / keystore / 路径 | `config/` | `config/` | ~8 | 13 | ✅ 等价（字节级对齐） |
| OS 后台守护进程 | `service/` | `daemon/` | ~6 | 6 | ✅ 等价（进程模型不同，见 §1） |
| CLI 子命令 | `cli/` | `cli/` + `cmd/feishu-codex-bridge` | ~12 | 11 + 1 | ✅ 等价（cobra vs commander） |
| 本地 HTTP 管理控制台 | `web/` | `web/` | 6 | 2 | ✅ 实装（端口不同） |
| 管理写操作 + IPC | `admin/` | `admin/` | 4 | 2 | ✅ 实装 |
| 进程 spawn 辅助 | `platform/` | `platform/` | 1 | 5 | ✅ 实装（更完整） |
| 工具函数 | `utils/` | `utils/` | 3 | 7 | ✅ 实装 |
| 飞书 OpenAPI wrapper | （散落在 `bot/bridge.ts`、`card/managed.ts`） | `feishu/`（**新增独立包**） | — | 7 | ✅ 新增隔离层 |
| 自更新 | `service/update.ts` | `update/`（**新增独立包**） | 1 | 2 | ✅ 新增隔离层 |
| 本地 agent hook 桥接 | `cli-bridge/` | `clibridge/` | ~12 | ~12 | ✅ 等价（见专项文档） |

> 注：codex 协议类型 TS 用 527 个 vendored 生成类型（`protocol/generated/*`），Go 用精简协议 + `json.RawMessage` 二次解析，不生成 450+ 类型——这是合理的重写取舍，不是缺失。

---

## 3. 逐模块对比

### 3.1 agent（claude / codex）
- **契约一致**：TS `agent/types.ts`（`AgentBackend`/`AgentThread`/`AgentEvent`）↔ Go `agent/types.go`（`AgentBackend`/`AgentThread`/`AgentEvent` 单 struct + `Type` 标签 + 构造函数族）。`AgentEvent` 归一化事件流两边等价。
- **codex**：JSON-RPC 2.0 over newline-delimited JSON，常驻进程 + 预热池（TS `client-pool.ts` ≈ Go `clientpool.go`，冷 ~1.6s → 热 ~64ms）。失败隔离、crash 重启一致。
- **claude**：TS 用持久 `query()` 复用线程；Go 用一次性 `claude --print --output-format stream-json` 进程 + `--resume <sessionId>` 跨进程续会话，并加了内网 GLM 网关 TLS 修复、`--resume` 网关瞬断指数退避重试。
- **权限档**：`qa/write/full` → 沙箱参数，两边都在非 mac/win 平台 **fail-closed 不降级**，一致。

### 3.2 bot（编排 / session / presence）
- **编排**：TS `bot/handle-message.ts`（`createOrchestrator`）↔ Go `bot/orchestrator.go` + `handle_turn.go`。消息去重→P2P→群门禁→命令→分支、`ResolveCwd`/`FindProjectByCwd`、并发信号量、session 持久化、上下文织入（quote/sender/thread-history）、媒体/评论处理——职责一一对应。
- **session store**：TS `bot/session-store.ts`（topic↔session，v1→v2 迁移）↔ Go `bot/session_store.go`（含 `codexThreadId`→`sessionId` 迁移），重启后 resume 正确 thread。
- **presence / 本地活跃检测**：TS 在 `bot` 链路里判定；Go 抽到 `clibridge/presence.go`（`ResolveCliPresenceRoute`），macOS 读 `HIDIdleTime`、失败策略一致。
- **群话题/建群**：bot 侧的飞书建群 SDK 调用**已 port（2026-07-09）**——`DMNewProjectSubmit`→`handleNewProjectSubmit`、`DMJoinGroupSubmit`→`handleJoinGroupSubmit` 均已注册并走 `Channel.(ChatCreator).CreateChat` 建群 + `ProjectStore.Add` 落盘 + 回 DM 成功卡 / 向新群发「🤖 本群使用说明」欢迎卡（card.BuildWelcomeCard，含手册链接）。其余 DM/GS 卡片 handler（设置/免@/自动压缩/默认模型/权限/管理员/白名单/诊断/重连/更新等共 ~35 个）也已全部对齐 TS 实现并注册（见 `internal/bot/handlers_cards.go` + `registerCardHandlers`）。`AddManagers`（提管理员）仍是 no-op。**群公告已实装**（`internal/feishu/announcement.go`：docx 公告块写入 + 置顶到群顶部横幅，对齐 TS `setAnnouncement`），但需飞书 app 开通 `im:chat.announcement:read`/`im:chat.announcement:write_only`（置顶还需 `im:chat.top_notice:write_only`）才生效——当前 app 历史日志确认缺这三个 scope。**onboarding 已全量实装**：created 群额外 Pin 欢迎卡 + 加群 Tab「👈 使用说明」+ 加群菜单「🤖 <后端名>」（internal/feishu/onboarding.go 的 `PinMessage`/`AddChatTab`/`AddChatMenu` + internal/bot/onboarding.go 的 `onboardGroup`，对齐 TS `onboarding.ts`）；Pin/Tab/Menu 分别需 `im:chat:pin`/`im:chat.tabs:write_only`/`im:chat.menu_tree:write_only`，当前 app 缺后两者会 best-effort 失败（仅告警）。cli-bridge 的自动建群不受影响。

### 3.3 card（schema 2.0 / CardKit）
- **完全一致**：`card/managed.ts`（`sendManagedCard`/`updateManagedCard`，CardKit **2.0 实体**，单调 `sequence`，`stampRenderToken` 反 12h 去重）↔ Go `card/managed.go`（`ManagedRegistry`：`SendManagedCard`/`UpdateManagedCard`，200810 点击窗口重试，同款 token 戳）。
- 流式运行卡（`run-card*`、`run_state`）、DM 卡、命令卡、goal/history/usage 卡、回调路由（`CardDispatcher` 按 `value.a`）两边齐备。
- 关键不变量（schema 2.0：按钮卡必须 CardKit 实体、多控件用 `column_set`、`print_frequency_ms` 必须 `{default:N}`）两边一致。

### 3.4 config（schema / keystore / 路径）
- **字节级对齐**：`AppConfig`/`AppPreferences` 结构、全部归一 getter（`maxConcurrentRuns[1,50]` clamp、`runIdleTimeout` 等）、`SecretRef`（`env|file|exec`）、`CliBridgePreferences` 解析——两边一致。
- **keystore**：TS `config/keystore.ts`（AES-256-GCM，PBKDF2-SHA256 100k，seed=hostname|username，末尾 16B 为 tag）↔ Go `config/keystore.go`，**互通**（同一加密格式，密钥可跨语言解密）。
- **paths**：TS 用全局 `currentBotDir` 可变态；Go 改为 `Paths{AppID}` 实例 + 纯函数 `BotXxxFile(appID)`，消除全局态——这是重写时的刻意改进。

### 3.5 daemon（TS `service/`）vs service
- 职责一致：OS 后台服务安装/启停/重启/日志/自更新；按平台分 launchd / systemd / win-startup。
- **进程模型不同**（§1）：TS 每 bot 一进程 + 独立 pid；Go 单 `run` 进程 + 按 appId 单实例锁。`daemon.go` 的 `killTree` 会杀整棵进程树（含 codex/claude 子进程）。

### 3.6 cli
- TS 用 commander（`cli/index.ts` + `cli/commands/*`）；Go 用 cobra（`internal/cli/root.go` + 各命令文件）。子命令一一对应：`run`/`start`/`stop`/`restart`/`status`/`logs`/`update`/`bot init|list|use|rm`/`doctor`/`secrets`/`hook`（隐藏）/ `chats`/`web`。
- Go 多出 `send`（端到端自测：给群发消息并可选跑一轮 codex/claude 回复发回）。

### 3.7 feishu（Go 新增独立 wrapper）
- TS 没有这个包，SDK 调用散落在 `bot/bridge.ts`、`card/managed.ts`。Go 把它隔离成 `internal/feishu/`：`Channel`（WS 长连接 + 事件分发）、`message.go`（SendText/SendMarkdown/SendCardByEntity/ReplyCardByEntity/SendMarkdownInThread…）、`cardkit.go`（CardKit 实体）、`chat.go`（建群/加管理员）。
- 这是重写的核心改进点之一：**业务层不直接依赖 Lark SDK**，便于替换/测试。cli-bridge 的 markdown/thread 差异本质都发生在这层（见专项文档）。

### 3.8 web / admin / platform / utils
- **全部已实装，非空壳**（之前误以为可能未实现，已核实）：
  - `web/`：仅绑 `127.0.0.1` + Bearer/`?token=` 鉴权 + DNS-rebinding 防护；读端点（`/api/state`、`/api/logs`、`/api/sessions`…）+ 写端点（镜像 DM 卡 + Web 专属的 bot 注册/后端安装/daemon 重启）；TS 端口 51847，Go 端口 18789（各自 stable token 落 discovery 文件）。
  - `admin/`：单一写操作真源（`ops.ts`/`ops.go`），DM 卡回调与 Web API 共用同一校验/落盘/驱逐路径；IPC（TS `admin/ipc.ts` supervisor↔child）在 Go 单进程下不需要。
  - `platform/`：进程 spawn / 进程树 kill（POSIX `kill(-pid)` / Windows `taskkill /T /F`），跨平台 `sysProcAttr`，Go 实现更完整（5 文件 vs TS 1 文件）。
  - `utils/`：`ValidateAppCredentials`（换 token + bot 信息 + scope 比对，绝不抛错）、`eventdiagnosis`、`openurl` 两边齐备。

### 3.9 update（Go 新增独立包）
- TS 在 `service/update.ts`；Go 抽到 `internal/update/`：`Latest`（GitHub Releases）、`CompareVersion`（语义版本）、`CurrentPlatformAsset`（按 goos/goarch 匹配）、`DownloadToTemp`（下载到临时文件，**不自动替换运行中二进制**）。

### 3.10 project（⚠️ 关键缺口所在）
- **注册表 / 纯函数 / 持久化**：`Project` 结构、`Store`（原子写 + 注册表级硬守卫防一群双绑）、`EffectiveMode`/`TurnTier`、`ValidateCreateProjectInput`/`ResolveCwd`/`AssertBackendUsable`——均已就绪。
- **建群动作本身已实现**：`feishu/chat.go` 的 `CreateChat`（调 `im.v1.chat.create`）已实现；cli-bridge 自动建群（`internal/cli/clibridge_wire.go:137` 的 `ch.CreateChat`）在用，**已真机验证**（拿到真实 `chatId`）。它仅建群 + 落盘 project，不做后续 onboarding。
- **✅ 对话内建群已全接（2026-07-09）**：
  - `DMNewProjectSubmit` → `handleNewProjectSubmit`：串起 `project` 纯函数（`ValidateCreateProjectInput`/`ResolveCwd`/`AssertBackendUsable`）+ `Channel.(ChatCreator).CreateChat` 建群 + `ProjectStore.Add` 落盘 + 回 DM 成功卡 + 向新群发 `OnboardingText` 欢迎卡。
  - `DMJoinGroupSubmit` → `handleJoinGroupSubmit`：解析表单（name/cwd/chatId/kind/backend）→ `ResolveCwd`/`AssertBackendUsable`/`ProjectStore.Add`（`Origin:"joined"`）+ 回 DM 成功卡 + 欢迎卡；群类型按 backend 选 `qa`/`full`（含 claude 走 `full`）。单测覆盖。
  - 纯函数层与飞书建群动作至此打通。
- **加管理员是空壳**：`feishu/chat.go` 的 `AddManagers` 是 no-op（注释"后续精确探索后补上"），无论哪条路径都不会真正提升管理员。
- **✅ 群公告已实装（2026-07-09 晚）**：`internal/feishu/announcement.go` 实现 `Channel.SetGroupAnnouncement`，对齐 TS `setAnnouncement`——`Docx.V1.ChatAnnouncementBlock.List` 找 page block → `ChatAnnouncementBlockChildren.BatchDelete` 清空 → `Create` 写入一行文本（`📁 name · 📂 cwd · 🌿 branch`，branch 经 `project.CurrentBranch` 懒检测）→ `Im.V1.ChatTopNotice.PutTopNotice`（`action_type=2`）置顶。刚建群公告 doc 未就绪时重试 5 次线性退避；scope 类错误 fast-fail 并打印需开通的权限名。已接到 `handleNewProjectSubmit`/`handleJoinGroupSubmit`，best-effort（失败仅告警、不影响建群）。**前提**：飞书 app 须开通 `im:chat.announcement:read` + `im:chat.announcement:write_only`（`top_notice` 置顶还需 `im:chat.top_notice:write_only`），否则写入会因 `Access denied` 失败（见 §5）。
- **✅ onboarding 已全量实装（2026-07-09 晚）**：向新群发「🤖 本群使用说明」欢迎卡（`card.BuildWelcomeCard`，对齐 TS `buildWelcomeCard`），created 群再 `PinMessage`/`AddChatTab`/`AddChatMenu`（对齐 TS `onboardGroup`）。Pin/Tab/Menu 分别需 `im:chat:pin`/`im:chat.tabs:write_only`/`im:chat.menu_tree:write_only`——当前 app 历史日志确认缺后两者，实测时这两步会 best-effort 失败（仅告警、不阻断建群），欢迎卡本身始终能发。

---

## 4. cli-bridge 关键差异（摘要，详见 [ts-vs-go-clibridge.md](./ts-vs-go-clibridge.md)）

| 项 | TS | Go |
|---|---|---|
| 群话题 markdown 渲染 | `post` 富文本（node-sdk 内部 `markdown→post`） | `interactive` 卡片（`tag:"markdown"` 元素）走 CardKit 实体引用（飞书 `im.message.create` 不支持 `msg_type:markdown`，内联 JSON 卡片被拒 `11310`/`200621`） |
| thread 归并 | `im.message.create` 直接带 `reply_in_thread`（node-sdk 暴露该字段） | Go SDK v3.9.7 的 create body 不暴露该字段 → 改为「首次建根 + `replyMessage(ReplyInThread=true)` 挂回」 |
| delivery 路由 | schema 虽写 `always\|away_only`，但运行时固定 `away_only` | 真实现 `always`（强制路由飞书）与 `away_only`（默认，本地在线走终端）两个值 |
| IPC | Unix socket + 换行 JSON | Unix socket + 换行 JSON（一致） |

---

## 5. 实现完成度评估

**已实现（等价 / 改进）**
- cli-bridge 全链路（hook→socket→daemon→群话题 markdown 卡片 + thread 归并 + delivery 路由），已真机验证。
- agent 双后端（codex JSON-RPC / claude stream-json），归一化 `AgentEvent` 流。
- card 体系（schema 2.0 + CardKit 实体 + 流式运行卡 + 各类 DM/命令卡）。
- config（字节级对齐 schema + 互通 keystore + 无全局态 paths）。
- web / admin / platform / utils（均已实装，非空壳）。
- feishu（新增独立 wrapper）/ update（新增独立包）/ daemon（OS 后台化）。
- cli 全子命令（cobra）。

**缺口 / 待补**
- ✅ **对话内 `/newproject` 建群已补（2026-07-09）**：`DMNewProjectSubmit` → `handleNewProjectSubmit` 已注册并联通 `CreateChat` + 落盘 + 欢迎卡（单测覆盖）。
- ✅ **对话内「加入群」`DMJoinGroupSubmit` 已补（2026-07-09）**：`handleJoinGroupSubmit` 已注册并联通 `CreateChat` + 落盘（`Origin:"joined"`）+ 欢迎卡。
- ⚠️ **飞书「建群后处理」链路其余部分仍未完整 port**：
  - `feishu/chat.go` 的 `AddManagers` 是 **no-op** → 建群后不会真正提升管理员（调用已接、实现待补）。
  - ✅ **群公告已实装（2026-07-09 晚）**：`internal/feishu/announcement.go` 已对齐 TS `setAnnouncement`（docx 块写入 + 置顶），并接到 `handleNewProjectSubmit`/`handleJoinGroupSubmit`（best-effort）。**但当前飞书 app 缺 `im:chat.announcement:read`/`write_only`（置顶还需 `im:chat.top_notice:write_only`）scope**，需先到飞书开放平台给 bot 应用开通这三个权限并重启 daemon，新建项目才会有群公告。
  - ✅ **onboarding 已全量实装（2026-07-09 晚）**：发「🤖 本群使用说明」欢迎卡 + created 群 Pin/Tab/Menu（`internal/feishu/onboarding.go` + `internal/bot/onboarding.go`）。**Pin/Tab/Menu 需 `im:chat:pin`/`im:chat.tabs:write_only`/`im:chat.menu_tree:write_only`**——当前 app 缺后两者，需到开放平台开通并 `launchctl kickstart` 重启 daemon 后才有 Pin/菜单；欢迎卡不受限。
  - 注：cli-bridge 自动建群的「建群动作」本身可用（已验证）。
- ⚠️ **用量 / 更新卡为占位（`not yet integrated`）**：`DMUsage`/`DMUpdate`/`DMUsageShare` 目前弹「尚未接入」卡——Go 侧没有 wham/npm 后端做用量统计与 npm 自更新，仅保留 `update` 子命令（GitHub Releases 查最新版）。
- ⚠️ **退群 / 转移群主无飞书 API**：`handleRmDo`（删除项目）只从本地 `ProjectStore` 注销并回诚实提示，不会调用飞书的 `leave_chat` / `transfer_owner`（当前 `feishu.Channel` 未实现这些接口）。
- ⚠️ **功能点层面并非 1:1（2026-07-10 全量审计发现）**：DM/GS 命名空间的 handler 已注册，但 **RC/MC/RES 三套运行卡/命令卡 action 全部死链**（`run.stop`/`goal.end`/`model.set`/`model.effort`/`resume.pick` 无任何 `d.On` 注册，按钮点不动）；**运行卡不流式**（`handle_turn.go` 自承"不含完整 RunCardStream"，只跑完发终态卡）；**reaction 事件 `im.message.reaction.created_v1` 与 bot 菜单 `application.bot.menu_v6` 未订阅**；**群 `/settings` 命令是占位、`gs.settings` 卡不可达**；文档/评论回复整体未接；Web 控制台仅只读。详见 [`ts-vs-go-alignment-audit.md`](./ts-vs-go-alignment-audit.md)。

---

## 6. 飞书 API 踩坑清单（重写时实测，避免再犯）

1. `im.message.create` **不支持 `msg_type:"markdown"`** → `code=230001 invalid msg_type`。
2. interactive 卡片 **内联 JSON 被本 app 拒绝**：`data` 传卡片**对象** → `ErrCode 11310 content's type illegal`；`data` 传卡片 **JSON 字符串** → `ErrCode 200621 parse card json err`。只有 `data:{card_id}`（CardKit 实体引用）被接受 → 所有卡片走 `SendCardByEntity`。
3. Go SDK `larksuite/oapi-sdk-go/v3` **v3.9.7 的 `im.message.create` body 不暴露 `reply_in_thread`**（仅 `message.reply` 的 builder 有 `ReplyInThread`），故 thread 归并改用「首次建根 + reply 回根」。
4. `CreateCardKitEntity` **冷启动偶发慢**（数分钟），热调用亚秒级；hook 客户端会随之阻塞，重试即恢复，非 IPC bug。
5. 任何发消息/API 调用的 error **绝不要用 `_` 吞**，否则静默失败无日志（cli-bridge 曾因此踩坑）。

---

## 7. 参考

- cli-bridge 专项对比：[`ts-vs-go-clibridge.md`](./ts-vs-go-clibridge.md)
- 源码：TS `src/` ↔ Go `internal/` + `cmd/feishu-codex-bridge`（每个 Go 文件头部注释均标注对齐的 TS 文件）
