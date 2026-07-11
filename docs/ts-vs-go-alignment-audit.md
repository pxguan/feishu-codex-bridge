# TS → Go 全量对齐审计报告

> 目的：在 `docs/ts-vs-go-overview.md` 模块级对照之外，做一次**功能点层面**的逐条 diff，找出"模块都在、但某个具体功能没 port"的盲区。
> 审计方法：两个探查 agent 分别全量枚举 `src/`（TS）与 `internal/`（Go）的用户可见功能，action 字面量逐字核对；对关键结论（死链 action、未订阅事件、群设置入口）再直接 grep 源码二次验证。
> 审计时间：2026-07-10；**复核：2026-07-11**（重新通读 + grep 验证，修正两处陈旧误判：§1.1/§4.1 运行卡流式已接入 `run_stream.go`、§4.2 `dm.update` 已接 GitHub Releases；并清理 `handleRmDo` 陈旧注释与 `root_test.go` 空断言测试 `TestPhase2Stub_ReturnsError`）。

---

## 0. 一句话结论

**模块布局 1:1 对齐成立，功能点层面经 2026-07-10~07-11 多轮补齐后已基本对齐。** 原 overview §5 那句「其余 DM/GS 卡片 handler 与 TS 已 1:1 对齐」当初**不成立**，但审计发现的所有缺口（运行卡流式、终止按钮、reaction、bot 菜单、群 `/settings`、群命令、评论回帖、Web 写端点、守护进程安装/卸载、成员选择器、建群提管/退群转让、`bot use` 多选、事件生效播报）**均已修复或验证**；仅剩**用量卡（wham 后端）占位**——因 Go 无此后端依赖，属外部服务缺口而非代码缺陷：

- 🔴~~运行卡**不流式**，且 ⏹ 终止 / 🎯 结束目标按钮**死链**~~ ✅ **已修复(2026-07-10 P0)**：`RunCardStream` 接入 `HandleTurn`/`HandleGoal`，`run.stop`/`goal.end` 已注册 handler（`internal/bot/run_stream.go`）
- 🔴~~**reaction 事件未订阅**~~ ✅ **已修复(2026-07-10 P0)**：`channel.go` 已订阅 `im.message.reaction.created_v1`，OK/DONE→终止、👍→续轮
- 🔴~~群 `/settings` 命令是占位，群设置卡不可达~~ ✅ **已修复(2026-07-10 P1)**：`handleCommand` `/settings` 现发真实群设置卡（`BuildGroupSettingsCard`），群级免@/自动压缩/默认模型开关可达
- 🔴~~**bot 客户端菜单 `application.bot.menu_v6` 未订阅**~~ ✅ **已修复(2026-07-10 P1)**：`channel.go` 已订阅 `application.bot.menu_v6`，按 `dm.*` event_key 路由到各 DM 卡（非管理员拒绝）
- 🔴~~群命令 `/model` `/resume` `/context` `/compact` 全是占位~~ ✅ **已修复(2026-07-10 P2)**：`commands_group.go` 实装——`/model` 拉后端模型/强度卡并即时生效（注册 `MCModel`/`MCEffort`）、`/resume` 列历史会话卡并恢复（注册 `RESPick`）、`/context` 展示 `SessionEntry.LastState` 的 token 用量、`/compact` 真正压缩活跃会话
- 🟢 文档/评论回复、Web 写端点、守护进程系统服务安装/卸载、运行卡流式（`run_stream.go`）、`dm.update` GitHub Releases 真实更新均已实装/验证（2026-07-10~07-11）；用量卡仍占位（无 wham 后端，Go 无此服务）；其余 P0–P3 全部完成。

---

## 1. 🔴 阻断级缺口（用户必撞）

### 1.1 运行卡不流式 + 终止按钮死链 —— ✅ 已修复(2026-07-10 P0)

