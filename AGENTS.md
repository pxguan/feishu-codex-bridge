# AGENTS.md

本文件为 AI 编码代理（Claude Code、Codex、Cursor 等）在本仓库工作时的基础指引。**动代码前先读完本文件。**

- 人类向导见 [`README.md`](README.md)。
- TypeScript → Go 重写背景与逐模块对照见 [`docs/ts-vs-go-overview.md`](docs/ts-vs-go-overview.md)。
- PR/提交规范、issue/label 用法见本文相应章节。

## 仓库概览

feishu-codex-bridge 把**飞书/Lark 桥接到本机 Codex / Claude**：群 = 项目 = cwd，话题 = 会话。群里 @机器人 即可让它在指定项目目录跑 Codex / Claude，结果以流式 Markdown 卡片实时回到群里。

- **语言 / 构建**：单 Go module `github.com/modelzen/feishu-codex-bridge`（`go 1.26`），**非** monorepo。
- **远端**：`origin = pxguan/feishu-codex-bridge`（fork，**协作主场**）/ `upstream = modelzen/feishu-codex-bridge`。
- **主开发分支**：`dev-go`（PR 默认 base）。
- **评审**：CodeRabbit（`.coderabbit.yaml`）+ alibaba/open-code-review（`.github/workflows/ocr-review.yml`）已就位，**勿重接**。
- **关键依赖**：`larksuite/oapi-sdk-go/v3`（飞书 SDK）、`spf13/cobra`（CLI）、`gopsutil/v3`、`golang.org/x/crypto`。无 golangci-lint。

## 构建命令

通过仓库根 [`Makefile`](Makefile)：

| 命令 | 作用 |
|---|---|
| `make build` | 构建二进制 `feishu-codex-bridge`（`-ldflags` 注入版本号） |
| `make run` | 前台跑 `run`（开发态） |
| `make test` | `go test ./...` |
| `make test-race` | `go test -race ./...`（改了并发逻辑时必跑） |
| `make vet` | `go vet ./...` |
| `make fmt` | `gofmt -s -w .`（**会改文件**） |
| `make lint` | `vet` + `gofmt -l` 校验（**非 golangci-lint**；不改文件，失败 `exit 1`） |
| `make coverage` | 生成 `coverage.html` |
| `make clean` | 清产物 |

> 本仓**无 CI 门禁 workflow**。提交前本地至少跑 `make vet fmt lint test` 自检。改完代码立即跑对应命令验证通过再回复用户。

## 目录入口速查

Go 主代码在 `internal/`（按职责域聚合为 9 个 `area`，与 [`.github/labels.yml`](.github/labels.yml) 一致）：

| area | 目录 | 职责 |
|---|---|---|
| `agent` | `internal/agent/`（含 `claude/`、`codex/`） | 后端中立 agent 协议（`AgentBackend`/`AgentThread`/`AgentEvent`）+ codex/claude 实现 |
| `bot` | `internal/bot/` `internal/project/` `internal/clibridge/` | 飞书编排 / 会话 / 项目注册表 / ☕ 咖啡一下反桥 |
| `card` | `internal/card/` | CardKit schema 2.0 卡片体系 |
| `feishu` | `internal/feishu/` | 飞书 OpenAPI wrapper（**业务层不直接碰 SDK 的隔离层**） |
| `infra` | `internal/{config,daemon,service,platform,update,utils,admin,core}/` | 配置 / 进程管理 / OS 服务 / 平台 / 更新 / 工具 / 管理 / 跨切面 |
| `cli` | `internal/cli/` `cmd/feishu-codex-bridge` | cobra 命令树 + 二进制入口 |
| `web` | `internal/web/` | 本机 Web 控制台 |
| `ci` | `.github/` `scripts/` `Makefile` | Actions / 脚本 / 构建 |
| `docs` | `docs/` `README.md` `AGENTS.md` | 文档 |

**⚠️ TS 重写基线（agent 勿改）**：`src/`（TypeScript 原版）与 `test/`（89 个 `*.ts` 测试）是 TS → Go 重写的对照基线，**仅供理解 Go 实现意图，不在其内落地任何改动**。`.coderabbit.yaml` 已用 `path_filters` 排除 `*.ts` 评审。本仓每个 `.go` 文件头注释都标注「对齐 TS xxx.ts」，改 Go 时可参考对应 TS 文件。

