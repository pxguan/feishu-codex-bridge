# TS 原版 vs Go 重写版：cli-bridge 实现对比

> 适用对象：接手 / 维护 `feishu-codex-bridge` 的开发者。
> 背景：本项目从 TypeScript 重写为 Go（Go module `github.com/modelzen/feishu-codex-bridge`，分支 `dev-go`，仓库在 `feishu-codex-bridge-go` worktree）。
> 本文聚焦 **cli-bridge**（本地 agent hook 桥接飞书的这一段），逐点对比两边实现，**重点标出差异与飞书 API 踩坑**，避免后人重蹈。
> 文中路径均为仓库内相对路径。

---

## 0. 一句话结论

整体架构、IPC 模型、presence 路由、CardKit 实体卡体系两边**对齐得很好**；唯一本质差异在「群话题的 markdown 到底怎么发」——TS 走 `post` 富文本，Go 为绕过飞书对 inline 卡片的限制 + 满足「真 markdown 渲染」改成了 `interactive` CardKit 卡片。

---

## 1. 模块映射（目录近乎 1:1）

cli-bridge 这一层，TS `src/cli-bridge/*` 与 Go `internal/clibridge/*` **文件名逐一对应**：

| TS (`src/cli-bridge/`) | Go (`internal/clibridge/`) | 说明 |
|---|---|---|
| `ipc.ts` | `ipc.go` | Unix socket IPC（换行 JSON） |
| `service.ts` | `service.go` | hook 事件处理 / completion-sync / 路由 |
| `hooks.ts` | `hooks.go` | hook 子命令（CLI 触发入口） |
| `presence.ts` | `presence.go` | 本机在场检测 |
| `parser.ts` | `parser.go` | hook payload 解析 |
| `protocol.ts` | `protocol.go` | IPC 协议常量 / 消息结构 |
| `store.ts` | `store.go` | 项目本地存储（projects.json） |
| `types.ts` | `types.go` | 类型定义 |
| `cards.ts` | `cards.go` | 卡片构建（owner 卡等） |
| `keep-awake.ts` | `keepawake.go` | 防系统休眠 |

更大的目录级映射：

| TS (`src/`) | Go (`internal/` 或 `cmd/`) | 说明 |
|---|---|---|
| `cli-bridge/` | `clibridge/` | 名字去掉连字符 |
| `bot/`、`bot/bridge.ts` | `bot/`、`clibridge` 注入 | 项目查找 / 建群逻辑 |
| `card/` | `card/` | 卡片构造（schema 2.0） |
| `config/schema.ts` | `config/` | 偏好 schema |
| `service/` | `daemon/` | 后台守护进程 |
| `cli/commands` | `cli/` + `cmd/feishu-codex-bridge` | CLI 入口 |
| `web/`、`admin/`、`platform/`、`utils/` | 同名 | 一致 |

**Go 独有的两个独立包**（TS 里没有对应物）：
- `internal/feishu/` —— 飞书 OpenAPI wrapper（基于 `larksuite/oapi-sdk-go/v3`）。
- `internal/update/` —— 自更新。

---

## 2. 架构与核心链路

### 2.1 IPC 通信 —— 完全一致
两边都用 **Unix domain socket + 换行分隔的 JSON**（一个连接一条请求）；`hook` 子命令从 **stdin** 读 payload 再发到 socket。TS `cli-bridge/ipc.ts`，Go `internal/clibridge/ipc.go`。握手 / 超时 / 权限（chmod 600）一致。

### 2.2 TaskComplete → 群话题 —— 调用链对齐

```
TS:  parseHookPayload → handleMessage
        → completionSync（fire-and-forget，忽略 presence 先做）
        → findProjectByCwd / createProjectForCwd
        → sendGroupTopic 闭包
        → channel.send(chatId, { markdown: body }, { replyInThread: openThread })

Go:  runCompletionSync
        → CreateProjectForCwd（无条件调用）
        → SendGroupTopic（cli-bridge 闭包）
        → ch.SendMarkdown(chatID, markdown)  /  ch.SendMarkdownInThread(chatID, markdown)
```

等价点：`completionSync` 都在 presence 路由**之前** fire-and-forget 执行；`CreateProjectForCwd` 都无条件调用；失败都兜底发 owner 卡。

---

## 3. ⭐ 群话题 markdown 发送：本质差异（重点）

| 维度 | TS 原版 | Go 当前 |
|---|---|---|
| **飞书消息类型** | `msg_type: 'post'`（富文本） | `msg_type: 'interactive'`（交互卡片） |
| **底层做法** | `createLarkChannel` 的 `channel.send(chatId, { markdown }, …)`，由 **node-sdk 内部**把 markdown 转成 `post` 富文本（转换逻辑在 `@larksuiteoapi/node-sdk` 依赖里，不在本仓库 `src`） | 构造 schema 2.0 卡片（单 `markdown` 元素）→ 走 **CardKit 实体**（`card_id` 引用） |
| **是否建 CardKit 实体** | 否，内联 JSON 直接发 | 是，每次 `CreateCardKitEntity` + 引用发 |
| **thread 回复** | create 时直接 `reply_in_thread`（node-sdk 的 `LarkChannel.send` 暴露该字段） | 首次 `SendMarkdown`(create) 当 thread 根记录 `message_id`，之后 `replyMessage(ReplyInThread=true)` 挂回该根（见 §5 原因） |
| **markdown 能力** | 较弱（`post` 富文本的子集：加粗 / 行内代码 / 链接 / 列表 / 标题，无代码块、无表格） | 完整（卡片 `markdown` 元素：加粗 / 行内代码 / 链接 / 代码块 / 引用 / 标题 / 表格全支持） |
| **延迟** | 一次 API，快 | 多一次 cardkit API，**冷启动偶发慢**（见 §6） |