> ✅ **2026-07-11 复核确认**：`internal/bot/run_stream.go` 已实现并把 `card.RunCardStream` 接入 `HandleTurn`/`HandleGoal`（`handle_turn.go:143-204` 走 `streamRunCardCreate`/`pushRunCard`/`finalizeRunCard` 实时 patch 运行卡，含 ⏹ 终止/🎯 结束按钮）；`run.stop`/`goal.end` 已在 `orchestrator.go` 注册 handler（`handleRunControl`）。下方为修复前的原始问题描述，保留作根因记录。

- **现象（修复前）**：在群内 `@bot` 提问，机器人**只在任务跑完才发一张最终卡片**，过程中没有任何"思考中 / 调用工具 / 输出中"的实时更新；最终卡片上的 ⏹ 终止 / 🎯 结束目标按钮**点了没反应**。
- **根因（修复前，已 grep 证实）**：
  - 旧 `internal/bot/handle_turn.go` 的 `HandleTurn` 只 `thread.RunStreamed` → 在 `for ev := range run.Events` 里 `card.Reduce` 累加状态，**循环结束后才 `BuildRunCard` 发一张终态卡**。
  - 运行卡渲染器 `internal/card/run_card.go` 画了 `Button("⏹ 终止", {"a": RCStop})` / `Button("🎯 结束目标", {"a": RCEndGoal})`，但 dispatcher 未注册 `run.stop`/`goal.end` → 按钮静默无响应。
- **TS 对照**：`handle-message.ts` 的 run card 走 `RunCardStream` 引擎，过程中持续 `sequence` 更新同一张 CardKit 实体；`run.stop` action 触发 `running.interrupt()` 真正中断后端进程。
- **影响（修复前）**：① 长任务全程黑屏；② **一旦任务跑起来就停不下来**。**（现已修复：运行卡实时流式更新 + 终止/结束按钮生效 + reaction 停止路径打通）**。

### 1.2 reaction 事件未订阅（停止/继续完全失效）
- **现象**：给机器人消息点 👍 / ✅ / 🛑 表情，没有任何反应。
- **根因（已 grep 证实）**：`internal/feishu/channel.go` 的 `Connect` 只注册了两类事件——`OnP2MessageReceiveV1`（:102）和 `OnCustomizedEvent("card.action.trigger", …)`（:132）。**没有 `im.message.reaction.created_v1` 的订阅与处理**。飞书 reaction API 本身已实现（`AddMessageReaction`/`RemoveMessageReaction`，channel.go:60-91），但只被机器人**给自己**的处理中消息加/去表情用（`addProcessingReaction`，orchestrator.go:734-761），没有"用户表情→命令"的 inbound 处理。
- **TS 对照**：`onReaction`（handle-message.ts:3806）对 `OK`/`DONE` → 中断运行中的 run，`THUMBSUP` → 在话题里"继续"。依赖 `im:message.reactions:read`。
- **影响**：与 1.1 叠加后，**目前 Go 版没有任何一条能用的"停止运行中任务"路径**。

### 1.3 群 `/settings` 命令占位 + 群设置卡不可达
- **现象**：在群里 `@bot /settings` 只收到一句"⚙️ 群设置（后续填充设置卡）"占位 markdown；群级免@ / 自动压缩 / 默认模型开关无从打开。
- **根因（已 grep 证实）**：
  - `internal/bot/orchestrator.go:672-674` 的 `handleCommand` `/settings` 分支确实只 `sendCard(占位 markdown)`。
  - 群设置卡本身**已实现**（`BuildGroupSettingsCard`，`internal/card/dm_cards_ext.go:500`）且其开关 handler `gs.settings`/`gs.noMention`/`gs.autoCompact`/`gs.modelDefault`/`gs.modelDefault.submit` **都已 `d.On` 注册**（orchestrator.go:295-311），`handlers_cards.go:861-936` 也确实在渲染它。
  - **但没有任何命令或菜单会首次触发 `gs.settings` 这个 action**——`dm_cards_ext.go:237` 只有"返回群设置"按钮会发 `gs.settings`，而那个按钮本身就处在群设置卡内部。相当于"进群设置卡的门"没装。`/settings` 占位后不 emit `gs.settings`，所以整张群设置卡不可达。
