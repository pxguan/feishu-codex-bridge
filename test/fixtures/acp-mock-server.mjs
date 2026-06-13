#!/usr/bin/env node
// Mock ACP server（test/acp-backend.test.ts 专用）：零依赖 NDJSON JSON-RPC over
// stdio，按 ACP 形状应答 initialize / session/new / session/load / session/prompt /
// session/cancel，并能反向发起 session/request_permission。行为开关走 argv：
//   --no-loadsession   initialize 不宣告 loadSession 能力（测 resumeThread 降级）
//   --permission       prompt 前先发权限请求，按客户端的决定回显 approved:yes/no
// prompt 文本协议：含 ECHO → 原样回显收到的 prompt；含 CANCELME → 扣住响应直到
// 收到 session/cancel，再以 stopReason:cancelled 收尾；其余 → 固定 golden 序列。

const NO_LOADSESSION = process.argv.includes('--no-loadsession');
const PERMISSION = process.argv.includes('--permission');

let buffer = '';
let reqSeq = 9000;
const pendingOwnRequests = new Map(); // id -> resolve(result)
let pendingCancel = null; // CANCELME 模式下挂起的收尾

const write = (obj) => process.stdout.write(`${JSON.stringify(obj)}\n`);
const respond = (id, result) => write({ jsonrpc: '2.0', id, result });
const notify = (sessionId, update) =>
  write({ jsonrpc: '2.0', method: 'session/update', params: { sessionId, update } });
const chunk = (text) => ({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } });

const request = (method, params) => {
  const id = ++reqSeq;
  write({ jsonrpc: '2.0', id, method, params });
  return new Promise((resolve) => pendingOwnRequests.set(id, resolve));
};

async function handle(msg) {
  // 对我们自己发出的请求（request_permission）的响应
  if (msg.id !== undefined && pendingOwnRequests.has(msg.id)) {
    pendingOwnRequests.get(msg.id)(msg.result);
    pendingOwnRequests.delete(msg.id);
    return;
  }
  switch (msg.method) {
    case 'initialize':
      respond(msg.id, {
        protocolVersion: msg.params.protocolVersion,
        agentInfo: { name: 'mock-acp', version: '9.9.9' },
        agentCapabilities: { loadSession: !NO_LOADSESSION },
      });
      return;
    case 'session/new':
      respond(msg.id, { sessionId: 'mock-sess-1' });
      return;
    case 'session/load': {
      // 回放历史（client 应在无 active turn 时丢弃这些通知）
      const sid = msg.params.sessionId;
      notify(sid, { sessionUpdate: 'user_message_chunk', content: { type: 'text', text: '历史用户消息' } });
      notify(sid, chunk('历史回复'));
      respond(msg.id, {});
      return;
    }
    case 'session/prompt':
      await handlePrompt(msg.id, msg.params);
      return;
    case 'session/cancel': // 通知，无 id
      if (pendingCancel) {
        const fin = pendingCancel;
        pendingCancel = null;
        fin();
      }
      return;
    default:
      if (msg.id !== undefined) {
        write({ jsonrpc: '2.0', id: msg.id, error: { code: -32601, message: `method not found: ${msg.method}` } });
      }
  }
}

async function handlePrompt(id, params) {
  const sid = params.sessionId;
  const text = (params.prompt ?? []).map((b) => b.text ?? '').join('');

  if (PERMISSION) {
    const res = await request('session/request_permission', {
      sessionId: sid,
      toolCall: { toolCallId: 'tc-perm', title: 'rm -rf /tmp/x' },
      options: [
        { optionId: 'allow-1', name: 'Allow once', kind: 'allow_once' },
        { optionId: 'reject-1', name: 'Reject', kind: 'reject_once' },
      ],
    });
    const allowed = res?.outcome?.outcome === 'selected' && res.outcome.optionId === 'allow-1';
    notify(sid, chunk(allowed ? 'approved:yes' : 'approved:no'));
    respond(id, { stopReason: 'end_turn' });
    return;
  }

  if (text.includes('ECHO')) {
    notify(sid, chunk(text));
    respond(id, { stopReason: 'end_turn' });
    return;
  }

  if (text.includes('CANCELME')) {
    notify(sid, chunk('working…'));
    pendingCancel = () => respond(id, { stopReason: 'cancelled' });
    return;
  }

  // golden 序列：思考 → 工具调用（开始/完成）→ 两段文本 → 用量 → end_turn
  notify(sid, { sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: '先看看目录' } });
  notify(sid, {
    sessionUpdate: 'tool_call',
    toolCallId: 'tc-1',
    title: 'ls -la',
    kind: 'execute',
    status: 'in_progress',
  });
  notify(sid, {
    sessionUpdate: 'tool_call_update',
    toolCallId: 'tc-1',
    status: 'completed',
    content: [{ type: 'content', content: { type: 'text', text: 'README.md' } }],
  });
  notify(sid, chunk('你好'));
  notify(sid, chunk('，世界'));
  notify(sid, { sessionUpdate: 'usage_update', used: 1234, size: 200000 });
  respond(id, { stopReason: 'end_turn' });
}

process.stdin.setEncoding('utf8');
process.stdin.on('data', (d) => {
  buffer += d;
  let nl;
  while ((nl = buffer.indexOf('\n')) !== -1) {
    const line = buffer.slice(0, nl).trim();
    buffer = buffer.slice(nl + 1);
    if (!line) continue;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      continue;
    }
    handle(msg).catch((e) => console.error('mock-acp error:', e));
  }
});
process.stdin.on('end', () => process.exit(0));
