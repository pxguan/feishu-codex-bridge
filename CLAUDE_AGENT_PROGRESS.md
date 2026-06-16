# Claude Agent SDK 后端 —— 过夜自治进度

> 这是跨循环的唯一记忆。每轮开工先读这里 + `git log --oneline -15` + `git status`。
> 状态标记：未到 ALL DONE 前不要停。所有「完成」必须有工具结果佐证。

## 当前状态：代码实现完成，待飞书端到端验收（第 1 轮）

### 已完成（有工具结果佐证）
- ✅ 安装 `@anthropic-ai/claude-agent-sdk@0.3.178`（已进 package.json deps）。
- ✅ 实现 `src/agent/claude-agent/{event-map,permission,thread,backend}.ts`。
- ✅ 注册：`src/agent/index.ts` REGISTRY + `src/agent/catalog.ts` BACKEND_CATALOG（id 'claude-agent'）。
- ✅ `npm run typecheck` 绿 / `npm run build` 绿 / `npm test` 706 passed（更新了两处 codex-only 快照测试为「codex+claude」新现实，并给 claude-agent 补了正向断言）。
- ✅ 三个 spike 实证：鉴权可起、流式 delta、工具/思考形状、resume 跨进程上下文、interrupt 后 query 可续用。

### 下一步（飞书端到端，需真实群 + computer use）
1. 起 bridge（npm start，或复用已运行实例），建测试群「【CC验收】Claude」，把项目后端设为 claude-agent。
2. 逐项验收清单（见下），computer use 截图佐证卡片流式/思考/工具/⏹。
3. 把每项结果勾进下面清单；全绿后在本文件顶部写 ALL DONE + 验收说明。



---

## 目标
新增 `claude-agent` 后端，让飞书机器人能像 Codex 后端一样跑 Claude Code，体验与安全尽量一致。
不改任何基础设施逻辑（run-card-stream / run-state / watchdog / session-store / handle-message）——
只实现 `src/agent/claude-agent/{backend,thread,event-map}.ts` 并注册。

## 环境事实（第 1 轮实测）
- SDK **未安装**：`@anthropic-ai/claude-agent-sdk` 不在 deps，也不在 node_modules。→ 本轮安装。
- claude CLI 在：`2.1.178`，但 PATH 里是 shell 函数 `safe-claude-wrapper.sh`（不是裸二进制）。
  Agent SDK 自带 cli.js，默认不依赖全局 claude；若需要可用 `pathToClaudeCodeExecutable` 指定。
- `ANTHROPIC_API_KEY` 未设；`~/.claude/.credentials.json` **不存在** → 凭据疑在 macOS Keychain。
  **鉴权能否起来必须用最小 spike 实跑验证**（这是头号风险）。

## 关键 API 事实（调研，待 spike 实证）
- 入口是 `import { query } from '@anthropic-ai/claude-agent-sdk'`，`query({ prompt, options }) => Query`。
  （注意：调研里出现的 `new Agent().query()` 写法与官方 `export function query` 矛盾，以真实 .d.ts 为准。）
- `Query` 继承 `AsyncGenerator<SDKMessage, void>`，可 `for await`；有 `interrupt()`、`setModel()`、
  `setPermissionMode()`、`initializationResult()`。
- 多轮：`prompt` 传 `AsyncIterable<SDKUserMessage>`（流式输入），持续 yield 新 user 消息。
  `SDKUserMessage = { type:'user', message: MessageParam, parent_tool_use_id: string|null, ... }`。
- 恢复：`options.resume = '<sessionId>'`（与 `continue` 互斥）。sessionId 取自 system(init) 或 result 消息的 `session_id`。
- 中断：`query.interrupt()`（仅流式输入模式可用）或 `options.abortController`。中断后 query 不可续用 → 需重建。
- 增量：`options.includePartialMessages = true` → 收到 `{ type:'stream_event', event: BetaRawMessageStreamEvent }`，
  text delta 在 `content_block_delta` 的 `delta.type==='text_delta'`，thinking delta 同理。
- 结果：`{ type:'result', subtype:'success'|..., usage, total_cost_usd, session_id, ... }`。
- 权限：`options.permissionMode ∈ default|acceptEdits|bypassPermissions|plan|dontAsk|auto`；
  `canUseTool(toolName, input, opts) => {behavior:'allow',updatedInput?}|{behavior:'deny',message}`；
  `allowedTools/disallowedTools: string[]`（Bash 不能按命令细分，需在 canUseTool 里看 input）。
- 沙箱：`options.sandbox: SandboxSettings { enabled, filesystem:{allowRead,allowWrite,denyRead,denyWrite},
  network:{allowedDomains,...}, failIfUnavailable, bwrapPath }` —— **SDK 直接暴露 OS 级沙箱**（macOS / Linux bwrap）。
  这点若属实，安全可与 Codex 对齐，是首选。**必须 spike 实证 sandbox 真的生效**。
- 模型/深度：`options.model`、`options.effort ∈ low|medium|high|xhigh|max`、`options.thinking`。
- 目录：`options.cwd`、`options.additionalDirectories`。
- 鉴权：自动复用本机已登录凭据，否则 `ANTHROPIC_API_KEY`，或 `options.env`。

## 契约要点（来自 types.ts，必须遵守）
- 实现 `AgentBackend`（id/displayName/capabilities/supportedModes/isAvailable/doctor/listModels/
  listThreads/readHistory/startThread/resumeThread）和 `AgentThread`（sessionId/runStreamed/runGoal/
  clearGoal/steer/abort/compact/isAlive/close）。
- `runStreamed` 返回 `AgentRun { events: AsyncIterable<AgentEvent>, turnId(), lastActivity?() }`。
  `lastActivity()` 必须在**每条** raw SDK 消息上刷新（含被 event-map 丢弃的），watchdog 靠它区分忙/挂死。