- **TS 对照**：`/settings`（仅管理员）直接 `buildGroupSettingsCard` 打开。
- **影响**：群级 免@/自动压缩/默认模型 三个开关**打不开**（仅能通过 DM 控制台 → 项目设置 里的 `dm.proj.*` 等价项间接设置，体验割裂）。
- **✅ 已修复(2026-07-10 P1)**：`handleCommand` `/settings` 分支改为 `sendCard(BuildGroupSettingsCard(...))`（按 chatID 取项目，无则默认多话题群），群设置卡可达。

### 1.4 bot 客户端菜单 `application.bot.menu_v6` 未订阅
- **现象**：在飞书客户端里点机器人头像 → 菜单项（新建项目/项目列表/设置/用量/诊断/重连/更新），点了无反应。
- **根因（已 grep 证实）**：同 1.2，`channel.go` 只订阅 message + card.action，没有 `application.bot.menu_v6`。注意 `internal/utils/eventdiagnosis.go:27` 把 `menu_v6` 列为"必配事件"做诊断，但 `channel.go` 实际没订阅它——诊断和建议自相矛盾。
- **TS 对照**：菜单 `event_key` 直接复用 `dm.*` 字面量，由 `onBotMenu`（handle-message.ts:3887）路由到对应 DM 卡。
- **影响**：飞书客户端内机器人菜单失效（DM 私聊里点菜单按钮本身是正常走的，因为那是 card.action）。
- **✅ 已修复(2026-07-10 P1)**：`channel.go` 已 `OnCustomizedEvent("application.bot.menu_v6")` 订阅，`HandleBotMenu` 按 `dm.*` event_key 路由到对应 DM 卡（非管理员拒绝，event_id 去重），与 TS `onBotMenu` 对齐。

---

## 2. 🟠 降级级缺口（能跑但残缺）

### 2.1 群命令 `/model` `/resume` `/context` `/compact` —— ✅ 已全部实装(2026-07-10 P2)
- `internal/bot/commands_group.go` 实装，`handleCommand` 已路由：
  - `/model` → `handleModelCommand` 拉后端模型/强度列表发卡；下拉 `MCModel`/`MCEffort` → `handleModelSelect` 即时落盘 + 驱逐活跃会话 + 重渲染。
  - `/context` → `handleContextCommand` 展示 `SessionEntry.LastState.Usage`（`HandleTurn`/`HandleGoal` 在 `EvContextUsage` 事件里写入）。
  - `/compact` → `handleCompactCommand` 调用活跃会话 `thread.Compact` 真正压缩。
  - `/resume` → `handleResumeCommand` 列历史会话卡；`RESPick` → `handleResumePick` 恢复并接管活跃会话。
- TS 对照已对齐：`/model` 真实切模型+强度、`/context` 发上下文占比卡、`/compact` 真正压缩、`/resume` 发历史会话选择卡。
- 仅用量/更新/评论相关卡片仍占位（见 2.2 / 2.4）。

### 2.2 文档/评论回复 —— ✅ 已实装(2026-07-10)
- `internal/feishu/channel.go` 订阅 `drive.notice.comment_add_v1`（OnP2NoticeCommentAddV1），并实装 `GetFileComment`（拉评论/回复文本）+ `CreateFileCommentReply`（回帖到同一条评论）。
- `internal/bot/orchestrator.go` `HandleComment`：仅处理 `is_mentioned` 的评论 → 由 `file_token` 定位项目（按 `Project.SourceURL` 包含匹配；单项目直接用；多项目且无 SourceURL 则报错避免跑错目录）→ `BuildCommentPrompt` 构造 prompt → `runAgentSync` 跑 agent 取最终文本 → `StripMarkdown` 去标记 → 截断到 `ReplyMaxChars(2000)` → 回帖。
- 对齐 TS `comments.ts`：云文档评论 @bot → 解析 → 跑 codex/claude → 回帖到同一评论线程。
- 新增 `Project.SourceURL` 字段（项目设置可填，用于评论→项目定位）。`internal/bot/comments.go` 的纯函数（`BuildCommentPrompt`/`StripMarkdown`/`ReplyMaxChars`）已接真实 SDK 回帖路径。

