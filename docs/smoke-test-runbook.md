# 真实飞书 + Codex / Claude 端到端冒烟 Runbook

> 目标：在**隔离环境**里把 Go 重写后的桥跑一轮真实飞书，验证 `run` 主线端到端打通
> （建群 → @bot → 流式 Markdown 卡片 → 工具调用 → ⏹ 中断 → resume）。
>
> 适用：Phase 1（Codex 主线）+ 刚移植的 Claude 后端（Go）。
> 约束：**沙箱内无飞书凭据、且本机有正在跑的生产 bridge**，因此真机冒烟必须由你在
> 一台隔离的 bot/app 上手动执行。本文件即为可执行步骤；离线预检见 `scripts/smoke-preflight.sh`。

---

## 0. 为什么必须隔离（先读）

- 桥有**进程级单实例锁**，按 `appId` 维度落在 `~/.feishu-codex-bridge/<appId>.lock`。
  同一 `appId` 不允许两个进程同时 `run`。
- 配置目录硬编码为 `~/.feishu-codex-bridge`，**无 env 覆盖**。
- 因此：若用与生产相同的 `HOME` 跑冒烟，会撞生产锁、并复用生产凭据。
- **唯一干净做法**：用**独立测试飞书应用** + **独立 HOME**（或独立用户）跑冒烟。
  生产那边完全不动。

```bash
# 隔离 HOME：所有配置/锁/密钥都落在 /tmp/fcb-smoke，绝不碰 ~/.feishu-codex-bridge
export HOME=/tmp/fcb-smoke
mkdir -p "$HOME"
```

---

## 1. 前置条件（隔离环境里准备）

| 项 | 检查 | 命令/动作 |
|---|---|---|
| Go 二进制 | 已构建 | `make build` → 产出 `./feishu-codex-bridge` |
| Codex CLI | 已登录 | `codex --version` 且 `codex login` 已完成（~/.codex 有效） |
| Claude CLI（可选，验 Claude 后端时） | 已登录 | `claude --version` 且 `claude` 已登录（~/.claude 有效） |
| 测试飞书应用 | 有独立 appId/appSecret | 在飞书开放平台新建一个**测试应用**，开「机器人」能力、开通 `im:message`、`im:message:send_as_bot` 等权限，拿到 appId/appSecret |
| 事件订阅 | 能接收卡片回调 | 测试应用配「事件订阅」+「卡片回调」指向你的公网/内网可达地址（或用飞书本地调试隧道） |

> 安全提示：测试应用的 appSecret 切勿与生产应用相同；建议直接用飞书「测试企业」或独立工作区。

---

## 2. 离线预检（无需飞书凭据，先跑这个）

```bash
bash scripts/smoke-preflight.sh
```

它会依次校验：
1. `go build ./...` / `go vet ./...` 通过；
2. `go test ./...` 全绿；
3. 二进制 `doctor` 能探到 Codex（以及 Claude，若已登录）；
4. 隔离 HOME 下**没有**被其它进程持有的生产锁（避免误撞）。

预检全绿再进第 3 步。

---

## 3. 注册测试 bot 并自检

```bash
# 在隔离 HOME 下
export HOME=/tmp/fcb-smoke
./feishu-codex-bridge bot init     # 交互式填入测试 appId/appSecret，设为 active
./feishu-codex-bridge bot list     # 确认已注册且 active
./feishu-codex-bridge doctor       # ✅ Codex 可用；列出已注册 bot
```

---

## 4. 前台 run + 走查清单

```bash
export HOME=/tmp/fcb-smoke
./feishu-codex-bridge run          # 连飞书长连接；建群后 @bot 即可触发
```

打开飞书，按下面清单逐项 ✅（建议边跑边勾）：

- [ ] **建群**：在飞书建一个群，把测试机器人拉进群。
- [ ] **@bot 触发**：在群里 `@测试机器人 帮我看下 /tmp/demo 里有哪些文件`。
  - [ ] 群里立刻出现一张「运行中」Markdown 卡片（turn_started）。
  - [ ] 卡片**流式**更新：text 增量、thinking 块实时追加。
- [ ] **工具调用**：让 bot 执行一个会调工具的任务（如「用 grep 找 xxx」）。
  - [ ] 卡片出现工具行：`读取 xxx` / `Shell 命令 xxx`，并显示结果。
- [ ] **用量/上下文**：卡片底部出现 token 用量 + 上下文窗口百分比。
- [ ] **⏹ 中断**：点卡片上的「停止」按钮（卡片 action）。
  - [ ] 底层 Codex/Claude 子进程被 kill（进程树清理，不残留）。
  - [ ] 卡片状态变为「已中断」。
- [ ] **resume**：接着发「继续」，验证能复用上一轮上下文（会话续上，非全新）。
- [ ] **（Claude 后端）** 把项目后端切到 claude-agent：
  - [ ] `bot` 里把项目后端选 `claude-agent`，或新建项目时选 Claude；
  - [ ] 重复上面 @bot → 流式 → 工具 → 中断 → resume 全链路。

---

## 5. 收尾 / 清理

```bash
# 退出 run：Ctrl+C（已处理 SIGINT/SIGTERM 优雅关闭并释放锁）
# 清理隔离 HOME（彻底与生产解耦）
rm -rf /tmp/fcb-smoke
```

---

## 6. 失败排查速查

| 现象 | 可能原因 | 处理 |
|---|---|---|
| `飞书连接失败` | appSecret 错 / 事件订阅未配 | 重跑 `bot init`；检查开放平台「事件订阅」 |
| `single-instance: already running` | 撞到同 appId 的另一个进程 | 确认 `HOME` 已隔离；`ps` 查占用进程；不要在真实 HOME 跑 |
| 卡片不更新 / 卡在「运行中」 | Codex CLI 未登录或崩了 | `codex --version`；看 `doctor`；查 `~/.feishu-codex-bridge/logs` |
| 中断后进程残留 | kill-tree 失败（仅 Windows 已知风险） | `ps` 查 `codex`/`claude` 子进程手动清理 |
| Claude 后端报「未找到 claude CLI」 | 未装/未登录 Claude Code | `npm i -g @anthropic-ai/claude-code` 并 `claude` 登录 |

---

## 7. 验收结论怎么记

全部勾完即视为「Phase 1 + Claude 后端」在真实飞书环境**通过验证**。
此前代码层已完成（编译/vet/单测全绿），本步骤填补的是**唯一残留的验证缺口**：
> 没有在真实飞书群里跑过一轮 Codex/Claude。

记一笔到 `docs/` 或项目 memory：日期、测试 appId、走查结果、是否发现缺陷。