- event-map：把 SDK 消息映射成 `AgentEvent`（system/turn_started/text_delta/text/thinking_delta/
  thinking/tool_use/tool_result/usage/context_usage/context_compacted/done/error）。未知事件返回 null。
- 注册两处且必须一致：`src/agent/index.ts` 的 REGISTRY + `src/agent/catalog.ts` 的 BACKEND_CATALOG
  （有测试断言两者 id 集合相等）。
- 不支持的能力用 `capabilities` 声明 false，且对应方法必须抛清晰「不支持」错误（不可静默半实现）。
- 权限档 fail-closed：`supportedModes` 只是 UI 预拦截，硬守卫永远在 startThread/resumeThread。

## 安全/沙箱决策（待定，spike 后敲定）
- 首选：用 SDK `options.sandbox` 的 OS 级隔离对齐 Codex 三档：
  - full → sandbox 关闭 + permissionMode 'bypassPermissions'（danger-full-access 等价）
  - write → sandbox.enabled，filesystem.allowWrite=[cwd]，network 按开关
  - qa → sandbox.enabled，只读（无 allowWrite 或 denyWrite 全部），permissionMode 限制写工具
- 若 sandbox 实测不生效/不可用：退化为 canUseTool + allowedTools/disallowedTools 软边界，
  **并在 PROGRESS 和代码注释如实标注「弱于 Codex 的 OS 沙箱」**，绝不假装一致。
- Linux 与 Codex 一致 fail-closed（非 darwin/win 的受限档若沙箱不可用则拒绝启动）。

## 验收清单
- [ ] claude-agent 后端注册成功、可被选中切换
- [ ] 新建会话 + 跑一轮，卡片流式增量更新（文本 delta + 思考 + 工具块），观感同 Codex
- [ ] 多轮上下文保持（追问能记住上一轮）
- [ ] 重启 bridge 后能 resume 恢复会话
- [ ] ⏹ 停止按钮能优雅中断正在运行的一轮
- [ ] 三档权限切换行为正确，沙箱/安全说明完整
- [ ] 指定 model + effort 生效，卡片模型显示正常
- [ ] npm run typecheck / build / test 全绿
- [ ] 在真实飞书群里端到端验证，并用 computer use 截图/肉眼确认卡片

## 下一步（按序）
1. 安装 `@anthropic-ai/claude-agent-sdk`，读真实 .d.ts 锁定 API。
2. 写最小 spike（scripts 或临时 .mjs）：实跑一次 query，验证 **鉴权能起来**、拿到 session_id、
   收到 stream_event 增量、interrupt 生效、sandbox 选项生效。把结论写回这里。
3. 实现 event-map.ts → thread.ts → backend.ts；注册 index.ts + catalog.ts。
4. typecheck/build/test 绿。
5. 飞书端到端 + computer use 验收。

## Spike 实证结论（第 1 轮，node _spike-claude.mjs）
- ✅ **鉴权无需 API key**：`apiKeySource=none` 仍成功出文（$0.189）。SDK 自带 cli.js 复用本机登录态
  （疑 Keychain）。`isAvailable`/`doctor` 不需要查 API key，跑一次轻量探测即可。
- ✅ 流式：`includePartialMessages:true` → `stream_event`：`message_start` → `content_block_start`
  (block.type) → `content_block_delta`(delta.type='text_delta'|'thinking_delta'|'input_json_delta')
  → `content_block_stop` → `message_delta` → `message_stop`。`idx` = content block index。
- ✅ 完整 `assistant` 消息（BetaMessage）随后到，`message.content[]` 是 {type:'text'|'thinking'|'tool_use',...}，
  tool_use 块有 `id`+`name`。text/thinking 块无 id → itemId 用 `${assistantMsgOrdinal}:${blockIndex}`。
- ✅ `result`：subtype/is_error/num_turns/total_cost_usd/usage(input_tokens,output_tokens,cache_*)/result(最终文本)。
- 噪声消息（map→null 但刷 lastActivity）：system/hook_started|hook_response|status、rate_limit_event。
- ⚠️ 冷启动延迟 ~22s（init→首 token）：进程 spawn + MCP + 大 system prompt 缓存创建。每轮 resume 新起进程都付此成本。
  → 体验对策待定：要么持久化 streaming-input query（一进程多轮，省冷启动），要么接受 per-turn + 加载提示。
  先用 **持久 streaming-input** 方案对齐 Codex 常驻进程模型（首选）。

## 架构决策（基于 spike）
- ClaudeThread = 一个常驻 `query()`（streaming-input：prompt 传可 push 的 AsyncIterable）。
  - startThread：起 query，后台循环消费消息；首个 system/init 给 session_id → resolve ready，再返回 thread。
  - runStreamed：push 一条 user 消息；后台路由把本轮消息映射成 AgentEvent 投到「当前轮 channel」，
    见到 `result` 即本轮 done。turns 串行（上一轮 result 后才开下一轮），单 channel 足够。
  - abort：`query.interrupt()`（仅 streaming-input 模式可用，spike3 验证中断后能否续用）。
  - resumeThread：query options.resume=sessionId，同样常驻。
  - steer：push 一条 user 消息（priority?）——能力位 steer 先标 false，spike 验证后再开。
  - compact：SDK 无直接「压缩」API → capabilities.compact=false（或走 /compact slash command，待查）。
  - runGoal：SDK 无 codex 式 goal → capabilities.goal=false，方法抛「不支持」。
  - listThreads/readHistory：SDK 有 listSessions/getSessionMessages（待查），先返回空 + capabilities.resume 视情况。

## 踩坑记录
（空）