### 2.3 Web 管理控制台 —— ✅ 已实装(2026-07-10)
- `internal/web/server.go` 从只读扩为可读写：daemon 启动时经 `cli/stubs.go` 注入 `Deps{Projects, Reconnect, LogFile}`，新增端点：
  - `GET /api/projects` 列出全部项目（含设置）；
  - `POST /api/projects/{name}/settings` 写 `noMention`/`autoCompact`/`defaultModel`/`defaultEffort`/`sourceUrl`（经 `Store.Update` 原子落盘）；
  - `GET /api/logs?lines=N[&follow=1]` 尾随 daemon 日志（follow=1 走 SSE 流）；
  - `POST /api/bot/reconnect` 重连飞书长连接（`Channel.Reconnect`）；
  - `POST /api/bot/register` 注册新 bot（密钥存 keystore + `AddBot` 落 bots.json）。
- 未注入 Deps（单独 `web` 命令）时写端点返回 501 只读降级。
- 守护进程系统服务安装/卸载（Go 版「后端一键安装」等价物）：✅ **已实装(2026-07-11)**——`internal/service` 包按平台生成服务文件并注册：macOS 写 `~/Library/LaunchAgents/<Label>.plist`（`launchctl bootstrap`+`kickstart`，复用已有 plist 的 PATH 避免后端丢失）+ Linux 写 `~/.config/systemd/user/<Label>.service`（`systemctl --user enable --now`）；CLI `bot install`/`bot uninstall`/`bot service` 子命令 + Web 端点 `POST /api/bot/install`、`POST /api/bot/uninstall`、`GET /api/bot/service`。TS 的 npm catalog「按需装可选后端依赖」在 Go 侧无对应物（后端为内置/外部探测），故不移植；扫码 SSE 见 3.2（Node-only 不可移植）。
- ⚠️ Web 端点 `install`/`uninstall` 自重启限制：从**运行中的 daemon 自身**调用这两个端点会令其自重启/自停止，HTTP 响应可能发不出（curl 空回复）；CLI `bot install`/`bot uninstall` 是独立进程，路径稳妥，推荐用 CLI。

### 2.4 用量 / 更新卡片
- **用量**：`handlers_cards.go` `DMUsage` 仍发占位 markdown"Go 版暂未接入用量统计后端（wham API）"。卡片渲染器 `BuildUsageCard`/`BuildShareConfigCard`/`BuildUsageShareCard` 代码完整，但 handler 不走它。TS 接的是 ChatGPT/wham 后端，Go 版无此后端 → **保持占位**。
- **更新**：✅ **已修复(2026-07-10)**：`handleUpdate`/`handleUpdateDo` 已接 `internal/update` 的 GitHub Releases 真实查询（`Latest`/`CompareVersion`/`CurrentPlatformAsset`/`DownloadToTemp`）。`DMUpdate` 显示真实最新版本号与是否有更新；`DMUpdateDo` 真正下载匹配平台的二进制到临时文件并提示手动替换（沿用 CLI `update` 的"不自动替换运行中二进制"安全策略）。
- 影响：DM 里点"更新"现已可用（查版本 + 下载）；用量仍依赖 wham 后端（Go 无），保持占位。

### 2.5 管理员 / 白名单成员选择器 —— ✅ 已修复(2026-07-10)
- `handleAddAdminForm` 经 `memberInputsAllProjects` 跨所有项目群 `GetChatMembers` 去重拉成员下拉；`handleAddAllowedForm` 经 `memberInputsForProject` 拉该项目群成员下拉。卡片渲染器 `BuildAddAdminCard`/`BuildAddAllowedCard` 已有 `SelectMenu("member", …)` 下拉（按 `ou_` 过滤掉机器人自身）。
- TS 对照：add admin/allowlist 用群成员 picker。
- 影响：已从"手填 ou_"升级为"从群成员下拉选择"，体验对齐 TS。

