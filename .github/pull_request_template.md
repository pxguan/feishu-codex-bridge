<!--
感谢贡献 feishu-codex-bridge！

开 PR 前:
- 读 AGENTS.md 了解仓库约定与构建/测试命令。
- 跑下方 Verification 清单（对应你改动的 area）。
- 用 Conventional Commits 标题（feat:/fix:/refactor:/chore:/docs:/test:/perf:），
  scope 可用 area（feishu/bot/card/...）或 feature，自由选择。
- 安全问题请勿开公开 PR，走 GitHub Security Advisories。
-->

## Summary

<!--
1-3 句描述本 PR 做了什么、为什么。
关联 issue: "Closes #123" / "Refs #456"。
-->

Closes #

## Type of change

- [ ] feat — 新功能
- [ ] fix — bug 修复
- [ ] refactor — 无用户可见行为变化的内部改动
- [ ] perf — 性能改进
- [ ] docs — 仅文档
- [ ] test — 仅测试
- [ ] chore / ci — 构建、工具、依赖、CI
- [ ] breaking change（请在下方说明迁移）

## Affected components

<!-- 勾选影响的 area，见 AGENTS.md「目录入口速查」。 -->
- [ ] `area: agent` — `internal/agent/`（含 `claude/`、`codex/`）
- [ ] `area: bot` — `internal/bot/` + `project/` + `clibridge/`
- [ ] `area: card` — `internal/card/`
- [ ] `area: feishu` — `internal/feishu/`（飞书 SDK 隔离层）
- [ ] `area: infra` — `internal/{config,daemon,service,platform,update,utils,admin,core}/`
- [ ] `area: cli` — `internal/cli/` + `cmd/`
- [ ] `area: web` — `internal/web/`
- [ ] `area: ci` — `.github/` + `scripts/` + `Makefile`
- [ ] `area: docs` — `README.md` / `AGENTS.md` / `docs/`

## Implementation notes

<!--
reviewer 需要知道的:
- 设计决策与权衡。
- 故意变大 / 拆分的文件。
- 隐含不变量、并发、性能考量。
- 新依赖（说明引入理由）。
- TS 对照: 本仓每个 .go 文件头标注「对齐 TS xxx.ts」，若有对照文件可在此列出。
-->

## Verification

<!--
贴出本地跑过且通过的命令。按改动 area 选 applicable 的行，删其余。
注: make lint = go vet + gofmt 校验（非 golangci-lint）。
-->

```bash
make vet              # go vet ./...
make lint             # vet + gofmt 校验（不改文件，失败 exit 1）
make fmt              # gofmt -s -w .（会改文件）
make test             # go test ./...
make test-race        # 带竞态检测（改了并发逻辑时必跑）
make build            # 构建二进制（注入版本号）
```

## Tests added / updated

- [ ] 新增/更新单测（`*_test.go`，与源码同包同目录）
- [ ] 无需测试，因为: <!-- 例如 docs-only / config-only -->

## Backward compatibility

- [ ] 向后兼容（对现有用户无 breaking change）
- [ ] 需要用户可见迁移 / 重新授权（在下方说明）

## Security & privacy

- [ ] 未提交新的 secrets / API key。
- [ ] 未记录 PII；敏感字段已脱敏。
- [ ] 新增的外部网络调用（如有）已在上方说明目的地与用途。
- [ ] 飞书 SDK / OpenAPI 调用的 error 未用 `_` 吞（本仓硬约定）。
- [ ] `.gitignore` 仍覆盖新产生的产物。

## Reviewer checklist

- [ ] 标题遵循 Conventional Commits。
- [ ] PR 聚焦——无关清理拆成单独 PR。
- [ ] 本地 `make lint` + `make test` 通过（本仓无 CI 门禁，靠自觉）。
- [ ] 若用户可见行为或开发者命令变化，已更新 `README.md` / `AGENTS.md`。