**易混淆点**：
- `internal/daemon`（进程生命周期：`start`/`stop`/`restart`）≠ `internal/service`（OS 级系统服务安装：`bot install`/`bot uninstall`）。
- `internal/core` 是跨切面基础设施（按天切日志 / version / PID 单实例锁 / stdin），**不含 agent 协议**——协议全在 `internal/agent/types.go`。

## CLI 要点

二进制 `feishu-codex-bridge`（cobra）。`--version` / `-v` 是 flag（**非**子命令）。

| 分组 | 子命令 | 用途 |
|---|---|---|
| 进程/守护 | `run` | 前台启动活跃机器人（含本机 Web，Ctrl+C 退出） |
| | `start` / `stop` / `restart` | 后台 daemon 启停重启（杀整棵进程树） |
| | `status` / `logs` | daemon 状态 / 日志（`-f` follow、`-n` 行数） |
| 更新/Web | `update` | 查 GitHub Releases 最新版（`--download` 仅下临时文件，不替换运行中二进制） |
| | `web` | 本机 Web 控制台（仅 127.0.0.1 + token） |
| 机器人管理 | `bot init` / `list` / `use` / `rm` | 注册 / 列出 / 勾选 / 移除机器人 |
| | `bot install` / `uninstall` / `service` | 注册 / 注销 / 查看 OS 系统服务（launchd / systemd） |
| 群枚举 | `chats list` / `sessions` | 列出群 / 持久化会话 |
| 杂项 | `doctor` | 本地自检（后端 / 登录 / 配置 / 权限） |
| | `send` | 给群发消息端到端自测（`--backend codex` 跑一轮回复发回） |
| 隐藏 | `hook` / `hooks` / `secrets` | ☕ 咖啡一下 hook 客户端 / hook 安装管理 / keystore exec-provider |

> 排查问题先 `doctor`；端到端自测用 `send --backend codex`。完整帮助 `feishu-codex-bridge --help`。

## 环境变量

Go 运行时（agent 应知）：

| 变量 | 作用 |
|---|---|
| `FEISHU_CODEX_BRIDGE=1` | **桥标记**。bridge spawn codex/claude 子进程时注入；咖啡一下 hook 用它判定「agent 进程是 bridge 拉起的」 |
| `FCB_UPDATE_REPO` | `update` / 运行卡更新检查的上游 repo 覆盖 |
| `CLAUDE_BIN` / `CODEX_BIN` | claude / codex 二进制路径覆盖 |
| `CODEX_HOME` | codex 配置目录覆盖（默认 `$HOME/.codex`） |
| `FCB_CLAUDE_MAX_RETRIES` / `FCB_CLAUDE_RETRY_BASE_DELAY` | claude `--resume` 网关瞬断指数退避重试参数 |

测试专用（默认 skip，opt-in）：`RUN_CLAUDE_ACCEPTANCE=1`（+ `CLAUDE_ACCEPTANCE_TIMEOUT/MODEL/DIR`）跑真 claude CLI 验收测试。

> **Go 不主动透传 `ANTHROPIC_*` / `CLAUDE_*`**——子进程用 `os.Environ()` 继承父进程全部 env（本机 claude/codex 登录态自然带过去），只额外加 `FEISHU_CODEX_BRIDGE=1`。不要在 Go 代码里新增 `ANTHROPIC_*` 透传逻辑。

## 约定与原则

**本仓硬约定（飞书桥特有）**：

1. **飞书 SDK / 任何 OpenAPI 调用的 error 绝不用 `_` 吞**——曾因此踩坑静默失败无日志。
2. **所有卡片走 CardKit 实体引用**（`SendCardByEntity` / `card_id`），不走内联 JSON。本 app 拒内联：对象 → `11310`，JSON 字符串 → `200621`。
3. **`im.message.create` 不支持 `msg_type:"markdown"`**（→ `230001`）。群话题 markdown 走 `interactive` 卡片的 `tag:"markdown"` 元素。
4. **业务层不直接 `import` Lark SDK**——全部经 `internal/feishu/` 隔离层。新增飞书调用走 feishu 包。
5. **Go 为主，`src/` 为基线**。落地只在 `internal/` + `cmd/`。
6. **`context` 取消/超时必须向下传**，不得 `context.Background()` 截断；`defer` 释放资源；凭证/token 写入原子化（临时文件 + rename）+ 敏感目录 `0700`。
7. **单进程模型**——`run` 一个进程跑所有活跃 bot，按 appId `core.AcquirePIDLock` 单实例。改编排/daemon 逻辑时勿引入「每 bot 一进程」假设。

**通用原则**：

