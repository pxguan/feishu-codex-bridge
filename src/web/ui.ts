/**
 * Web 控制台前端 —— 单文件内嵌 HTML（零依赖、零构建）。
 *
 * 为什么是 .ts 模板字符串而不是独立 ui.html：tsup 打包 / tsc / vitest 三方都
 * 无需任何额外配置（.html 文件要么得加 esbuild loader + d.ts，要么得改 vitest
 * 配置），这是"实现最干净的一种"。约定：内嵌的 client JS **不用反引号、不用
 * ${}**（全部字符串拼接），外层模板字符串就无需任何转义。
 *
 * UI 风格贴飞书 DM 卡片：卡片块 + 圆角 + 标签 + 蓝主按钮（#3370ff），中文文案与
 * src/card/dm-cards.ts 同款 emoji 标签（🧠 后端 / 🔐 权限 / 🩺 诊断 / ✋ 免@ /
 * 🗜️ 自动压缩 / 🧵 话题 / 👥 多话题群 / 💬 单会话群）。
 */
export const UI_HTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Codex Bridge 管理台</title>
<style>
  :root {
    --blue: #3370ff;
    --blue-dark: #245bdb;
    --bg: #f2f3f5;
    --card: #ffffff;
    --border: #e5e6eb;
    --text: #1f2329;
    --text-2: #646a73;
    --green: #34c724;
    --orange: #ff8800;
    --red: #f54a45;
    --radius: 12px;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--text);
    font: 14px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
      "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  }
  .wrap { max-width: 1180px; margin: 0 auto; padding: 20px 16px 48px; }
  .topbar {
    background: var(--blue); color: #fff; border-radius: var(--radius);
    padding: 14px 20px; display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
  }
  .topbar h1 { font-size: 17px; margin: 0; font-weight: 600; }
  .topbar .sub { font-size: 12px; opacity: .85; }
  .bot-tabs { margin-left: auto; display: flex; gap: 8px; flex-wrap: wrap; }
  .bot-tab {
    border: 1px solid rgba(255,255,255,.55); color: #fff; background: transparent;
    border-radius: 999px; padding: 3px 14px; font-size: 13px; cursor: pointer;
  }
  .bot-tab.on { background: #fff; color: var(--blue); font-weight: 600; border-color: #fff; }
  .cols { display: grid; grid-template-columns: minmax(0, 7fr) minmax(0, 5fr); gap: 16px; margin-top: 16px; }
  @media (max-width: 900px) { .cols { grid-template-columns: 1fr; } }
  .card {
    background: var(--card); border: 1px solid var(--border); border-radius: var(--radius);
    padding: 16px 18px; margin-bottom: 16px;
  }
  .card h2 { font-size: 15px; margin: 0 0 10px; display: flex; align-items: center; gap: 8px; }
  .card h2 .right { margin-left: auto; font-weight: 400; }
  .hr { border: 0; border-top: 1px solid var(--border); margin: 10px 0; }
  .note { color: var(--text-2); font-size: 12px; }
  .tag {
    display: inline-block; border-radius: 4px; padding: 0 6px; font-size: 12px;
    background: #eff0f1; color: var(--text-2); margin-right: 6px; white-space: nowrap;
  }
  .tag.blue { background: #e1eaff; color: var(--blue-dark); }
  .tag.green { background: #d9f5d6; color: #2ea121; }
  .tag.orange { background: #feead2; color: #b25e00; }
  .tag.red { background: #fde2e2; color: #c02a26; }
  .btn {
    display: inline-block; border-radius: 6px; border: 1px solid var(--border);
    background: #fff; color: var(--text); padding: 4px 14px; font-size: 13px;
    cursor: pointer; transition: filter .12s;
  }
  .btn:hover { filter: brightness(.96); }
  .btn.primary { background: var(--blue); border-color: var(--blue); color: #fff; }
  .btn.disabled { opacity: .5; cursor: not-allowed; }
  .proj { padding: 10px 0; border-bottom: 1px solid var(--border); }
  .proj:last-child { border-bottom: 0; }
  .proj .name { font-weight: 600; font-size: 14px; }
  .proj .meta { margin: 4px 0 8px; }
  .proj .path { color: var(--text-2); font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .statline { display: flex; align-items: center; gap: 8px; margin: 6px 0; flex-wrap: wrap; }
  #logbox {
    background: #0e1117; color: #c9d1d9; border-radius: 8px; padding: 10px 12px;
    font: 11.5px/1.55 ui-monospace, SFMono-Regular, Menlo, monospace;
    height: 480px; overflow-y: auto; white-space: pre-wrap; word-break: break-all;
  }
  #logbox .hl { color: #79c0ff; }
  #logbox .warn { color: #e3b341; }
  #logbox .err { color: #ff7b72; }
  .drawer-mask { position: fixed; inset: 0; background: rgba(0,0,0,.35); display: none; z-index: 9; }
  .drawer {
    position: fixed; top: 0; right: -460px; width: 440px; max-width: 94vw; height: 100vh;
    background: var(--card); z-index: 10; box-shadow: -8px 0 24px rgba(0,0,0,.12);
    transition: right .2s ease; padding: 18px 20px; overflow-y: auto;
  }
  .drawer.open { right: 0; }
  .drawer-mask.open { display: block; }
  .drawer h3 { margin: 0 0 4px; font-size: 16px; }
  .opt-row { display: flex; gap: 8px; margin: 6px 0 2px; flex-wrap: wrap; }
  .backend-row { display: flex; align-items: center; gap: 8px; padding: 6px 0; }
  .backend-row .grow { flex: 1; min-width: 0; }
  #toast {
    position: fixed; left: 50%; bottom: 36px; transform: translateX(-50%);
    background: #1f2329; color: #fff; border-radius: 8px; padding: 8px 18px;
    font-size: 13px; display: none; z-index: 20; max-width: 80vw;
  }
  .empty { color: var(--text-2); text-align: center; padding: 18px 0; }
  /* ── 添加机器人向导（day-0：bot 连上前 DM 卡片不存在，只能从这里手填密钥）── */
  .add-bot-tab {
    border: 1px solid rgba(255,255,255,.55); color: #fff; background: transparent;
    border-radius: 999px; padding: 3px 14px; font-size: 13px; cursor: pointer;
  }
  .add-bot-tab:hover { background: rgba(255,255,255,.18); }
  #wizMask, #confirmMask {
    position: fixed; inset: 0; background: rgba(0,0,0,.45); display: none; z-index: 30;
    overflow-y: auto; padding: 40px 16px;
  }
  #confirmMask { z-index: 40; }
  #wizMask.open, #confirmMask.open { display: block; }
  .btn.danger { background: var(--red); border-color: var(--red); color: #fff; }
  .btn.danger:hover { filter: brightness(.95); }
  .switch { cursor: pointer; user-select: none; }
  .bot-row { display: flex; align-items: center; gap: 8px; padding: 9px 0; border-bottom: 1px solid var(--border); flex-wrap: wrap; }
  .bot-row:last-child { border-bottom: 0; }
  .bot-row .grow { flex: 1; min-width: 0; }
  .wiz {
    background: var(--card); border-radius: var(--radius); max-width: 560px; margin: 0 auto;
    padding: 22px 24px 26px; box-shadow: 0 12px 40px rgba(0,0,0,.2);
  }
  .wiz h3 { margin: 0 0 4px; font-size: 17px; }
  .wiz .steps { display: flex; gap: 6px; margin: 12px 0 16px; }
  .wiz .step {
    flex: 1; text-align: center; font-size: 12px; color: var(--text-2);
    border-top: 3px solid var(--border); padding-top: 6px;
  }
  .wiz .step.on { color: var(--blue); border-top-color: var(--blue); font-weight: 600; }
  .wiz .step.done { color: var(--green); border-top-color: var(--green); }
  .wiz label { display: block; font-size: 13px; font-weight: 600; margin: 12px 0 4px; }
  .wiz input[type=text], .wiz input[type=password] {
    width: 100%; border: 1px solid var(--border); border-radius: 8px;
    padding: 8px 10px; font-size: 13px; font-family: inherit;
  }
  .wiz input:focus { outline: 0; border-color: var(--blue); }
  .wiz .radio-row { display: flex; gap: 16px; margin: 6px 0; font-size: 13px; }
  .wiz .actions { display: flex; gap: 10px; margin-top: 18px; align-items: center; }
  .wiz .actions .grow { flex: 1; }
  .check-item {
    display: flex; align-items: flex-start; gap: 10px; padding: 10px 0;
    border-bottom: 1px solid var(--border);
  }
  .check-item:last-child { border-bottom: 0; }
  .check-item .ico { font-size: 16px; line-height: 1.5; width: 20px; flex: none; text-align: center; }
  .check-item .body { flex: 1; min-width: 0; }
  .check-item .body .t { font-weight: 600; font-size: 13.5px; }
  .check-item .body .d { color: var(--text-2); font-size: 12px; margin-top: 2px; }
  .spin {
    display: inline-block; width: 13px; height: 13px; border: 2px solid var(--border);
    border-top-color: var(--blue); border-radius: 50%; animation: spin .8s linear infinite;
    vertical-align: -1px;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .copybox {
    display: flex; gap: 8px; align-items: center; background: #0e1117; color: #c9d1d9;
    border-radius: 8px; padding: 8px 10px; margin: 6px 0;
    font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .copybox code { flex: 1; min-width: 0; word-break: break-all; }
</style>
</head>
<body>
<div class="wrap">
  <div class="topbar">
    <h1>🤖 Codex Bridge 管理台</h1>
    <span class="sub">全局控制台 · 仅本机（127.0.0.1）· 添加机器人 / daemon 管理 / 多 bot / 体检 / 项目</span>
    <div class="bot-tabs" id="botTabs"></div>
  </div>

  <div class="cols">
    <div>
      <div class="card" id="daemonCard">
        <h2>🛰️ 后台 daemon <span class="right"><button class="btn" id="restartBtn">🔁 重启</button></span></h2>
        <div id="daemonBody" class="note">加载中…</div>
        <hr class="hr">
        <div id="updateBody" class="note">版本检查中…</div>
      </div>

      <div class="card" id="botsCard">
        <h2>🤖 多机器人 <span class="right note" id="botsCount"></span></h2>
        <div id="botsList" class="empty">加载中…</div>
      </div>

      <div class="card" id="overviewCard">
        <h2>📡 概览 <span class="right"><button class="btn" id="rediagBtn">🩺 诊断</button></span></h2>
        <div id="overviewBody" class="empty">加载中…</div>
        <hr class="hr">
        <div id="diagBody" class="note">点「🩺 诊断」检测事件订阅与各后端环境（约 3 秒）。</div>
      </div>

      <div class="card">
        <h2>📁 项目列表 <span class="right note" id="projCount"></span></h2>
        <div id="projList" class="empty">加载中…</div>
      </div>

      <div class="card" id="hostCard">
        <h2>🩺 宿主机体检 <span class="right"><button class="btn" id="hostBtn">重新检测</button></span></h2>
        <div id="hostBody" class="note">点「重新检测」查看本机后端环境与运行时信息。</div>
      </div>
    </div>

    <div>
      <div class="card">
        <h2>📜 实时日志 <span class="right note" id="logStatus">连接中…</span></h2>
        <div id="logbox"></div>
        <div class="note" style="margin-top:6px">高亮：<span class="tag blue">stream.timing</span><span class="tag blue">agent.*</span>　当日文件日志 SSE 实时跟随。</div>
      </div>
    </div>
  </div>

  <div class="note" style="text-align:center">
    数据每 5 秒自动刷新 · 写操作（🧠 后端切换 / 🔐 权限 / ✋ 免@ / 🗜️ 自动压缩）与飞书 DM 卡片共享同一服务层；daemon 在跑时实时生效，只读预览（daemon 未跑）下不可写。
  </div>
</div>

<div class="drawer-mask" id="drawerMask"></div>
<div class="drawer" id="drawer"></div>

<!-- ➕ 添加机器人向导：内容由 JS 按步骤渲染（day-0 手填密钥 → checklist → 完成） -->
<div id="wizMask"><div class="wiz" id="wizBody"></div></div>

<!-- 二次确认弹窗（重启 daemon / 删除机器人等破坏性操作） -->
<div id="confirmMask"><div class="wiz" id="confirmBody" style="max-width:440px"></div></div>

<div id="toast"></div>

<script>
(function () {
  'use strict';
  var state = null;          // /api/state 快照
  var diag = null;           // /api/diagnosis 结果
  var currentBot = null;     // 选中的 appId
  var drawerProject = null;  // 抽屉里打开的项目名

  function $(id) { return document.getElementById(id); }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = text;
    return n;
  }
  function toast(msg) {
    var t = $('toast');
    t.textContent = msg;
    t.style.display = 'block';
    clearTimeout(t._tm);
    t._tm = setTimeout(function () { t.style.display = 'none'; }, 3200);
  }

  // 二次确认弹窗（破坏性操作通用）：title/desc/确认按钮文案/危险样式 + onConfirm。
  function confirmDialog(opts) {
    var mask = $('confirmMask');
    var body = $('confirmBody');
    body.textContent = '';
    body.appendChild(el('h3', null, opts.title));
    (opts.lines || []).forEach(function (line) {
      body.appendChild(el('div', 'note', line));
    });
    var actions = el('div', 'actions');
    var cancel = el('button', 'btn', '取消');
    cancel.onclick = function () { mask.classList.remove('open'); };
    actions.appendChild(cancel);
    actions.appendChild(el('div', 'grow'));
    var ok = el('button', 'btn ' + (opts.danger ? 'danger' : 'primary'), opts.confirmLabel || '确认');
    ok.onclick = function () { mask.classList.remove('open'); opts.onConfirm(); };
    actions.appendChild(ok);
    body.appendChild(actions);
    mask.classList.add('open');
  }

  // ── 文案（与 DM 卡片 src/card/dm-cards.ts 对齐）───────────────────────────
  function kindLabel(kind) { return kind === 'single' ? '💬 单会话群' : '👥 多话题群'; }
  function tierLabel(m) {
    if (m === 'qa') return '🔒 项目内只读';
    if (m === 'write') return '✏️ 项目内读写';
    return '⚠️ 完全访问';
  }
  function permissionSummary(p) {
    return p.mode === p.guestMode
      ? '所有人：' + tierLabel(p.mode)
      : '管理员：' + tierLabel(p.mode) + ' · 其他人：' + tierLabel(p.guestMode);
  }
  function eventDiagText(d) {
    if (!d) return '（未检测）';
    if (d.state === 'ok') return '✅ 已生效（版本 v' + (d.version || '?') + ' 已订阅 im.message.receive_v1）';
    if (d.state === 'missing') return '❌ 已发布版本 v' + (d.version || '?') + ' 缺事件：' + (d.missingRequired || []).join('、') + ' —— @机器人不会有反应';
    if (d.state === 'unpublished') return '❌ 从未发布过版本 —— 事件订阅尚未生效，@机器人不会有反应';
    return '⚠️ 未能自动检测（' + (d.reason || '未知原因') + '）';
  }

  // ── 数据拉取 ──────────────────────────────────────────────────────────────
  function loadState() {
    fetch('/api/state').then(function (r) {
      if (r.status === 401) { toast('登录态失效：请用启动日志里带 token 的 URL 重新打开'); throw new Error('401'); }
      return r.json();
    }).then(function (s) {
      state = s;
      if (!currentBot && s.bots.length > 0) {
        var cur = null;
        for (var i = 0; i < s.bots.length; i++) if (s.bots[i].current) cur = s.bots[i];
        currentBot = (cur || s.bots[0]).appId;
      }
      render();
    }).catch(function () { /* 下个周期重试 */ });
  }

  function loadDiagnosis() {
    if (!currentBot) return;
    $('diagBody').textContent = '🔍 正在检测事件订阅与各后端环境…';
    fetch('/api/diagnosis?bot=' + encodeURIComponent(currentBot)).then(function (r) { return r.json(); })
      .then(function (d) { diag = d; renderDiagnosis(); })
      .catch(function () { $('diagBody').textContent = '⚠️ 诊断请求失败'; });
  }

  // ── daemon 生命周期 + 升级 ──────────────────────────────────────────────────
  var daemon = null;
  function loadDaemon() {
    fetch('/api/daemon').then(function (r) { return r.json(); })
      .then(function (d) { daemon = d; renderDaemon(); })
      .catch(function () { /* 下个周期重试 */ });
  }
  function loadUpdate() {
    fetch('/api/update/check').then(function (r) { return r.json(); })
      .then(function (u) { renderUpdate(u); })
      .catch(function () { $('updateBody').textContent = '⚠️ 版本检查失败（网络或 npm registry）'; });
  }

  function fmtUptime(ms) {
    if (typeof ms !== 'number') return '—';
    var s = Math.floor(ms / 1000);
    var d = Math.floor(s / 86400); s -= d * 86400;
    var h = Math.floor(s / 3600); s -= h * 3600;
    var m = Math.floor(s / 60);
    if (d > 0) return d + '天' + h + '小时';
    if (h > 0) return h + '小时' + m + '分';
    return m + '分钟';
  }

  function renderDaemon() {
    var box = $('daemonBody');
    box.textContent = '';
    var d = daemon;
    if (!d) { box.textContent = '加载中…'; return; }
    var btn = $('restartBtn');
    if (!d.supported) {
      box.appendChild(el('div', null, '⚠️ 本平台不支持后台服务'));
      box.appendChild(el('div', 'note', '用 feishu-codex-bridge run 前台运行；重启请在终端 Ctrl+C 后重跑。'));
      btn.className = 'btn disabled'; btn.disabled = true;
      return;
    }
    var line = el('div', 'statline');
    if (d.running) line.appendChild(el('span', 'tag green', '✅ 运行中' + (d.pid ? ' · pid ' + d.pid : '')));
    else line.appendChild(el('span', 'tag orange', d.installed ? '⚠️ 已安装但未在运行' : '⚪ 未安装为后台服务'));
    line.appendChild(el('span', 'tag', d.platformName || '后台服务'));
    line.appendChild(el('span', 'tag blue', 'v' + d.version));
    box.appendChild(line);
    if (d.uptimeMs !== undefined && d.running) box.appendChild(el('div', 'note', '已运行 ' + fmtUptime(d.uptimeMs)));
    if (d.lastExit !== undefined && d.lastExit !== '0') box.appendChild(el('div', 'note', '上次退出码：' + d.lastExit));
    // 重启按钮：仅在「本进程就是 daemon」（有 uptimeMs，即写操作可用）时真生效；
    // 只读预览态点了会 501，按钮仍可点但提示去起 daemon。
    btn.className = 'btn'; btn.disabled = false;
  }

  function renderUpdate(u) {
    var box = $('updateBody');
    box.textContent = '';
    if (u.dev) {
      box.appendChild(el('div', null, '🧩 源码开发模式（仓库内有 .git）'));
      box.appendChild(el('div', 'note', '当前 v' + u.current + '；升级请用 git pull && npm i（不走全局安装）。'));
      return;
    }
    if (u.hasUpdate && u.latest) {
      box.appendChild(el('div', null, '🆕 有新版 v' + u.latest + '（当前 v' + u.current + '）'));
      var row = el('div', 'statline');
      var up = el('button', 'btn primary', '⬆️ 升级到 v' + u.latest);
      up.onclick = function () { askUpdate(u.latest); };
      row.appendChild(up);
      row.appendChild(el('span', 'note', '默认只检测不自动升级；点上方按钮手动升级。'));
      box.appendChild(row);
    } else {
      box.appendChild(el('div', 'note', '✅ 已是最新版 v' + u.current + (u.latest ? '' : '（最新版查询失败，可稍后重试）')));
    }
  }

  function askRestart() {
    confirmDialog({
      title: '🔁 重启后台 daemon？',
      lines: [
        '重启期间所有群短暂无响应（通常数秒）。',
        '正在进行的 codex 会话会被优雅关闭并在重启后可继续。',
        '本机服务管理器会在旧实例退出后自动拉起新实例。',
      ],
      confirmLabel: '确认重启',
      onConfirm: function () { postAction('/api/daemon/restart', '重启'); },
    });
  }
  function askUpdate(latest) {
    confirmDialog({
      title: '⬆️ 升级到 v' + latest + '？',
      lines: [
        '将执行 npm i -g 安装最新版，完成后自动重启 daemon。',
        '重启期间所有群短暂无响应；正在进行的会话会被优雅关闭。',
      ],
      confirmLabel: '确认升级',
      onConfirm: function () { postAction('/api/update', '升级'); },
    });
  }
  // 重启/升级响应：202 = 已发起（detached helper 接管）；501 = 只读预览（无 daemon）。
  function postAction(path, label) {
    fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
      .then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (resp) {
        if (resp.status === 202) toast('✅ ' + (resp.body.message || (label + '已发起')));
        else if (resp.status === 501) toast('⏳ ' + (resp.body.message || (label + '需要 daemon 在跑（当前为只读预览）')));
        else toast('❌ ' + (resp.body.message || ('HTTP ' + resp.status)));
      })
      .catch(function () { toast('❌ 请求失败'); });
  }

  // ── 多机器人管理 ────────────────────────────────────────────────────────────
  function renderBots() {
    var box = $('botsList');
    box.textContent = '';
    if (!state || state.bots.length === 0) {
      box.className = 'empty';
      box.textContent = '还没有机器人。点右上「➕ 添加机器人」接入。';
      $('botsCount').textContent = '';
      return;
    }
    box.className = '';
    $('botsCount').textContent = '共 ' + state.bots.length + ' 个 · 绿点=在线';
    state.bots.forEach(function (b) {
      var row = el('div', 'bot-row');
      var grow = el('div', 'grow');
      var head = el('div', 'statline');
      head.appendChild(el('span', null, (b.running ? '🟢 ' : '⚪ ') + (b.botName || b.name)));
      head.appendChild(el('span', 'tag', b.tenant === 'lark' ? 'Lark' : '飞书'));
      head.appendChild(el('span', 'tag', b.appId));
      if (b.current) head.appendChild(el('span', 'tag blue', '主'));
      grow.appendChild(head);
      var sub = el('div', 'note',
        (b.running ? '运行中' + (b.pid ? ' · pid ' + b.pid : '') : '未在运行') +
        ' · ' + ((b.projects && b.projects.length) || 0) + ' 个项目');
      grow.appendChild(sub);
      row.appendChild(grow);

      // enabled 开关（= 活跃集；改后需重启 daemon 生效）
      var sw = el('button', 'btn' + (b.active ? ' primary' : ''), b.active ? '✅ 已启用' : '⛔ 已停用');
      sw.title = b.active ? '点一下停用（退出活跃集）' : '点一下启用（加入活跃集）';
      sw.onclick = function () { toggleBotEnabled(b); };
      row.appendChild(sw);

      // 删除（二次确认，危险样式）
      var del = el('button', 'btn danger', '🗑️');
      del.title = '删除这个机器人';
      del.onclick = function () { askDeleteBot(b); };
      row.appendChild(del);

      box.appendChild(row);
    });
  }

  function toggleBotEnabled(b) {
    fetch('/api/bots/' + encodeURIComponent(b.appId), {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: !b.active }),
    }).then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (resp) {
        if (resp.status === 200) { toast('✅ ' + (resp.body.message || '已保存')); loadState(); }
        else toast('❌ ' + (resp.body.message || ('HTTP ' + resp.status)));
      })
      .catch(function () { toast('❌ 请求失败'); });
  }

  function askDeleteBot(b) {
    confirmDialog({
      title: '🗑️ 删除机器人「' + (b.botName || b.name) + '」？',
      lines: [
        '将永久删除：注册表项 + 本机加密密钥 + 状态目录（项目 / 话题记录）。',
        '此操作不可撤销。删除后需重新 init 才能再次接入这个 App。',
      ],
      danger: true,
      confirmLabel: '确认删除',
      onConfirm: function () {
        fetch('/api/bots/' + encodeURIComponent(b.appId), { method: 'DELETE' })
          .then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
          .then(function (resp) {
            if (resp.status === 200) {
              toast('✅ ' + (resp.body.message || '已删除'));
              if (currentBot === b.appId) currentBot = null;
              loadState();
            } else toast('❌ ' + (resp.body.message || ('HTTP ' + resp.status)));
          })
          .catch(function () { toast('❌ 请求失败'); });
      },
    });
  }

  // ── 宿主机体检 ──────────────────────────────────────────────────────────────
  function loadHostDoctor() {
    $('hostBody').textContent = '🔍 正在检测…';
    fetch('/api/host-doctor').then(function (r) { return r.json(); })
      .then(function (h) { renderHostDoctor(h); })
      .catch(function () { $('hostBody').textContent = '⚠️ 体检请求失败'; });
  }
  function fmtBytes(n) {
    if (typeof n !== 'number' || n <= 0) return '0 B';
    var u = ['B', 'KB', 'MB', 'GB'];
    var i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
  }
  function renderHostDoctor(h) {
    var box = $('hostBody');
    box.textContent = '';
    var rt = el('div', 'statline');
    rt.appendChild(el('span', 'tag blue', 'Node ' + h.node));
    rt.appendChild(el('span', 'tag', h.platform + '/' + h.arch));
    rt.appendChild(el('span', 'tag', 'v' + h.version));
    box.appendChild(rt);
    box.appendChild(el('div', 'path', '🗂️ 配置目录：' + h.appDir));
    box.appendChild(el('div', 'path', '📜 日志目录：' + h.logsDir + '（' + fmtBytes(h.logBytes) + '）'));
    var title = el('div', null, '🧠 后端环境：');
    title.style.marginTop = '6px';
    box.appendChild(title);
    (h.backends || []).forEach(function (bk) {
      var row = el('div', 'statline');
      row.appendChild(el('span', null, (bk.ok ? '✅' : '❌') + ' ' + bk.name + (bk.version ? ' ' + bk.version : '') + (bk.isDefault ? '（默认）' : '')));
      if (bk.ok && bk.location) row.appendChild(el('span', 'note', bk.location));
      if (!bk.ok && bk.hint) row.appendChild(el('span', 'note', bk.hint));
      box.appendChild(row);
    });
  }

  // ── 渲染 ─────────────────────────────────────────────────────────────────
  function botOf(appId) {
    if (!state) return null;
    for (var i = 0; i < state.bots.length; i++) if (state.bots[i].appId === appId) return state.bots[i];
    return null;
  }

  function render() {
    renderBotTabs();
    renderBots();
    renderOverview();
    renderProjects();
    if (drawerProject) renderDrawer(drawerProject);
  }

  function renderBotTabs() {
    var box = $('botTabs');
    box.textContent = '';
    if (!state) return;
    state.bots.forEach(function (b) {
      var t = el('button', 'bot-tab' + (b.appId === currentBot ? ' on' : ''),
        (b.running ? '🟢 ' : '⚪ ') + (b.botName || b.name));
      t.onclick = function () {
        currentBot = b.appId; diag = null; closeDrawer(); render();
        $('diagBody').textContent = '点「🩺 诊断」检测事件订阅与各后端环境（约 3 秒）。';
      };
      box.appendChild(t);
    });
    // ➕ 添加机器人（day-0 入口）：始终可点——首装时没有任何 bot 也得能进向导。
    var add = el('button', 'add-bot-tab', '➕ 添加机器人');
    add.onclick = function () { openWizard(); };
    box.appendChild(add);
  }

  function renderOverview() {
    var box = $('overviewBody');
    box.textContent = '';
    box.className = '';
    var b = botOf(currentBot);
    if (!b) {
      box.className = 'empty';
      box.textContent = '还没有已注册的机器人。';
      var goWiz = el('button', 'btn primary', '➕ 添加机器人');
      goWiz.style.marginTop = '10px';
      goWiz.onclick = function () { openWizard(); };
      box.appendChild(el('div', 'note', '已在开发者后台建好应用？点下方手填 App ID/Secret 接入；或在终端跑 feishu-codex-bridge run 扫码创建。'));
      box.appendChild(goWiz);
      return;
    }

    var line1 = el('div', 'statline');
    line1.appendChild(el('span', null, '🤖 ' + (b.botName || b.name)));
    line1.appendChild(el('span', 'tag', b.tenant === 'lark' ? 'Lark' : '飞书'));
    line1.appendChild(el('span', 'tag', b.appId));
    if (b.current) line1.appendChild(el('span', 'tag blue', '主 bot'));
    if (b.active) line1.appendChild(el('span', 'tag blue', '活跃集'));
    box.appendChild(line1);

    var line2 = el('div', 'statline');
    if (b.running) {
      line2.appendChild(el('span', 'tag green', '✅ bridge 运行中 · pid ' + b.pid));
      // 真实 WS 长连接状态：daemon 进程内（本进程 channel / 子进程 IPC）上报；
      // 只读预览（锁文件探测）没有该字段，不渲染。
      if (b.connection) {
        var connOk = b.connection === 'connected';
        line2.appendChild(el('span', 'tag ' + (connOk ? 'green' : 'orange'), '长连接 ' + connText(b.connection)));
      }
    } else {
      line2.appendChild(el('span', 'tag orange', '⚠️ bridge 未在运行'));
      line2.appendChild(el('span', 'note', '终端执行 run / start 后这里显示实时状态'));
    }
    box.appendChild(line2);
    var note = el('div', 'note', '版本 v' + state.version + ' · 快照 ' + new Date(state.generatedAt).toLocaleTimeString());
    box.appendChild(note);
  }

  function connText(s) {
    if (s === 'connected') return '✅ 已连接';
    if (s === 'connecting') return '⏳ 连接中';
    if (s === 'reconnecting') return '↻ 重连中';
    if (s === 'disconnected') return '❌ 已断开';
    return s;
  }

  function renderDiagnosis() {
    var box = $('diagBody');
    box.textContent = '';
    box.className = '';
    if (!diag) return;
    box.appendChild(el('div', null, '事件订阅：' + eventDiagText(diag.event)));
    var title = el('div', null, '🧠 后端环境：');
    title.style.marginTop = '6px';
    box.appendChild(title);
    (diag.backends || []).forEach(function (bk) {
      var row = el('div', 'statline');
      row.appendChild(el('span', null, (bk.ok ? '✅' : '❌') + ' ' + bk.name + (bk.version ? ' ' + bk.version : '') + (bk.isDefault ? '（默认）' : '')));
      if (!bk.ok && bk.hint) row.appendChild(el('span', 'note', bk.hint));
      box.appendChild(row);
    });
  }

  function renderProjects() {
    var box = $('projList');
    box.textContent = '';
    var b = botOf(currentBot);
    var projects = (b && b.projects) || [];
    $('projCount').textContent = '共 ' + projects.length + ' 个项目';
    if (projects.length === 0) {
      box.className = 'empty';
      box.textContent = '还没有项目。到飞书私聊机器人，点「➕ 新建项目」创建。';
      return;
    }
    box.className = '';
    projects.forEach(function (p) {
      var item = el('div', 'proj');
      var head = el('div', 'statline');
      head.appendChild(el('span', 'name', p.name + (p.blank ? '（空白）' : '')));
      if (p.branch && p.branch !== '—') head.appendChild(el('span', 'tag', '🌿 ' + p.branch));
      item.appendChild(head);

      var meta = el('div', 'meta');
      meta.appendChild(el('span', 'tag', kindLabel(p.kind)));
      if (p.origin === 'joined') meta.appendChild(el('span', 'tag', '🔗 已加入'));
      meta.appendChild(el('span', 'tag ' + (p.mode === 'full' ? 'orange' : 'green'), '🔐 ' + permissionSummary(p)));
      meta.appendChild(el('span', 'tag blue', '🧠 ' + p.backend));
      meta.appendChild(el('span', 'tag', '🧵 ' + p.sessionCount + ' 话题'));
      meta.appendChild(el('span', 'tag', '✋ 免@：' + (p.noMention ? '开' : '关')));
      item.appendChild(meta);
      item.appendChild(el('div', 'path', '📂 ' + p.cwd));

      var ops = el('div', 'statline');
      var btn = el('button', 'btn primary', '⚙️ 设置');
      btn.onclick = function () { openDrawer(p.name); };
      ops.appendChild(btn);
      item.appendChild(ops);
      box.appendChild(item);
    });
  }

  // ── 项目详情抽屉（设置项写操作走与 DM 卡片同源的写路由）──────────────────
  function openDrawer(name) {
    drawerProject = name;
    renderDrawer(name);
    $('drawer').classList.add('open');
    $('drawerMask').classList.add('open');
  }
  function closeDrawer() {
    drawerProject = null;
    $('drawer').classList.remove('open');
    $('drawerMask').classList.remove('open');
  }
  $('drawerMask').onclick = closeDrawer;

  function findProject(name) {
    var b = botOf(currentBot);
    var list = (b && b.projects) || [];
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    return null;
  }

  // 写操作统一入口：200 ✅ 已保存并刷新；501 = 只读预览（daemon 未跑）；
  // 409 等其余 = 校验拒绝/出错，把服务端中文 message 弹出来。
  function postWrite(path, body) {
    fetch(path + '?bot=' + encodeURIComponent(currentBot), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {}),
    }).then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (resp) {
        if (resp.status === 501) toast('⏳ ' + (resp.body.message || '写操作需要 daemon 在跑（当前为只读预览）'));
        else if (resp.status === 200) { toast('✅ 已保存'); loadState(); }
        else toast('❌ ' + (resp.body.message || ('HTTP ' + resp.status)));
      })
      .catch(function () { toast('❌ 请求失败'); });
  }

  function optButtons(opts, current, onPick) {
    var row = el('div', 'opt-row');
    opts.forEach(function (o) {
      var b = el('button', 'btn' + (o.value === current ? ' primary' : ''), o.label);
      b.onclick = function () { onPick(o.value); };
      row.appendChild(b);
    });
    return row;
  }

  function renderDrawer(name) {
    var p = findProject(name);
    var d = $('drawer');
    d.textContent = '';
    if (!p) { d.appendChild(el('div', 'empty', '项目不存在或已删除')); return; }

    var close = el('button', 'btn', '✕ 关闭');
    close.style.cssFloat = 'right';
    close.onclick = closeDrawer;
    d.appendChild(close);
    d.appendChild(el('h3', null, '⚙️ 项目设置 · ' + p.name));
    d.appendChild(el('div', 'note', kindLabel(p.kind) + ' · 📂 ' + p.cwd));
    d.appendChild(el('hr', 'hr'));

    // 🔐 权限（管理员档 / 普通用户档 / 联网）
    d.appendChild(el('div', null, '🔐 权限'));
    d.appendChild(el('div', 'note', '当前 ' + permissionSummary(p) + ' · codex 沙箱可访问的范围（管理员 / 普通用户可分设）'));
    var tiers = [
      { label: '🔒 项目内只读', value: 'qa' },
      { label: '✏️ 项目内读写', value: 'write' },
      { label: '⚠️ 完全访问', value: 'full' },
    ];
    d.appendChild(el('div', 'note', '👑 管理员档'));
    d.appendChild(optButtons(tiers, p.mode, function (v) {
      postWrite('/api/project/' + encodeURIComponent(p.name) + '/permission', { mode: v, guestMode: p.guestMode, network: p.network });
    }));
    d.appendChild(el('div', 'note', '👥 普通用户档'));
    d.appendChild(optButtons(tiers, p.guestMode, function (v) {
      postWrite('/api/project/' + encodeURIComponent(p.name) + '/permission', { mode: p.mode, guestMode: v, network: p.network });
    }));
    d.appendChild(el('hr', 'hr'));

    // 🧠 后端 —— 检测式选择 UI（探测结果来自 🩺 诊断；切换走与 DM 同源的写路由）
    d.appendChild(el('div', null, '🧠 后端'));
    d.appendChild(el('div', 'note', '当前 ' + p.backend + ' · 切换只对新话题生效；已有话题会话仍走原后端'));
    var backends = (diag && diag.backends) || null;
    if (!backends) {
      d.appendChild(el('div', 'note', '（先点概览卡的「🩺 诊断」检测本机各后端可用状态）'));
    } else {
      backends.forEach(function (bk) {
        var row = el('div', 'backend-row');
        var label = (bk.ok ? '✅ ' : '❌ ') + bk.name + (bk.version ? ' ' + bk.version : '') + (bk.isDefault ? '（默认）' : '');
        var grow = el('div', 'grow');
        grow.appendChild(el('div', null, label + (bk.id === p.backend ? ' · ✓ 使用中' : '')));
        if (!bk.ok && bk.hint) grow.appendChild(el('div', 'note', bk.hint));
        row.appendChild(grow);
        if (bk.ok && bk.id !== p.backend) {
          var sw = el('button', 'btn primary', '切换');
          sw.onclick = function () {
            postWrite('/api/project/' + encodeURIComponent(p.name) + '/backend', { backend: bk.id });
          };
          row.appendChild(sw);
        }
        d.appendChild(row);
      });
    }
    d.appendChild(el('hr', 'hr'));

    // ✋ 免@
    d.appendChild(el('div', null, '✋ 免@（不用 @ 也回复）'));
    d.appendChild(optButtons(
      [{ label: '开', value: 'on' }, { label: '关', value: 'off' }],
      p.noMention ? 'on' : 'off',
      function (v) { postWrite('/api/project/' + encodeURIComponent(p.name) + '/no-mention', { on: v === 'on' }); }
    ));
    d.appendChild(el('div', 'note', p.kind === 'single'
      ? '开启后：本群所有消息(不用 @)都交给机器人处理。'
      : '开启后：话题内消息(不用 @)都处理；开新话题仍需 @机器人。'));
    d.appendChild(el('hr', 'hr'));

    // 🗜️ 自动压缩
    d.appendChild(el('div', null, '🗜️ 自动压缩上下文'));
    d.appendChild(optButtons(
      [{ label: '开', value: 'on' }, { label: '关', value: 'off' }],
      p.autoCompact ? 'on' : 'off',
      function (v) { postWrite('/api/project/' + encodeURIComponent(p.name) + '/auto-compact', { on: v === 'on' }); }
    ));
    d.appendChild(el('div', 'note', '开启后：上下文接近上限时 Codex 自动总结早前对话、释放空间（默认开）。'));
    d.appendChild(el('hr', 'hr'));

    // 🧵 话题
    d.appendChild(el('div', null, '🧵 话题 · 共 ' + p.sessionCount + ' 个'));
    var topicBox = el('div', 'note', '加载中…');
    d.appendChild(topicBox);
    fetch('/api/project/' + encodeURIComponent(p.name) + '/sessions?bot=' + encodeURIComponent(currentBot))
      .then(function (r) { return r.json(); })
      .then(function (j) {
        topicBox.textContent = '';
        var list = j.sessions || [];
        if (list.length === 0) { topicBox.textContent = '（暂无话题）'; return; }
        list.slice(0, 50).forEach(function (s) {
          var line = (s.summary || '(空)').replace(/\\s+/g, ' ').slice(0, 50);
          topicBox.appendChild(el('div', null, '· ' + line + ' · ' + new Date(s.updatedAt).toLocaleString()));
        });
        if (list.length > 50) topicBox.appendChild(el('div', null, '· …还有 ' + (list.length - 50) + ' 个话题'));
      })
      .catch(function () { topicBox.textContent = '⚠️ 话题加载失败'; });
  }

  // ── ➕ 添加机器人向导（day-0：bot 连上前飞书 DM 卡片不存在，只能从这里手填密钥）──
  // 安全：appSecret 仅在 step1 提交时一次性放进 POST body（service 层探活后进
  // 本机 keystore），绝不回显、绝不进任何 GET/轮询——下面 step≥2 全程不再持有它。
  var wizStep = 1;          // 1=表单 2=checklist 3=完成
  var wizBotId = null;      // 注册成功后拿到的 appId（checklist 轮询用）
  var wizPoll = null;       // setup-status 轮询定时器
  var openWizardBtnTenant = 'feishu';

  function openWizard() {
    wizStep = 1; wizBotId = null;
    stopWizPoll();
    $('wizMask').classList.add('open');
    renderWizard();
  }
  function closeWizard() {
    stopWizPoll();
    $('wizMask').classList.remove('open');
    loadState(); // 关向导后刷新 bot 列表（新注册的 bot 立即出现在标签里）
  }
  function stopWizPoll() {
    if (wizPoll) { clearInterval(wizPoll); wizPoll = null; }
  }

  function wizStepBar(active) {
    var bar = el('div', 'steps');
    var labels = ['① 填密钥', '② 接入检测', '③ 完成'];
    labels.forEach(function (lab, i) {
      var n = i + 1;
      var cls = 'step' + (n === active ? ' on' : (n < active ? ' done' : ''));
      bar.appendChild(el('div', cls, lab));
    });
    return bar;
  }

  function renderWizard() {
    if (wizStep === 1) return renderWizForm();
    if (wizStep === 2) return renderWizChecklist();
    return renderWizDone();
  }

  // ── ① 表单：appId / appSecret（密码型）/ 租户 ──────────────────────────────
  function renderWizForm() {
    var w = $('wizBody');
    w.textContent = '';
    w.appendChild(el('h3', null, '➕ 添加机器人'));
    w.appendChild(el('div', 'note', '已在飞书开发者后台建好「自建应用」？把它的 App ID 与 App Secret 填进来即可接入。'));
    w.appendChild(wizStepBar(1));

    w.appendChild(el('label', null, 'App ID'));
    var idIn = el('input');
    idIn.type = 'text'; idIn.id = 'wizAppId'; idIn.placeholder = 'cli_xxxxxxxxxxxxxxxx';
    idIn.autocomplete = 'off';
    w.appendChild(idIn);
    var idHint = el('div', 'note', '在开发者后台「凭证与基础信息」页可以找到（cli_ 开头）。');
    w.appendChild(idHint);

    w.appendChild(el('label', null, 'App Secret'));
    var secIn = el('input');
    secIn.type = 'password'; secIn.id = 'wizAppSecret'; secIn.placeholder = '••••••••••••••••';
    secIn.autocomplete = 'new-password';
    w.appendChild(secIn);
    w.appendChild(el('div', 'note', '🔒 密钥仅用于一次性探活验证后，加密存储在本机 keystore（AES-256-GCM）；不回显、不进日志，绝不外发。'));

    w.appendChild(el('label', null, '版本'));
    var radioRow = el('div', 'radio-row');
    [['feishu', '飞书（feishu.cn）'], ['lark', 'Lark（larksuite.com）']].forEach(function (pair) {
      var lbl = el('label'); lbl.style.fontWeight = '400'; lbl.style.margin = '0';
      var r = el('input'); r.type = 'radio'; r.name = 'wizTenant'; r.value = pair[0];
      if (pair[0] === openWizardBtnTenant) r.checked = true;
      r.onchange = function () {
        // 保留已填的 id/secret，仅切租户后重渲染（更新「前往后台」深链）。
        openWizardBtnTenant = pair[0];
        var keepId = ($('wizAppId') || {}).value || '';
        var keepSec = ($('wizAppSecret') || {}).value || '';
        renderWizForm();
        if ($('wizAppId')) $('wizAppId').value = keepId;
        if ($('wizAppSecret')) $('wizAppSecret').value = keepSec;
      };
      lbl.appendChild(r); lbl.appendChild(document.createTextNode(' ' + pair[1]));
      radioRow.appendChild(lbl);
    });
    w.appendChild(radioRow);

    var deepLink = openWizardBtnTenant === 'lark'
      ? 'https://open.larksuite.com/app'
      : 'https://open.feishu.cn/app';
    var dl = el('div', 'note');
    var a = el('a', null, '前往开发者后台创建 / 查看应用 ↗');
    a.href = deepLink; a.target = '_blank'; a.rel = 'noopener';
    dl.appendChild(document.createTextNode('还没有应用？'));
    dl.appendChild(a);
    w.appendChild(dl);

    var msg = el('div', 'note'); msg.id = 'wizFormMsg'; msg.style.color = 'var(--red)';
    w.appendChild(msg);

    var actions = el('div', 'actions');
    var cancel = el('button', 'btn', '取消');
    cancel.onclick = closeWizard;
    var submit = el('button', 'btn primary', '验证并添加');
    submit.id = 'wizSubmit';
    submit.onclick = submitWizForm;
    actions.appendChild(cancel);
    actions.appendChild(el('div', 'grow'));
    actions.appendChild(submit);
    w.appendChild(actions);
    setTimeout(function () { idIn.focus(); }, 50);
  }

  function submitWizForm() {
    var appId = ($('wizAppId').value || '').trim();
    var appSecret = ($('wizAppSecret').value || '').trim();
    var msg = $('wizFormMsg');
    msg.textContent = '';
    if (!appId || !appSecret) { msg.textContent = 'App ID 与 App Secret 都要填。'; return; }
    var btn = $('wizSubmit');
    btn.textContent = '验证中…'; btn.className = 'btn primary disabled'; btn.disabled = true;
    fetch('/api/bots', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ appId: appId, appSecret: appSecret, tenant: openWizardBtnTenant }),
    }).then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (resp) {
        // appSecret 用完即弃：清掉输入框，后续 checklist 全程不再持有它。
        $('wizAppSecret').value = '';
        if (resp.status === 201 && resp.body.ok) {
          wizBotId = resp.body.bot.appId;
          wizStep = 2;
          renderWizard();
          startWizPoll();
        } else {
          btn.textContent = '验证并添加'; btn.className = 'btn primary'; btn.disabled = false;
          msg.textContent = '❌ ' + (resp.body.message || ('添加失败（HTTP ' + resp.status + '）'));
        }
      })
      .catch(function () {
        btn.textContent = '验证并添加'; btn.className = 'btn primary'; btn.disabled = false;
        msg.textContent = '❌ 请求失败，请重试。';
      });
  }

  // ── ② checklist：密钥 → 长连接 → 事件订阅（5s 轮询直到事件 ✅）────────────
  var wizSetup = null;
  function startWizPoll() {
    stopWizPoll();
    pollWizSetup();
    wizPoll = setInterval(pollWizSetup, 5000);
  }
  function pollWizSetup() {
    if (!wizBotId) return;
    fetch('/api/bots/' + encodeURIComponent(wizBotId) + '/setup-status')
      .then(function (r) { return r.json(); })
      .then(function (s) {
        wizSetup = s;
        if (wizStep === 2) renderWizChecklist();
        // 事件已生效 → 停轮询（用户可手动「下一步」去完成页）。
        if (s.event && s.event.state === 'ok') stopWizPoll();
      })
      .catch(function () { /* 下个周期重试 */ });
  }

  function checkItem(ico, title, desc, extra) {
    var item = el('div', 'check-item');
    var ic = el('div', 'ico'); ic.textContent = ''; item.appendChild(ic);
    if (ico === 'spin') ic.appendChild(el('span', 'spin')); else ic.textContent = ico;
    var body = el('div', 'body');
    body.appendChild(el('div', 't', title));
    if (desc) body.appendChild(el('div', 'd', desc));
    if (extra) body.appendChild(extra);
    item.appendChild(body);
    return item;
  }

  function renderWizChecklist() {
    var w = $('wizBody');
    w.textContent = '';
    w.appendChild(el('h3', null, '② 接入检测'));
    w.appendChild(el('div', 'note', '机器人「' + (wizBotId || '') + '」已注册。下面逐项检测接入状态，事件订阅生效后即可去群里 @它。'));
    w.appendChild(wizStepBar(2));

    var s = wizSetup;
    if (!s) {
      w.appendChild(checkItem('spin', '正在检测…', '首次拉取约 2~3 秒'));
      w.appendChild(wizChecklistActions(false));
      return;
    }

    // 密钥有效
    w.appendChild(checkItem(
      s.credentials && s.credentials.ok ? '✅' : '❌',
      '密钥有效',
      s.credentials && s.credentials.ok ? '凭据探活通过' + (s.botName ? '（' + s.botName + '）' : '') : ('凭据校验失败：' + ((s.credentials && s.credentials.reason) || '未知'))
    ));

    // scope 缺失（密钥有效时才有意义）
    if (s.credentials && s.credentials.ok && s.scopes) {
      var miss = s.scopes.missingRequired;
      if (miss === undefined) {
        w.appendChild(checkItem('⚠️', '权限检测', '未能读取已授权权限（缺 application:app_version 等只读 scope 时会这样），可先继续。'));
      } else if (miss.length === 0) {
        w.appendChild(checkItem('✅', '必需权限齐全', '核心消息权限已全部授权'));
      } else {
        var grant = el('div', 'note');
        var ga = el('a', null, '一键去授权页补齐这 ' + miss.length + ' 项 ↗');
        ga.href = s.scopes.grantUrl; ga.target = '_blank'; ga.rel = 'noopener';
        grant.appendChild(ga);
        w.appendChild(checkItem('⚠️', '缺 ' + miss.length + ' 项必需权限', miss.join('、'), grant));
      }
    }

    // 长连接在线
    var conn = s.connection || {};
    if (conn.running && conn.connection === 'connected') {
      w.appendChild(checkItem('✅', '长连接在线', 'bridge 已连上飞书，可实时收发'));
    } else if (conn.running) {
      w.appendChild(checkItem('spin', 'bridge 运行中', '长连接' + (conn.connection ? '（' + conn.connection + '）' : '建立中…')));
    } else {
      var cmd = el('div');
      cmd.appendChild(el('div', 'note', '该机器人尚未被 daemon 拉起。把它加入活跃集后重启 daemon 即可生效：'));
      cmd.appendChild(copyRow('feishu-codex-bridge bot use ' + (wizBotId || '<appId>')));
      cmd.appendChild(copyRow('feishu-codex-bridge start'));
      cmd.appendChild(el('div', 'note', '（已在跑单 bot 的 run 进程不会被打断；上面的命令只影响 daemon 拉起的活跃集。）'));
      w.appendChild(checkItem('⚪', '长连接未建立', '需要 daemon 拉起这个 bot', cmd));
    }

    // 事件订阅三态
    var ev = s.event || { state: 'unchecked' };
    if (ev.state === 'ok') {
      w.appendChild(checkItem('✅', '事件订阅已生效', eventDiagText(ev)));
    } else {
      var evExtra = el('div');
      evExtra.appendChild(el('div', 'note', ev.state === 'unchecked'
        ? '（缺 application:app_version 只读 scope 或网络不通时无法自动检测；按下方深链手动核对「事件配置」。）'
        : '去开发者后台「事件与回调」：事件配置改「长连接」→ 添加 im.message.receive_v1 → 应用发布里创建并发布版本。'));
      var ea = el('a', null, '打开「事件与回调」配置页 ↗');
      ea.href = s.eventConfigUrl; ea.target = '_blank'; ea.rel = 'noopener';
      evExtra.appendChild(ea);
      evExtra.appendChild(el('div', 'note', '配置好后无需手动刷新——本页每 5 秒自动复检，生效会变 ✅。'));
      w.appendChild(checkItem(ev.state === 'unchecked' ? '⚠️' : 'spin',
        ev.state === 'unchecked' ? '事件订阅未能自动检测' : '事件订阅尚未生效',
        eventDiagText(ev), evExtra));
    }

    w.appendChild(wizChecklistActions(ev.state === 'ok'));
  }

  function wizChecklistActions(eventOk) {
    var actions = el('div', 'actions');
    var back = el('button', 'btn', '稍后再说');
    back.onclick = closeWizard;
    actions.appendChild(back);
    actions.appendChild(el('div', 'grow'));
    var next = el('button', 'btn' + (eventOk ? ' primary' : ''), eventOk ? '下一步' : '事件生效后再继续');
    if (eventOk) { next.onclick = function () { wizStep = 3; stopWizPoll(); renderWizard(); }; }
    else { next.className = 'btn disabled'; next.disabled = true; }
    actions.appendChild(next);
    return actions;
  }

  function copyRow(text) {
    var row = el('div', 'copybox');
    row.appendChild(el('code', null, text));
    var btn = el('button', 'btn', '复制');
    btn.onclick = function () {
      var done = function () { toast('✅ 已复制'); };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () { toast('复制失败，请手动选中'); });
      } else { toast('请手动选中复制'); }
    };
    row.appendChild(btn);
    return row;
  }

  // ── ③ 完成页：引导去飞书建群 / 拉 bot ─────────────────────────────────────
  function renderWizDone() {
    var w = $('wizBody');
    w.textContent = '';
    w.appendChild(el('h3', null, '🎉 接入完成'));
    w.appendChild(wizStepBar(3));
    w.appendChild(el('div', 'note', '机器人「' + ((wizSetup && wizSetup.botName) || wizBotId || '') + '」已就绪，事件订阅已生效。'));
    var ul = el('div'); ul.style.margin = '12px 0';
    [
      '① 在飞书里私聊这个机器人，点「➕ 新建项目」把一个目录绑成项目群；',
      '② 或把机器人拉进一个已有群（需开「加入存量群」相关权限）自动绑定；',
      '③ 然后在群里 @机器人 提需求，它就在绑定的目录里干活。',
    ].forEach(function (line) {
      var d = el('div'); d.style.padding = '4px 0'; d.textContent = line;
      ul.appendChild(d);
    });
    w.appendChild(ul);
    w.appendChild(el('div', 'note', '提示：私聊机器人发任意消息即可唤出私聊管理台；这里 Web 控制台与私聊卡片共享同一套设置，双端实时一致。'));

    var actions = el('div', 'actions');
    actions.appendChild(el('div', 'grow'));
    var done = el('button', 'btn primary', '完成');
    done.onclick = function () { currentBot = wizBotId; closeWizard(); };
    actions.appendChild(done);
    w.appendChild(actions);
  }

  // ── 日志 SSE ─────────────────────────────────────────────────────────────
  var MAX_LOG_LINES = 500;
  function startLogStream() {
    var es = new EventSource('/api/logs/stream');
    var box = $('logbox');
    es.onopen = function () { $('logStatus').textContent = '🟢 已连接'; };
    es.onerror = function () { $('logStatus').textContent = '🔁 重连中…'; };
    es.onmessage = function (ev) {
      if (!ev.data) return;
      var div = el('div', null, ev.data);
      // 关键事件高亮：stream.timing / agent.*（与终端 STDOUT 允许清单同感）
      if (ev.data.indexOf('stream.timing') >= 0 || ev.data.indexOf('"phase":"agent"') >= 0 || ev.data.indexOf('agent.') >= 0) {
        div.className = 'hl';
      }
      if (ev.data.indexOf('"level":"warn"') >= 0) div.className = 'warn';
      if (ev.data.indexOf('"level":"error"') >= 0) div.className = 'err';
      var stick = box.scrollTop + box.clientHeight >= box.scrollHeight - 24;
      box.appendChild(div);
      while (box.childNodes.length > MAX_LOG_LINES) box.removeChild(box.firstChild);
      if (stick) box.scrollTop = box.scrollHeight;
    };
  }

  // ── 启动 ─────────────────────────────────────────────────────────────────
  $('rediagBtn').onclick = loadDiagnosis;
  $('restartBtn').onclick = askRestart;
  $('hostBtn').onclick = loadHostDoctor;
  $('confirmMask').onclick = function (e) { if (e.target === $('confirmMask')) $('confirmMask').classList.remove('open'); };
  loadState();
  loadDaemon();
  loadUpdate();
  startLogStream();
  setInterval(loadState, 5000); // 轮询 /api/state 5s 刷新
  setInterval(loadDaemon, 5000); // daemon 运行状态/时长同频刷新
})();
</script>
</body>
</html>
`;
