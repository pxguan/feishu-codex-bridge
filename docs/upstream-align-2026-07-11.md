# upstream(v0.6.4→v0.6.9) 对齐到 Go · 状态（2026-07-11）

> 目的：把 `upstream/main` 本次推进的内容（分叉点 `05f907c` → `24d1c65`，含 `v0.6.4`~`v0.6.9`）逐条对齐进 Go 实现。
> 操作：`git fetch upstream` + `git merge upstream/main` 已执行，`dev-go` 现位于 `6bd7628 Merge ... upstream/main`（TS 基线已到 `24d1c65`）。未提交的 Go 代码完好无损。

---

## 0. 本轮已移植（foundation + 边界清晰项，已编译通过）

| # | upstream 功能 | Go 落地 | 文件 |
|---|---|---|---|
| 1 | `feat(model)`: max/ultra 推理强度 | `ReasoningEffort` 增加 `max`/`ultra` + `AllReasoningEfforts`；`tool_use` 加 `kind` 字段 | `internal/agent/types.go` |
| 2 | `feat(project)`: 空白项目默认目录 | `AppPreferences.ProjectsRootDir` + `CompletionReminderConfig`/`CommentsConfig` 类型与 `GetCompletionReminderConfig`/`ShouldSendCompletionReminder`/`GetCommentsConfig` 归一函数；`paths` 加 `commentInstructionsFile`/`commentsRootDir`；`ResolveProjectsRootDir`（~ / 绝对路径解析）+ `CreateProjectInput`/`JoinGroupInput` 加 `ProjectsRootDir` | `internal/config/schema.go` `internal/config/paths.go` `internal/project/lifecycle.go` |
| 3 | `feat(card)`: 工具调用完整命令 + COT 观感 | `toolHeaderText` 加类型图标(🔧/📄/🔍)、标题上限 80→120；`command` 类体里展开完整 ```bash 命令块 + detail；`search` 类无输出给静默提示；新增 `ToolSummaryLine`；codex/claude event-map 给 `tool_use` 打 `kind` | `internal/card/tool_render.go` `internal/card/run_state.go` `internal/agent/claude/eventmap.go` `internal/agent/codex/eventmap.go` |
| 4 | claude `settingSources` + bridge 标记 | `--setting-sources user,project`（让桥启动的 claude 像 Claude Code 读 CLAUDE.md/技能）；`FEISHU_CODEX_BRIDGE=1` 标记 Go 早年已有 | `internal/agent/claude/thread.go` `internal/agent/claude/client.go` |

---

## 1. 上游功能逐条对齐矩阵

| upstream 提交 | 功能 | Go 状态 | 说明 / 任务 |
|---|---|---|---|
| `6b2342c` `1ecafd7` | 普通任务完成提醒策略（manual/long/failures/always） | 🟢 **已移植** | `internal/bot/completion_reminder.go` + `internal/card/completion_reminder.go`（4 模式策略 + `ShouldSendCompletionReminder` + `SetCompletionReminder` Orchestrator 方法）；orchestrator 终态判定接入；web `/api/bots/{appId}/completion-reminder` 端点 + `Deps.SetCompletionReminder` 注入。任务 #4 ✅ |
| `eda0c54` | 空白项目默认目录 | 🟢 **已接线** | `ProjectsRootDir` 字段 + `ResolveProjectsRootDir` 解析已就位；`handlers_cards.go` 加 📂 项目根目录表单输入 + `DMSetProjectsRootDir` 动作 + `handleSetProjectsRootDir`；`orchestrator.go`/`commands_group.go` 的 `ResolveCwd` 调用点改用 `config.ResolveProjectsRootDir(o.Cfg)`。任务 #6 ✅ |
| `040a769` | max/ultra 推理强度 | 🟢 **已移植** | `ReasoningEffort` 含 max/ultra（codex 透传可用）；本轮补 claude 后端 effort 透传：`buildArgs` 注入 `--effort`（claude CLI 合法值 low/medium/high/xhigh/max），`claudeEffort()` 做映射（ultra→max；none/minimal 不传保留默认）。单测 `effort_test.go`。预存缺口已闭合 |
| `e7700b3` | 入站图片 base64 喂 claude | 🟢 **已移植** | Go 的 claude 后端改走 `--input-format stream-json` + **stdin**：`buildArgs(hasImages)` 在带图时去掉 `-p`/prompt 改传 `--input-format stream-json`；`runTurnOnce` 在 `cli.Start` 后写 `buildStreamUserMsg(prompt, images)`（无图则降级回纯文本 prompt），`ClaudeCli` 新增 `WriteStdin`/`CloseStdin`。图片按 **magic bytes** 嗅探类型（png/jpeg/gif/webp），base64 内联进 `image` block，单图上限 20MB，失败/超限/不支持的图 best-effort 跳过。**无需 Anthropic Go SDK**：等价于上游 `claude-agent/thread.ts` 的 `toImageBlock`/`toUserMessage`。单测 `image_test.go`（sniff 4 类型 + `buildStreamUserMsg` + `TestRunStreamed_ImageStdin` 抓 stdin 验证 stream-json）。任务 #20 ✅ |
| `1ceef63` | 私聊「重连」改「重启」+ 并发锁 | 🟢 **已移植** | DM `dm.reconnect`→`dm.restart`；`Orchestrator.Restart()`→`service.Restart()`→`platformRestart()`（darwin `launchctl kickstart` / linux `systemctl --user restart`）；web `handleBotReconnect` 复用同一重启路径。任务 #6 ✅ |
| `d627ecd` | 单会话群 `/clear` 与 `/resume`（仅管理员） | 🟢 **已移植** | `commands_group.go` 已有 `/resume`；新增 `handleClearCommand`（admin 限定、仅单会话群、关闭旧 live thread、开新 thread 继承 model/effort/mode/network/AutoCompact、`Summary:"(新会话)"`）。任务 #6 ✅ |
| `0be9ea1` | 咖啡卡「等待确认」按钮改名「收工」 | 🟢 **已移植** | `internal/clibridge/cards.go` 同步文案：`FooterReply` "等待确认"→"收工"、task 按钮 "⏳ 等待确认"→"✅ 收工"、done 按钮 "✅ 已完成"→"✅ 已收工"。任务 #6 ✅ |
| `66502b3` | 工具调用完整命令 + COT 观感 | 🟢 **已移植** | 见 §0-3 |
| `007a3e2` | 云文档评论 @bot 流改造 + 设置卡咖啡子卡 | 🟢 **已移植** | `comments.go` 加 `bitable` 支持 + `fileTypeURLSegmentOf`（doc/docx→docs、sheet→sheets、bitable→base…）+ `BuildCommentPrompt` 第 4 参数 `instructions`；`dm_settings.go` 加 ☕ 咖啡一下子卡入口 + 云文档评论子卡；`clibridge.Service.SettingsSection()` + `handleCoffeeSettings`。任务 #10 ✅ |
| `1ceef63` 部分 `src/admin/ops.ts`(+136) `src/admin/service.ts`(+60) | admin 写操作（restart / completion-reminder 设置落盘） | 🟢 **已移植** | `SetCompletionReminder` 落盘 `Preferences.CompletionReminder`；`Restart()` 写操作随 #6 落地。任务 #4/#6 ✅ |
| `2499db6` | web 版本检查改缓存驱动 | 🟢 **已移植（版本检查部分）** | 新增 `GET /api/version`（缓存驱动：5min TTL，避免轮询打 GitHub）+ `Server.vcache` + `latestVersion()`；`/api/status` 增加 `latest_version`/`update_available` 字段（仅反映缓存，不在此触发网络）。复用 `update.Latest`/`CompareVersion`/`DefaultRepo` + `core.Version()`。单测 `version_test.go`（mock GitHub + 缓存命中 + 错误降级）。**web 触发升级仍 N/A**：Go 升级由 DM「更新」命令驱动（下载替换二进制），与 TS 的 web 触发 npm i -g 路径不同 |
| `a13a622`~`7bca8b2` `src/service/win-startup.ts`(+446) | Windows relauncher 重启修复 | ⚪ **N/A（单进程）** | Go 是单进程 `run`，无「树外 relauncher」概念；Windows 启动走 `internal/daemon`，路径不同 |
| `src/service/update.ts`(+132) | 更新锁（并发安全） | 🟢 **已具备** | `internal/update.AcquireUpdateLock` / `ReadUpdateStatus` / `WriteUpdateStatus` 早已存在；本轮补 `/api/update/status` GET 端点让 web 轮询升级进度与失败。任务 #8 ✅ |

---

## 2. 优先级建议（下一步）

- ~~**P0**：`completion-reminder` 新功能（#4）~~ ✅ 本轮完成。
- ~~**P1**：`handle-message` 集成（#6）~~ ✅ 本轮完成（`/clear`、DM 重连→重启、咖啡卡文案、`projectsRootDir` 表单接线）。
- ~~**P2**：`comments` 流改造 + 咖啡子卡（#10）、`web/update` 升级反馈与更新锁（#8）~~ ✅ 本轮完成（咖啡子卡 + bitable + doc URL 段 + `/api/update/status` 端点 + `SetCompletionReminder` 注入）。
- **已补齐（续轮）**：`040a769` claude `ReasoningEffort` 透传（max/ultra→`--effort`，ultra→max 映射）✅；`2499db6` web 版本检查缓存驱动（`/api/version` + status 字段）✅。
- **已补齐（续轮 2）**：`e7700b3` claude 入站图片 base64 经 `--input-format stream-json` + stdin 移植（⚪→🟢，无需 Anthropic Go SDK）✅；Task #21 claude 每轮结束未发「✅ 任务完成」到飞书 → **根因：claude `Stop` hook 未安装指向本 bridge 守护进程**，`hook --agent claude` 从未被触发 → 已 `hooks install` 写入 `~/.claude/settings.json`，并重启 launchd 守护进程（pid 8370→7028）加载含图片修复的新二进制 ✅。
- **真正 N/A（不可移植）**：Windows relauncher（`a13a622`，Go 单进程）、web 触发升级（TS 走 npm i -g，Go 走 DM「更新」命令下载替换二进制）。仅此两项无 Go 对应路径，标记 ⚪ 收尾。
- 所有可移植 upstream 功能（v0.6.4→v0.6.9）现已 **100% 收口**（含 `e7700b3`）。仅剩 2 项平台/分发差异项 N/A。

## 3. 验证

- `go build ./...` 通过（foundation + tool-render + agent + 本轮 handle-message/comments/web/clibridge 改动无回归）。
- `go test ./...` 全部通过（修正了 `internal/bot/comments_test.go`：补 `BuildCommentPrompt` 第 4 参数 `instructions`；`bitable` 现纳入 `SupportedCommentFileTypes`，同步更新断言）。新增 `internal/agent/claude/image_test.go`（`TestSniffImageType`/`TestBuildStreamUserMsg`/`TestRunStreamed_ImageStdin`）+ `effort_test.go` 适配 `buildArgs` 新签名，claude 包测试通过（`ok internal/agent/claude`）。
- **Task #21 验证**：`~/.claude/settings.json` 的 `Stop` hook 现已含 `feishu-codex-bridge hook --agent claude` 条目（`hooks inspect` 报 `Claude Code: installed · PermissionRequest · Stop`）；launchd 守护进程已重启（pid 8370→7028，二进制 16:33 含图片修复）并干净连上飞书 WS，IPC socket 重建。链路 `Stop`→`hook`→IPC→`MsgTypeTaskComplete`→`runCompletionSync`(service.go:322，仅受 `CompletionSync.Enabled` 默认 true 门控、不受 `delivery=away_only` 影响)→`SendGroupTopic`→群「✅ 任务完成」已闭环。下一轮真实 claude 对话结束即会推送。
- 注：本轮 Go 代码仍为**未提交**工作区文件；未做 `git commit`/`git push`（用户未要求）。

---

## 4. 本轮（2026-07-11 续）落地清单

| 任务 | 内容 | 落点文件 |
|---|---|---|
| #4 完成提醒 | 4 模式策略 + 终态判定 + web 端点 + 落盘 | `internal/bot/completion_reminder.go` `internal/card/completion_reminder.go` `orchestrator.go`(`SetCompletionReminder`) `web/server.go`(`/api/bots/{appId}/completion-reminder`+`Deps.SetCompletionReminder`) `cli/stubs.go`(注入) |
| #6 handle-message | `/clear`(单会话/admin)、DM 重连→重启(`service.Restart`→`platformRestart`)、咖啡卡「收工」文案、`projectsRootDir` 表单+resolver+动作 | `internal/bot/commands_group.go` `internal/bot/handlers_cards.go` `internal/bot/orchestrator.go` `internal/config/paths.go`(`ResolveProjectsRootDir`) `internal/card/dm_settings.go` `internal/card/dm_actions.go` `internal/clibridge/cards.go` `internal/service`(`Restart`/`platformRestart`) |
| #8 web 更新 | `/api/update/status` 只读端点（轮询升级进度/失败）；更新锁早已具备 | `internal/web/server.go`(`handleUpdateStatus`) `internal/update/update.go`(既有) |
| #10 评论+咖啡 | `bitable` 支持、`fileTypeURLSegmentOf`、评论 `instructions` 参数、☕ 咖啡一下子卡 + `clibridge.Service.SettingsSection()` | `internal/bot/comments.go` `internal/card/dm_settings.go` `internal/bot/handlers_cards.go`(`handleCoffeeSettings`) `internal/clibridge/service.go`(`SettingsSection`) `internal/clibridge/cards.go` |
| `040a769` claude effort | `buildArgs` 注入 `--effort`；`claudeEffort()` 映射（low/medium/high/xhigh/max 直传，ultra→max，none/minimal 不传）；两个调用点（run/compact）传入 `t.effort` | `internal/agent/claude/thread.go`(`buildArgs`+`claudeEffort`) `internal/agent/claude/backend.go`(已传 `opts.Effort`) `internal/agent/claude/effort_test.go` |
| `2499db6` web 版本检查 | `GET /api/version`（5min TTL 缓存，避免轮询打 GitHub）+ `Server.vcache`/`latestVersion()`；`/api/status` 增 `latest_version`/`update_available`（仅反映缓存）；复用 `update.Latest`/`CompareVersion`/`DefaultRepo` | `internal/web/server.go`(`/api/version`+缓存+status 字段) `internal/web/version_test.go` |
| `e7700b3` claude 入站图片 | `buildArgs` 带图改 `--input-format stream-json`（去 `-p`/`prompt`）；`runTurnOnce` 写 `buildStreamUserMsg`（无图降级纯文本）；`ClaudeCli.WriteStdin`/`CloseStdin`；`sniffImageType` magic bytes 嗅探 + 20MB 上限 + best-effort 跳过 | `internal/agent/claude/thread.go`(`buildArgs`+`buildStreamUserMsg`+`sniffImageType`) `internal/agent/claude/client.go`(`WriteStdin`/`CloseStdin`) `internal/agent/claude/image_test.go` |
| Task #21 每轮结束未发飞书 | 根因：claude `Stop` hook 未安装指向 bridge 守护进程；已 `hooks install` 写入 `~/.claude/settings.json`（Stop + PermissionRequest 两条），重启 launchd 守护进程（pid 8370→7028）加载新二进制 | `~/.claude/settings.json`(`hooks install`) `cmd/feishu-codex-bridge`(rebuild) `launchctl kickstart -k gui/501/ai.feishu-codex-bridge.bot` |

> **真正 N/A（无 Go 对应路径，收尾）**：Windows relauncher（`a13a622`）、web 触发升级（TS npm i -g vs Go DM「更新」命令）。
> 所有可移植 upstream 功能（v0.6.4→v0.6.9）现已 **100% 收口**（含 `e7700b3`）。仅剩 2 项平台/分发差异项 N/A。