- 修改即构建：改完立即跑 `make vet` / `lint` / `test` 验证。
- 不做多余防御：不加无意义 try、不写冗长注释、不引入向后兼容壳。
- 优先复用已有结构，而非新建抽象。
- 中文交流。

## 命名约定（Go）

遵循 [Effective Go](https://go.dev/doc/effective_go) 与 `gofmt`：

- **包名**：全小写、与目录一致、单个单词（`clibridge`，非 `cliBridge`）。
- **导出标识符**：`PascalCase`（`NewOrchestrator`、`AgentEvent`）；每个导出类型/函数配中文 doc comment（本仓既有风格）。
- **未导出标识符**：`camelCase`。
- **缩写统一大小写**：`URL`、`ID`、`HTTP`（`parseURL` / 导出版 `ParseURL`）。
- **接收者**：1–2 字母短名，全文件一致（`func (o *Orchestrator) ...`）。
- **错误处理**：不得 `_` 吞（呼应硬约定 1）。
- **文件头注释**：每个 `.go` 文件头标注「对齐 TS xxx.ts」（本仓强约定，新增文件照做）。
- **测试文件**：`*_test.go`，与源码同包同目录。
- **提交信息**：Conventional Commits（`feat:` / `fix:` / `refactor:` / `chore:` / `docs:` / `test:` / `perf:`）；**scope 自由**——可用 area（`feishu`/`bot`/`card`/`cli`/`web`/…）也可用 feature；area 的真源是 PR label，不强求 scope 与 label 一致。
- **分支**：feature 分支 off `dev-go`，PR 回 `dev-go`。

## 测试

```bash
make test           # go test ./...（全量）
make test-race      # 带竞态检测（并发改动必跑）
make coverage       # 出 coverage.html
```

- 新测试写成 `*_test.go`，与源码同包同目录。
- **不在 `test/`（TS 测试遗留）下加 Go 测试**。
- 验收 / live 测试默认 `t.Skip`，需 `RUN_CLAUDE_ACCEPTANCE=1` 且本机装了 claude/codex CLI 才跑（沙箱/CI 无凭据无网络跑不了）。
- 依赖凭据/飞书网络的逻辑（如 `send`、建群）用**纯函数剥离**便于单测（本仓既有模式：纯函数 port 到 `project`/`card`，SDK 调用在 `feishu`，分别测）。

## PR / 提交

- 提交信息用 Conventional Commits（scope 自由，见「命名约定」）。
- 通过 `gh pr create` 开 PR；PR base = `dev-go`；未经确认不要 merge 或 force push。
- 关联 issue：`Closes #N` / `Fixes #N` / `Refs #N`。小型 typo / docs-only / test-only 可不关联，但 PR body 说明上下文。
- PR 模板见 [`.github/pull_request_template.md`](.github/pull_request_template.md)。
- **本仓无 CI 门禁**，`make lint` + `make test` 是本地自觉前置。
- CodeRabbit + OCR 自动评审，PR 评论里可 `@coderabbitai` 对话。
- 安全漏洞不开普通 issue / PR，走 [GitHub Security Advisories](https://github.com/pxguan/feishu-codex-bridge/security/advisories/new)。
- 单一维护者 `@pxguan`，[`.github/CODEOWNERS`](.github/CODEOWNERS) 会自动 request review。

## Issue / PR 标签

- Label 列表是源码化配置，唯一来源是 [`.github/labels.yml`](.github/labels.yml)；**不要直接在 GitHub UI 改 label**，改 `labels.yml` 后由 [`.github/workflows/labels-sync.yml`](.github/workflows/labels-sync.yml) 自动同步。
- Label 命名 `<group>: <slug>`：`type:`（工作类型）/ `priority:`（P0–P3）/ `area:`（9 职责域）/ `status:`（流转）/ `needs:`（阻塞）+ `agent: ready` 等可发现性。
- 新建 issue 至少加一个 `type:` 和一个 `area:`；bug / agent-task 还应加 `priority:`。
- **agent 领取优先看 `agent: ready` + `status: ready`**，再按 `priority:` 和 `area:` 过滤。
- agent 任务用 [`.github/ISSUE_TEMPLATE/agent_task.yml`](.github/ISSUE_TEMPLATE/agent_task.yml) 模板，标题前缀 `[agent]:`，默认带 `type: agent-task` + `status: ready`。

---

> 本文件参考 [superduck-ai/superduck](https://github.com/superduck-ai/superduck) 的 AGENTS.md 协作体系，按本仓 Go 单 module 形态本地化裁剪。