### 为什么 Go 没沿用 TS 的 `post` 路线
实测踩坑（见 §6）决定了这条路走不通：
1. 飞书 `im.message.create` 不支持 `msg_type: "markdown"`（直接 `230001 invalid msg_type`）；
2. 内联 `interactive` JSON 在本 app 下也被拒（`11310 content's type illegal` / `200621 parse card json err`）；
3. **只有 CardKit 实体引用**（`data: { card_id }`）被接受。

再加上用户明确要求「真正渲染 markdown」，`interactive` 卡片的 `markdown` 元素能力比 `post` 富文本强得多，于是 Go 走成了现在的 CardKit 交互卡片方案。

---

## 4. CardKit 实体卡（权限卡 / owner 卡）—— 完全一致

TS `card/managed.ts` 的 `sendManagedCard` 与 Go `feishu.SendCardByEntity` 是**同一套**逻辑：
1. 先 `cardkit.v1.card.create`（或 SDK 等价调用）拿到 `card_id`；
2. 再发 `{ "type": "card", "data": { "card_id": ... } }` 引用消息。

owner 卡还会缓存 `card_id`，按 `sequence` 做整卡 / 元素级更新，两边一致。

---

## 5. delivery / presence 路由 —— 基本一致，Go 真的支持 `always`

- **TS**：`src/config/schema.ts:228` 的类型定义确实写了 `'always' | 'away_only'`，但 `:237` 注释明确「历史兼容字段；用户侧不再暴露配置，**运行时固定为 away_only**」（`:342` 默认也是 `away_only`）。即 TS 实际只跑 `away_only`。
- **Go**：`internal/clibridge/service.go:287` **真正实现了两者切换** —— `delivery=always` 忽略 presence、强制路由飞书（reason 记为 `delivery_always`）；`delivery=away_only`（默认）下 `local_active` 走本地终端审批。
- **presence 检测**：TS `cli-bridge/presence.ts`（`ioreg HIDIdleTime` + `CGSSessionScreenIsLocked`，macOS）；Go `internal/clibridge/presence.go` 的 `localActivity()`，逻辑等价。本地活跃 → 走本地终端，离开 / 空闲 → 推飞书。

> 历史插曲：本项目曾把 `delivery` 在 `always` / `away_only` 间反复切过，区别就在于此——Go 保留了 `always` 这个 TS 实际已关掉的能力。

---

## 6. 关键坑（均已实测，勿复犯）

1. **飞书 `im.message.create` 不支持 `msg_type: "markdown"`** → `code=230001 invalid msg_type`。任何想直接发 markdown 文本消息的尝试都会失败。
2. **本 app 拒绝内联 interactive JSON 卡片**：`data` 传卡片**对象** → `ErrCode 11310 content's type illegal`；`data` 传卡片 **JSON 字符串** → `ErrCode 200621 parse card json err`。**只有 `data: { card_id }`（CardKit 实体引用）被接受**。所以 `message.go` 里的 `SendCardJSON`（内联 JSON 那套）在本 app 下实际不可用，发 markdown / 群话题一律走 `SendCardByEntity`。
3. **Go SDK v3.9.7 的 `im.message.create` body builder 不暴露 `reply_in_thread`**（只有 `message.reply` 的 builder 有 `ReplyInThread`）。TS 能在 create 时开 thread，是因为 node-sdk 版本较新暴露了该字段。故 Go 的 thread 归并改用「首次建根 + 之后 `replyMessage(ReplyInThread=true)` 挂回根」实现，效果等价（甚至更连贯：同一项目所有完成历史归并到一段 thread）。
4. **CardKit 实体首调冷启动偶发慢**：守护进程重启后首次 `CreateCardKitEntity` 可能耗时数分钟（TLS / 连接或飞书 cardkit 偶发延迟），但热调用亚秒级正常。首调延迟会拖住 `hook` IPC 客户端的返回（看起来像卡死），**非 IPC bug**，重试即恢复。
5. **发消息 / API 调用的 error 绝不能 `_` 吞**：cli-bridge 的 `runCompletionSync → sendGroupTopic` 曾因吞错导致「群话题永远发不出去却无任何日志」。现已改为 `core.Warn`（失败）+ `core.Info`（成功）显式记录，排「没收到消息」类问题先查发送路径是否吞错。

---

## 7. 当前验证状态（截至 2026-07-09）

- **markdown 渲染**：真机触发 TaskComplete → `auto-open-managed-agents-757` 群收到带 markdown 的交互卡片，加粗 / 行内代码 / 链接 / 代码块 / 引用 / 标题均正常渲染。
- **thread 归并**：对同一 cwd 触发两次 TaskComplete，日志证实首次建 thread 根、第二次的 `replyTo` 精确指向首次的根消息 id → 归并到同一段 thread，不再平铺主时间线。
- **已知小限制**：`threadRoots`（记录各群 thread 根 id）是**内存** map，守护进程重启后会丢弃 → 重启后首次群话题会再建一个根（重启不频繁，可接受；如需跨重启持久化可存入 `projects.json`）。

---

## 8. 后续可选工作（未做）

1. **thread 根跨重启持久化**：把根消息 id 存入 `projects.json`，daemon 重启后接着挂回旧根，thread 永远连续。
2. 本对比文档即「可选 2」，已完成 ✅。