### 2.6 AddManagers / 退群-转让群主 —— ✅ 已修复(2026-07-10)
- `internal/feishu/chat.go` 实装 `GetChatMembers`/`AddManagers`(真实 `chatManagers.AddManagers`)/`TransferOwner`(`chat.Update` owner_id)/`LeaveChat`(`chatMembers.Delete` 以 app_id 移除自身)。
- `handleNewProjectSubmit` 建群后调用 `AddManagers` 把创建者提升为群管理员（真实生效）。
- `handleRmDo` 删项目前 best-effort：先 `GetChatMembers` 找一个人类成员 `TransferOwner` 转让群主，再 `LeaveChat` 退群；失败（如仍是群主）仅 warn 不阻断解绑，并在群里提示手动移出。
- 影响：建群后创建者即管理员；删项目后机器人自动退群（best-effort）。注：若机器人仍是唯一群主，飞书拒绝退群，已降级提示手动处理。

---

## 3. 🟡 取舍 / 次要（可接受或路径不同）

| # | 项 | 说明 |
|---|---|---|
| 3.1 | `bot use` 多选已实现 | Go 版支持 `bot use <name...>`（按名/appId 激活）、`--all`/`--none`、无参且 TTY 时编号交互多选（`botUse` + `interactivePickBots`，`internal/cli/stubs.go`），复用 `config.SetActiveBots`。TS 用 inquirer 多选 → 体验等价。✅ |
| 3.2 | 扫码注册向导缺失 | Go 只支持 `bot init` 凭据直填（`RegisterBotFromCredentials` 真探活+写 keystore）；TS 另有 `wizard.ts` 扫码向导 + Web 扫码 SSE。**Go 版 Lark SDK 无 `registerApp`（Node-only），扫码注册不可移植**，仅凭据直填路径。功能等价（都能注册 bot）。🟡 路径不同，非缺陷。 |
| 3.3 | 事件生效播报已实现 | TS `announceEventsWhenLive` 轮询版本 API 播报"事件已生效"；Go 对齐：`Channel.OnConnected`(SDK `ws.WithOnReady`，**首次连上**触发，非断线) 触发 `Orchestrator.AnnounceWhenLive`（`internal/feishu/channel.go` / `internal/bot/orchestrator.go`），复用 `utils.DiagnoseEventSubscription`/`PollEventSubscription` 诊断+后台轮询，DM 播报卡（ok/warn 两态，`internal/card/event_live.go`）发给 owner+全部 admin。missing/unpublished 先不播报，等后台轮询确认 ok 再播。✅ **并修复诊断误判**：飞书 `app_versions` 的 `events` 是中文展示名（"接收消息"），真实事件码在 `event_infos[].event_type`；旧代码拿码比名→误报"缺 im.message.receive_v1"，会向 owner 发假警告。已改为用 `event_infos[].event_type` 判定（含回归测试 `TestDiagnose_EventsAreChineseNames`）。 |
| 3.4 | TS 冗余常量 | `cli.set.delivery` / `cli.toggle.includeBridge`（TS `cli-bridge/cards.ts:22,25`）在 TS 侧也**未被任何卡片/dispatcher 使用**，属冗余；Go 可忽略。 |

---

## 4. 功能对齐矩阵（action / 命令级，逐字核对）

### 4.1 运行卡 / 命令卡 action（RC / MC / RES 命名空间）—— ✅ 已全部修复(2026-07-10 P0/P2)

| TS action | Go 常量 | Go 注册 handler? | Go 工作? | 备注 |
|---|---|---|---|---|
| `run.stop` | `card.RCStop` | ✅ `d.On` | ✅ **已修复(2026-07-10 P0)** | 终止按钮 → `handleRunControl` 取消 agent 运行 ctx |
| `goal.end` | `card.RCEndGoal` | ✅ `d.On` | ✅ **已修复(2026-07-10 P0)** | 结束目标按钮 → `handleRunControl` |
| `model.set` | `card.MCModel` | ✅ `d.On` | ✅ **已修复(2026-07-10 P2)** | `/model` 下拉 → `handleModelSelect("model")` 即时生效 |
| `model.effort` | `card.MCEffort` | ✅ `d.On` | ✅ **已修复(2026-07-10 P2)** | `/model` 强度下拉 → `handleModelSelect("effort")` |
| `resume.pick` | `card.RESPick` | ✅ `d.On` | ✅ **已修复(2026-07-10 P2)** | `/resume` 卡按钮 → `handleResumePick` 恢复历史会话 |

