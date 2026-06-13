#!/usr/bin/env node
// claude-acp 后端真机探针：spawn 一个真实 ACP server（默认 claude-code-acp），
// 用与 src/agent/acp/backend.ts 完全相同的 client 姿态（fs/terminal 全 false、
// 权限自动 allow）走完 initialize → session/new → session/prompt → stopReason
// 一整轮流式。手动运行，不进 vitest（vitest 只收 test/**/*.test.ts）；mock 全链路
// 单测见 test/acp-backend.test.ts。
//
// ⚠️ 真跑会向 ACP server 背后的 Claude 发一条最小消息（花订阅用量），所以默认
// 跳过 —— 必须显式开环境变量：
//
//   ACP_SMOKE=1 node test/probe-acp.mjs                         # PATH 上的 claude-code-acp
//   ACP_SMOKE=1 node test/probe-acp.mjs node /path/claude-code-acp/dist/index.js
//   ACP_SMOKE=1 ACP_SMOKE_CMD='node /path/dist/index.js' node test/probe-acp.mjs
//   ACP_SMOKE_CWD=/path/to/workdir  # 可选，默认 os.tmpdir()

import { tmpdir } from 'node:os';
import { Readable, Writable } from 'node:stream';
import spawn from 'cross-spawn';
import * as acp from '@agentclientprotocol/sdk';

if (process.env.ACP_SMOKE !== '1') {
  console.log('probe-acp: 默认跳过（会花一条订阅消息）。要真跑：ACP_SMOKE=1 node test/probe-acp.mjs [command args…]');
  process.exit(0);
}

const argvCmd = process.argv.slice(2);
const envCmd = (process.env.ACP_SMOKE_CMD ?? '').split(/\s+/).filter(Boolean);
const [cmd, ...args] = argvCmd.length ? argvCmd : envCmd.length ? envCmd : ['claude-code-acp'];
const CWD = process.env.ACP_SMOKE_CWD || tmpdir();
const PROMPT = 'reply with exactly: pong';

const t0 = Date.now();
const ts = () => `+${String(Date.now() - t0).padStart(6)}ms`;

console.log(`${ts()} spawning ACP server: ${cmd} ${args.join(' ')}`);
const child = spawn(cmd, args, { stdio: ['pipe', 'pipe', 'pipe'] });
child.on('error', (err) => {
  console.error(`${ts()} spawn failed: ${err.message}`);
  process.exit(1);
});
child.stderr.on('data', (d) => {
  const line = d.toString().trim();
  if (line) console.error(`${ts()} [server-stderr] ${line.slice(0, 200)}`);
});
process.on('exit', () => {
  try {
    child.kill('SIGTERM');
  } catch {}
});
// 全程死线：交互式 claude 启动 + 一轮最小回复，3 分钟绰绰有余
setTimeout(() => {
  console.error(`${ts()} ❌ overall timeout (180s)`);
  process.exit(1);
}, 180_000).unref();

// 与 backend.ts 同款 client：只实现必选两方法；权限自动 allow（full 档语义）
const client = {
  async sessionUpdate({ update }) {
    switch (update.sessionUpdate) {
      case 'agent_message_chunk':
        if (update.content.type === 'text') console.log(`${ts()} 💬 ${update.content.text}`);
        break;
      case 'agent_thought_chunk':
        if (update.content.type === 'text') console.log(`${ts()} 🤔 ${update.content.text.slice(0, 80)}`);
        break;
      case 'tool_call':
        console.log(`${ts()} 🔧 ${update.title} (${update.status ?? 'pending'})`);
        break;
      case 'tool_call_update':
        console.log(`${ts()} 🔧 ${update.toolCallId} -> ${update.status}`);
        break;
      default:
        console.log(`${ts()} ·  ${update.sessionUpdate}`);
    }
  },
  async requestPermission({ toolCall, options }) {
    const allow = options.find((o) => o.kind === 'allow_once') ?? options.find((o) => o.kind?.startsWith('allow'));
    console.log(`${ts()} 🔐 permission: ${toolCall?.title} -> auto "${allow?.name ?? 'cancelled'}"`);
    if (!allow) return { outcome: { outcome: 'cancelled' } };
    return { outcome: { outcome: 'selected', optionId: allow.optionId } };
  },
};

const stream = acp.ndJsonStream(Writable.toWeb(child.stdin), Readable.toWeb(child.stdout));
const conn = new acp.ClientSideConnection(() => client, stream);

const init = await conn.initialize({
  protocolVersion: acp.PROTOCOL_VERSION,
  clientInfo: { name: 'feishu-codex-bridge-probe', version: '0.0.0' },
  clientCapabilities: { fs: { readTextFile: false, writeTextFile: false }, terminal: false },
});
console.log(
  `${ts()} ✅ initialized: ${init.agentInfo?.name ?? '?'} v${init.agentInfo?.version ?? '?'} (protocol v${init.protocolVersion}, loadSession=${init.agentCapabilities?.loadSession ?? false})`,
);

const session = await conn.newSession({ cwd: CWD, mcpServers: [] });
console.log(`${ts()} 📝 session ${session.sessionId} ready (cwd=${CWD})`);

console.log(`${ts()} 💬 USER: ${PROMPT}`);
const res = await conn.prompt({ sessionId: session.sessionId, prompt: [{ type: 'text', text: PROMPT }] });
console.log(`${ts()} ✅ turn complete: stopReason=${res.stopReason}`);
process.exit(res.stopReason === 'end_turn' ? 0 : 1);