> 2026-07-11 复核确认：上述 5 个 action 均已注册 handler 且工作正常（运行卡经 `run_stream.go` 流式实时更新）。原审计"全部死链"结论已不成立。

### 4.2 DM 控制台（dm.*）—— ✅ 基本对齐（含少量占位）

| TS action | Go handler 注册 | 状态 |
|---|---|---|
| `dm.menu` `dm.newProject` `dm.newProject.submit` `dm.joinGroup.submit` `dm.projects` `dm.settings` | ✅ | 完全实现（建群异步已修） |
| `dm.doctor` `dm.reconnect` | ✅ | ✅ 真实探测(2026-07-11)：`handleDoctor` 不再硬编码——`agent.DetectAgents()` 探 codex 可用性/版本、`Channel.ConnState()`（SDK OnReady/OnReconnecting/OnReconnected/OnDisconnected 回调维护）取真实长连接态、`utils.ValidateAppCredentials` 比对 granted scopes 得 MissingScopes/JoinMissing/BotOpenID、`utils.DiagnoseEventSubscription` 取事件订阅诊断；日志/配置/平台路径与授权/事件页 URL 均真实填充。`handleReconnect` 同步用真实连接态。 |
| `dm.update` `dm.update.do` | ✅ | ✅ 已接真实后端(2026-07-10)：`handleUpdate` 查 GitHub Releases 最新版本号、`handleUpdateDo` 下载匹配平台二进制到临时文件（沿用"不自动替换运行中二进制"安全策略）；非占位 |
| `dm.usage` `dm.usage.refresh` `dm.usage.share` `dm.usage.share.do` | ✅ | 占位：wham 后端未接 |
| `dm.rmConfirm` `dm.rmCancel` `dm.rmDo` | ✅ | 完全实现（解绑 + `GetChatMembers` 找人类成员 `TransferOwner` 转让群主 + `LeaveChat` 退群，best-effort 失败仅 warn） |
| `dm.set.tools` `dm.set.showModel` `dm.set.watchdog` `dm.set.watchdog.custom` `dm.set.watchdog.customSubmit` `dm.set.pending` `dm.set.concurrency` | ✅ | 完全实现（落盘即时生效） |
| `dm.admins` `dm.admin.addForm` `dm.admin.addSubmit` `dm.admin.rm` `dm.allowlist` `dm.allow.addForm` `dm.allow.addSubmit` `dm.allow.rm` | ✅ | 完全实现（`memberInputsAllProjects`/`memberInputsForProject` 跨群拉成员下拉，按 `ou_` 过滤自身） |
| `dm.projectSettings` `dm.projectTopics` `dm.proj.noMention` `dm.proj.autoCompact` `dm.proj.perm` `dm.proj.perm.submit` `dm.proj.modelDefault` `dm.proj.modelDefault.submit` | ✅ | 完全实现 |

### 4.3 群设置（gs.*）—— ⚠️ handler 在，入口断

| TS action | Go handler 注册 | 状态 |
|---|---|---|
| `gs.settings` `gs.noMention` `gs.autoCompact` `gs.modelDefault` `gs.modelDefault.submit` | ✅ 已注册 | ✅ **已可达**(2026-07-10 P1)：`/settings` 命令现发真实群设置卡触发 `gs.settings` |

### 4.4 群文本命令

| 命令 | TS | Go | 状态 |
|---|---|---|---|
| `/help` | 速查卡 | ✅ 真实卡 | 对齐 |
| `/goal <目标>` | 自主多轮 | ✅ `HandleGoal` 真实 | 对齐 |
| `/settings` | 群设置卡 | ✅ 真实卡（2026-07-10 P1） | ✅ 已修复 |
| `/model` | 模型/强度卡 | ✅ 真实卡 + 下拉生效 | ✅ **已修复(2026-07-10 P2)** |
| `/context` | 上下文占比卡 | ✅ 展示 `SessionEntry.LastState` 的 token 用量 | ✅ **已修复(2026-07-10 P2)** |
| `/compact` | 压缩 | ✅ 真正压缩活跃会话 | ✅ **已修复(2026-07-10 P2)** |
| `/resume` | 历史会话卡 | ✅ 列历史 + `resume.pick` 恢复 | ✅ **已修复(2026-07-10 P2)** |

### 4.5 事件订阅

| 事件 | TS | Go channel.go | 状态 |
|---|---|---|---|
| `im.message.receive_v1` | ✅ | ✅ `OnP2MessageReceiveV1` | 对齐 |
| `card.action.trigger` | ✅ | ✅ `OnCustomizedEvent` | 对齐 |
| `im.message.reaction.created_v1` | ✅ `onReaction` | ✅ `OnP2MessageReactionCreatedV1`（2026-07-10 P0） | ✅ 已修复：运行中卡 OK/DONE→终止，终态卡 👍→续轮 |
| `application.bot.menu_v6` | ✅ `onBotMenu` | ✅ `OnCustomizedEvent` + `HandleBotMenu`（2026-07-10 P1） | ✅ 已修复：按 `dm.*` event_key 路由到各 DM 卡，非管理员拒绝 |
| `drive.notice.comment_add_v1` | ✅ 评论回复 | ✅ `OnP2NoticeCommentAddV1` + `HandleComment`（2026-07-10） | ✅ 已修复：仅处理 @bot 评论 → 定位项目 → 跑 agent → 回帖 |
| `im.message.reaction.deleted_v1` | （无操作） | ✅ `OnP2MessageReactionDeletedV1` no-op（2026-07-10） | ✅ 已修复：静默消费，消除 "not found handler" 日志噪声 |
| `im.chat.member.bot.added_v1` / `deleted_v1` | ✅ 绑群/解绑 | ✅ `OnP2ChatMemberBotAddedV1` / `OnP2ChatMemberBotDeletedV1`（2026-07-10） | ✅ 已修复：`HandleBotAdded` 已绑定则提示、未绑定 DM 操作者绑定表单；`HandleBotDeleted` 自动解绑并通知群主 |

### 4.6 Web 端点 / CLI 子命令 / 注册流程

| 类别 | TS | Go | 状态 |
|---|---|---|---|
| Web 端点 | 20+ 读写 | `/healthz` `/api/status` `/` + 写端点（projects/settings/logs/reconnect/register/**bot install·uninstall·service**）| ✅ 已实装(2026-07-10 基础 + 2026-07-11 服务安装/卸载，见 2.3) |
| CLI 子命令 | run/start/stop/restart/status/logs/update/web/bot init\|list\|use\|rm/doctor/secrets/hook/chats | 同套 + `send` + `hooks install\|inspect\|uninstall` + **`bot install\|uninstall\|service`** | ✅ `bot use` 多选已实装（见 3.1）；`bot install/uninstall/service` 守护进程系统服务已实装(2026-07-11) |
| 注册流程 | 扫码向导 + 凭据直填 + Web 扫码 SSE | 仅凭据直填（扫码向导 Node-only 不可移植，见 3.2） | 🟡 路径不同（见 3.2） |

---

## 5. 根因归纳

1. **运行卡流式引擎未接入 bot 路径** —— ✅ **已修复(2026-07-10 P0)**：`internal/bot/run_stream.go` 把 `card.RunCardStream` 接入 `HandleTurn`/`HandleGoal`（`handle_turn.go:143-204` 走 `streamRunCardCreate`/`pushRunCard`/`finalizeRunCard` 实时 patch 运行卡，含 ⏹ 终止/🎯 结束按钮）；`run.stop`/`goal.end` 已在 `orchestrator.go` 注册 `handleRunControl`。原审计此条已不成立。
2. **事件订阅清单过窄**：`channel.go` 只接了 message + card.action 两类，漏了 reaction、bot 菜单、评论、bot 进出群。
3. **部分 handler 注册了但入口命令没接**：`gs.*` 全套 handler 都在，唯独 `/settings` 命令是占位、不 emit `gs.settings`，导致群设置卡不可达。同理 `MC.*`/`RES.*` 卡片画了但无命令/无 handler。
4. **外部后端未在 Go 实现**：wham（用量）、npm（更新卡）在 TS 侧是独立服务，Go 版没移植，故相关卡片只能占位（底层 `update` 库已用 GitHub Releases 替代 npm）。
5. **飞书细粒度 API 未封装**：`GetChatMembers`/`AddManagers`/`leave_chat`/`transfer_owner`——✅ **已实装(2026-07-10)**（`internal/feishu/chat.go`），并接到成员选择器(2.5)、建群提管理员、删项目退群/转让(2.6)。

---

## 6. 建议修复优先级

- **P0（停止能力，最致命）—— ✅ 已全部完成（2026-07-10）**
  1. ✅ `handle_turn.go` 接入 `RunCardStream` 流式引擎：过程中持续更新运行卡（`streamRunCardCreate`/`pushRunCard`/`finalizeRunCard`，`runCtx`/`ctx` 分离保证 stop 不卡 patch）；无 CardKitClient 时回退单卡。
  2. ✅ 注册 `run.stop` / `goal.end` handler（`handleRunControl`，按运行卡 message_id 定位 `activeRuns` 句柄并 cancel agent 运行 ctx）。
  3. ✅ `channel.go` 订阅 `im.message.reaction.created_v1` + `HandleReaction`（运行中 OK/DONE→终止；终态 👍→续轮 steer "继续"，对齐 TS STOP_EMOJIS={OK,DONE}/CONTINUE_EMOJIS={THUMBSUP}）。
- **P1（群侧命令补全）**
  4. ✅ `/settings` 命令现发真实群设置卡（`BuildGroupSettingsCard`），接通 `gs.settings`（2026-07-10 已修复）。
  5. ✅ `/model` 实装（接后端 model 列表 + 注册 `model.set`/`model.effort`）、`/resume` 接 `listThreads` + 注册 `resume.pick`、`/context`/`/compact` 实装（**全部完成 2026-07-10 P2**）；
  6. ✅ `channel.go` 订阅 `application.bot.menu_v6` → `HandleBotMenu` 按 `dm.*` event_key 路由（2026-07-10 已修复）。
- **P2（增强）**
  7. 文档/评论回复（`drive.notice.comment_add_v1` + 回帖）—— ✅ **已实装(2026-07-10)**；
  8. Web 控制台补写端点（bot 注册/重连/日志流/项目设置写）—— ✅ **已实装(2026-07-10)**；守护进程系统服务安装/卸载（Go 版「后端一键安装」等价物）—— ✅ **已实装(2026-07-11)**，见 2.3；
  9. 用量/更新卡片接真实后端 —— ✅ **更新已接真实 GitHub Releases(2026-07-10)**；用量(wham)无后端，**保持占位**。
- **P3（体验）**
  10. `feishu` 包补 `GetChatMembers`/`leave_chat`/`transfer_owner`/`AddManagers` 实装 —— ✅ **已全部实装并接线(2026-07-10)**；
  11. `bot use` 多选、扫码注册向导、事件生效播报 —— ✅ `bot use` 多选(3.1) 与事件生效播报(3.3) **已实装并验证(2026-07-11)**；扫码注册向导(3.2) 因 Go Lark SDK 无 `registerApp`（Node-only）**不可移植**，仅凭据直填，功能等价。

---

## 7. 与 overview 文档的关系

本审计是 `docs/ts-vs-go-overview.md` 的**功能点级补丁**。overview 的模块映射（§2/§3）仍成立；但其 §5 末尾"其余 DM/GS 卡片 handler 与 TS 已 1:1 对齐"需修正为"DM/GS handler 已注册，但 RC/MC/RES 三套运行卡/命令卡 action 未注册、群设置入口断、reaction/bot 菜单事件未订阅"——详见本文 §1–§4。
