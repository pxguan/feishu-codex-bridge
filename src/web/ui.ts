/**
 * Web 控制台前端 —— 单文件内嵌 HTML（零依赖、零构建）。
 *
 * 为什么是 .ts 模板字符串而不是独立 ui.html：tsup 打包 / tsc / vitest 三方都
 * 无需任何额外配置（.html 文件要么得加 esbuild loader + d.ts，要么得改 vitest
 * 配置），这是"实现最干净的一种"。约定：内嵌的 client JS **不用反引号、不用
 * ${}**（全部字符串拼接）；外层模板字符串（本文件 TS 层）可用 ${} 把若干「纯
 * 逻辑」JS 片段拼进来 —— 这些片段（{@link UI_PURE_JS}）是 DOM-free 纯函数，
 * 既内联进页面、又被单测 new Function 取出来直接断言（无需 jsdom）。
 *
 * 架构（design web-tabs.md）：hash 路由的多 Tab——📊 总览（全局：daemon / 多
 * bot 聚合 / 宿主机体检 / 🧠 后端管理卡 / ➕ 添加 / 全局日志）+ 每 bot 一个 Tab
 * （该 bot 概览 / setup 诊断 / 项目列表 + 设置抽屉 / 停用·删除）。单 #tabContent
 * 按路由清空重渲；SSE 日志全程只连一次不随 Tab 断。
 *
 * UI 风格贴飞书 DM 卡片：卡片块 + 圆角 + 标签 + 蓝主按钮（#3370ff），中文文案与
 * src/card/dm-cards.ts 同款 emoji 标签（🧠 后端 / 🔐 权限 / 🩺 诊断 / ✋ 免@ /
 * 🗜️ 自动压缩 / 🧵 话题 / 👥 多话题群 / 💬 单会话群）。
 */

/**
 * 纯逻辑片段（DOM-free，单测直接 new Function 取出断言）：
 *   parseRoute       hash → { tab:'overview' } | { tab:'bot', botId }
 *   qrEncode/qrSvg   零依赖微型 QR 编码器（byte 模式·ECC M·自动选版选 mask）→ SVG
 *   depTriState      后端依赖三态 → { state, label, action }（下载 / 手动 / 就绪）
 *   groupBackends    catalog 条目按 agentFamily 分组（Codex / Claude）
 *   summarizeState   /api/state 快照 → 全局聚合摘要（在线 N/M · 项目数 · 活跃集）
 * 约束：纯字符串拼接、不用反引号 / ${}（这段会被原样塞进 <script>）。
 */
export const UI_PURE_JS = `
  // ── hash 路由：'#overview'（或空）→ 总览；'#bot/<appId>' → 该 bot Tab ──────────
  function parseRoute(hash) {
    var h = (hash || '').replace(/^#/, '');
    if (h.indexOf('bot/') === 0) return { tab: 'bot', botId: decodeURIComponent(h.slice(4)) };
    return { tab: 'overview' };
  }

  // ── 后端依赖三态（catalog entry → 渲染语义）─────────────────────────────────
  //   installed       已装 → { state:'installed', label:'✅ 就绪' }
  //   not-installed   npm-ondemand 未装 → { state:'downloadable', action:'download' }
  //   external-missing 外部 CLI/适配器缺失 → { state:'manual', action:'manual' }
  function depTriState(entry) {
    if (entry.depState === 'installed') return { state: 'installed', label: '✅ 就绪', action: null };
    if (entry.installable) return { state: 'downloadable', label: '⬇️ 可下载', action: 'download' };
    return { state: 'manual', label: '⚠️ 未安装', action: 'manual' };
  }

  // ── catalog 条目按 agentFamily 分组（picker / 后端管理卡都用）────────────────
  // 返回 [{ family:'codex', entries:[…] }, { family:'claude', entries:[…] }, …]，
  // 顺序按首次出现，codex 永远在前（catalog 第一条即 codex）。
  function groupBackends(entries) {
    var order = [], byFamily = {};
    (entries || []).forEach(function (e) {
      if (!byFamily[e.agentFamily]) { byFamily[e.agentFamily] = []; order.push(e.agentFamily); }
      byFamily[e.agentFamily].push(e);
    });
    return order.map(function (f) { return { family: f, entries: byFamily[f] }; });
  }

  // ── /api/state 快照 → 全局聚合摘要 ──────────────────────────────────────────
  function summarizeState(state) {
    var bots = (state && state.bots) || [];
    var online = 0, projects = 0, active = 0;
    bots.forEach(function (b) {
      if (b.running) online++;
      if (b.active) active++;
      projects += (b.projects && b.projects.length) || 0;
    });
    return { total: bots.length, online: online, projects: projects, active: active };
  }

  // ── 扫码 SSE 事件 → 文案映射（事件处理核心，DOM-free 纯逻辑）─────────────────
  // status 事件：polling / slow_down / domain_switched → 顶部细字提示。
  function scanStatusText(status) {
    if (status === 'slow_down') return '📱 等待扫码…（已降速）';
    if (status === 'domain_switched') return '📱 已识别国际版租户，等待扫码…';
    return '📱 用飞书扫码并确认创建…';
  }
  // error 事件：读 SDK reject 的 code（非 instanceof）→ 文案。abort=null（静默不弹）。
  function scanErrorText(code, message) {
    if (code === 'abort') return null;
    if (code === 'expired_token') return '二维码已过期，请重新生成。';
    if (code === 'access_denied') return '你在飞书里取消或拒绝了创建。';
    return message || '创建失败，请重试。';
  }

  // ── SSE 块解析：从 fetch 流式安装的一个「event: …\\ndata: {json}」块取出 JSON。──
  // 返回 parse 后的对象或 null（无 data 行 / JSON 坏）。用于后端安装进度流。
  function parseSseDataBlock(block) {
    var dataLine = null;
    (block || '').split('\\n').forEach(function (l) { if (l.indexOf('data:') === 0) dataLine = l.slice(5).trim(); });
    if (!dataLine) return null;
    try { return JSON.parse(dataLine); } catch (e) { return null; }
  }

  // ── 微型 QR 编码器（Model 2 · byte 模式 · ECC M · 自动选版 + 选 mask）────────
  // 端口自 Nayuki QR Code generator（MIT）核心。返回 { size, modules:[[bool]] }。
  function qrEncode(text) {
    var bytes = qrUtf8Bytes(text);
    var version = 0, dataCapBits = 0;
    for (var v = 1; v <= 40; v++) {
      var cap = qrNumDataCodewords(v) * 8;
      var ccBits = v < 10 ? 8 : 16;
      var used = 4 + ccBits + bytes.length * 8;
      if (used <= cap) { version = v; dataCapBits = cap; break; }
    }
    if (!version) throw new Error('数据过长，无法编码为二维码');
    var bb = [];
    qrAppendBits(bb, 0x4, 4);
    qrAppendBits(bb, bytes.length, version < 10 ? 8 : 16);
    for (var i = 0; i < bytes.length; i++) qrAppendBits(bb, bytes[i], 8);
    qrAppendBits(bb, 0, Math.min(4, dataCapBits - bb.length));
    qrAppendBits(bb, 0, (8 - bb.length % 8) % 8);
    for (var pad = 0xEC; bb.length < dataCapBits; pad ^= 0xEC ^ 0x11) qrAppendBits(bb, pad, 8);
    var dataCodewords = [];
    for (var k = 0; k < bb.length; k += 8) {
      var byte = 0;
      for (var b = 0; b < 8; b++) byte = (byte << 1) | bb[k + b];
      dataCodewords.push(byte);
    }
    var allCodewords = qrAddEcc(dataCodewords, version);
    var size = version * 4 + 17;
    var modules = [], isFn = [];
    for (var r = 0; r < size; r++) { modules.push(new Array(size).fill(false)); isFn.push(new Array(size).fill(false)); }
    qrDrawFunctionPatterns(modules, isFn, size, version);
    qrDrawCodewords(modules, isFn, size, allCodewords);
    var bestMask = 0, minPenalty = Infinity;
    for (var msk = 0; msk < 8; msk++) {
      qrApplyMask(modules, isFn, size, msk);
      qrDrawFormatBits(modules, isFn, size, msk);
      var pen = qrPenalty(modules, size);
      if (pen < minPenalty) { minPenalty = pen; bestMask = msk; }
      qrApplyMask(modules, isFn, size, msk);
    }
    qrApplyMask(modules, isFn, size, bestMask);
    qrDrawFormatBits(modules, isFn, size, bestMask);
    return { size: size, modules: modules };
  }
  function qrUtf8Bytes(s) {
    var out = [];
    for (var i = 0; i < s.length; i++) {
      var c = s.charCodeAt(i);
      if (c < 0x80) out.push(c);
      else if (c < 0x800) { out.push(0xC0 | (c >> 6), 0x80 | (c & 0x3F)); }
      else if (c >= 0xD800 && c <= 0xDBFF) {
        var c2 = s.charCodeAt(++i);
        var cp = 0x10000 + ((c & 0x3FF) << 10) + (c2 & 0x3FF);
        out.push(0xF0 | (cp >> 18), 0x80 | ((cp >> 12) & 0x3F), 0x80 | ((cp >> 6) & 0x3F), 0x80 | (cp & 0x3F));
      } else { out.push(0xE0 | (c >> 12), 0x80 | ((c >> 6) & 0x3F), 0x80 | (c & 0x3F)); }
    }
    return out;
  }
  function qrAppendBits(bb, val, len) { for (var i = len - 1; i >= 0; i--) bb.push((val >>> i) & 1); }
  var QR_ECC_CW = [-1,10,16,26,18,24,16,18,22,22,26,30,22,22,24,24,28,28,26,26,26,26,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28,28];
  var QR_ECC_BLOCKS = [-1,1,1,1,2,2,4,4,4,5,5,5,8,9,9,10,10,11,13,14,16,17,17,18,20,21,23,25,26,28,29,31,33,35,37,38,40,43,45,47,49];
  function qrNumRawDataModules(ver) {
    var result = (16 * ver + 128) * ver + 64;
    if (ver >= 2) { var n = Math.floor(ver / 7) + 2; result -= (25 * n - 10) * n - 55; if (ver >= 7) result -= 36; }
    return result;
  }
  function qrNumDataCodewords(ver) {
    return Math.floor(qrNumRawDataModules(ver) / 8) - QR_ECC_CW[ver] * QR_ECC_BLOCKS[ver];
  }
  function qrGfMul(x, y) {
    var z = 0;
    for (var i = 7; i >= 0; i--) { z = (z << 1) ^ ((z >>> 7) * 0x11D); z ^= ((y >>> i) & 1) * x; }
    return z & 0xFF;
  }
  function qrReedSolomonDivisor(degree) {
    var result = [];
    for (var i = 0; i < degree - 1; i++) result.push(0);
    result.push(1);
    var root = 1;
    for (var j = 0; j < degree; j++) {
      for (var m = 0; m < result.length; m++) {
        result[m] = qrGfMul(result[m], root);
        if (m + 1 < result.length) result[m] ^= result[m + 1];
      }
      root = qrGfMul(root, 0x02);
    }
    return result;
  }
  function qrReedSolomonRemainder(data, divisor) {
    var result = divisor.map(function () { return 0; });
    data.forEach(function (b) {
      var factor = b ^ result.shift();
      result.push(0);
      divisor.forEach(function (coef, i) { result[i] ^= qrGfMul(coef, factor); });
    });
    return result;
  }
  function qrAddEcc(data, ver) {
    var numBlocks = QR_ECC_BLOCKS[ver], blockEccLen = QR_ECC_CW[ver];
    var rawCodewords = Math.floor(qrNumRawDataModules(ver) / 8);
    var numShort = numBlocks - rawCodewords % numBlocks;
    var shortLen = Math.floor(rawCodewords / numBlocks);
    var blocks = [], rsDiv = qrReedSolomonDivisor(blockEccLen), k = 0;
    for (var i = 0; i < numBlocks; i++) {
      var datLen = shortLen - blockEccLen + (i < numShort ? 0 : 1);
      var dat = data.slice(k, k + datLen); k += datLen;
      var ecBytes = qrReedSolomonRemainder(dat, rsDiv);
      if (i < numShort) dat.push(0);
      blocks.push(dat.concat(ecBytes));
    }
    var result = [];
    for (var idx = 0; idx < blocks[0].length; idx++) {
      for (var b = 0; b < blocks.length; b++) {
        if (idx !== shortLen - blockEccLen || b >= numShort) result.push(blocks[b][idx]);
      }
    }
    return result;
  }
  function qrSetFn(m, isFn, r, c, val) { m[r][c] = val; isFn[r][c] = true; }
  function qrGetBit(x, i) { return ((x >>> i) & 1) !== 0; }
  function qrDrawFunctionPatterns(m, isFn, size, ver) {
    for (var i = 0; i < size; i++) { qrSetFn(m, isFn, 6, i, i % 2 === 0); qrSetFn(m, isFn, i, 6, i % 2 === 0); }
    qrFinder(m, isFn, size, 3, 3); qrFinder(m, isFn, size, size - 4, 3); qrFinder(m, isFn, size, 3, size - 4);
    var pos = qrAlignPositions(ver), n = pos.length;
    for (var a = 0; a < n; a++) for (var b = 0; b < n; b++) {
      if ((a === 0 && b === 0) || (a === 0 && b === n - 1) || (a === n - 1 && b === 0)) continue;
      qrAlignment(m, isFn, pos[a], pos[b]);
    }
    qrDrawFormatBits(m, isFn, size, 0);
    qrDrawVersion(m, isFn, size, ver);
  }
  function qrFinder(m, isFn, size, cx, cy) {
    for (var dy = -4; dy <= 4; dy++) for (var dx = -4; dx <= 4; dx++) {
      var dist = Math.max(Math.abs(dx), Math.abs(dy)), xx = cx + dx, yy = cy + dy;
      if (xx >= 0 && xx < size && yy >= 0 && yy < size) qrSetFn(m, isFn, yy, xx, dist !== 2 && dist !== 4);
    }
  }
  function qrAlignment(m, isFn, cx, cy) {
    for (var dy = -2; dy <= 2; dy++) for (var dx = -2; dx <= 2; dx++) qrSetFn(m, isFn, cy + dy, cx + dx, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
  }
  function qrAlignPositions(ver) {
    if (ver === 1) return [];
    var n = Math.floor(ver / 7) + 2;
    var step = Math.floor((ver * 8 + n * 3 + 5) / (n * 4 - 4)) * 2;
    var result = [6];
    for (var pos = ver * 4 + 10; result.length < n; pos -= step) result.splice(1, 0, pos);
    return result;
  }
  function qrDrawFormatBits(m, isFn, size, mask) {
    var data = (0 << 3) | mask; // ECC M = 0b00
    var rem = data;
    for (var i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    var bits = (data << 10 | rem) ^ 0x5412;
    for (var i2 = 0; i2 <= 5; i2++) qrSetFn(m, isFn, i2, 8, qrGetBit(bits, i2));
    qrSetFn(m, isFn, 7, 8, qrGetBit(bits, 6));
    qrSetFn(m, isFn, 8, 8, qrGetBit(bits, 7));
    qrSetFn(m, isFn, 8, 7, qrGetBit(bits, 8));
    for (var i3 = 9; i3 < 15; i3++) qrSetFn(m, isFn, 8, 14 - i3, qrGetBit(bits, i3));
    for (var i4 = 0; i4 < 8; i4++) qrSetFn(m, isFn, 8, size - 1 - i4, qrGetBit(bits, i4));
    for (var i5 = 8; i5 < 15; i5++) qrSetFn(m, isFn, size - 15 + i5, 8, qrGetBit(bits, i5));
    qrSetFn(m, isFn, size - 8, 8, true);
  }
  function qrDrawVersion(m, isFn, size, ver) {
    if (ver < 7) return;
    var rem = ver;
    for (var i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1F25);
    var bits = ver << 12 | rem;
    for (var i2 = 0; i2 < 18; i2++) {
      var bit = qrGetBit(bits, i2), a = size - 11 + i2 % 3, b = Math.floor(i2 / 3);
      qrSetFn(m, isFn, b, a, bit); qrSetFn(m, isFn, a, b, bit);
    }
  }
  function qrDrawCodewords(m, isFn, size, codewords) {
    var i = 0;
    for (var right = size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (var vert = 0; vert < size; vert++) {
        for (var j = 0; j < 2; j++) {
          var col = right - j, upward = ((right + 1) & 2) === 0, row = upward ? size - 1 - vert : vert;
          if (!isFn[row][col] && i < codewords.length * 8) {
            m[row][col] = qrGetBit(codewords[i >>> 3], 7 - (i & 7)); i++;
          }
        }
      }
    }
  }
  function qrApplyMask(m, isFn, size, mask) {
    for (var r = 0; r < size; r++) for (var c = 0; c < size; c++) {
      if (isFn[r][c]) continue;
      var invert = false;
      if (mask === 0) invert = (r + c) % 2 === 0;
      else if (mask === 1) invert = r % 2 === 0;
      else if (mask === 2) invert = c % 3 === 0;
      else if (mask === 3) invert = (r + c) % 3 === 0;
      else if (mask === 4) invert = (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0;
      else if (mask === 5) invert = (r * c) % 2 + (r * c) % 3 === 0;
      else if (mask === 6) invert = ((r * c) % 2 + (r * c) % 3) % 2 === 0;
      else if (mask === 7) invert = ((r + c) % 2 + (r * c) % 3) % 2 === 0;
      if (invert) m[r][c] = !m[r][c];
    }
  }
  function qrPenalty(m, size) {
    var penalty = 0, r, c;
    for (r = 0; r < size; r++) { var rc = m[r][0], rl = 1; for (c = 1; c < size; c++) { if (m[r][c] === rc) { rl++; if (rl === 5) penalty += 3; else if (rl > 5) penalty++; } else { rc = m[r][c]; rl = 1; } } }
    for (c = 0; c < size; c++) { var cc = m[0][c], cl = 1; for (r = 1; r < size; r++) { if (m[r][c] === cc) { cl++; if (cl === 5) penalty += 3; else if (cl > 5) penalty++; } else { cc = m[r][c]; cl = 1; } } }
    for (r = 0; r < size - 1; r++) for (c = 0; c < size - 1; c++) { var col = m[r][c]; if (col === m[r][c + 1] && col === m[r + 1][c] && col === m[r + 1][c + 1]) penalty += 3; }
    return penalty;
  }

  // ── QR → SVG 字符串（quiet zone 4 模块，路径合并所有黑格）────────────────────
  function qrSvg(text, opts) {
    var px = (opts && opts.size) || 220;
    var qr = qrEncode(text);
    var n = qr.size, border = 4, total = n + border * 2;
    var path = '';
    for (var r = 0; r < n; r++) for (var c = 0; c < n; c++) {
      if (qr.modules[r][c]) path += 'M' + (c + border) + ',' + (r + border) + 'h1v1h-1z';
    }
    return '<svg xmlns="http://www.w3.org/2000/svg" width="' + px + '" height="' + px +
      '" viewBox="0 0 ' + total + ' ' + total + '" shape-rendering="crispEdges" role="img" aria-label="登录二维码">' +
      '<rect width="' + total + '" height="' + total + '" fill="#ffffff"/>' +
      '<path d="' + path + '" fill="#000000"/></svg>';
  }
`;

export const UI_HTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Codex Bridge 管理台</title>
<style>
  :root {
    /* 火山引擎 / Arco Design 令牌：主色 #165DFF，中性灰阶 + 状态色对齐 Arco。 */
    --blue: #165dff;
    --blue-hover: #4080ff;
    --blue-dark: #0e42d2;
    --blue-tint: #f0f5ff;
    --bg: #f2f3f5;
    --card: #ffffff;
    --border: #e5e6eb;
    --border-2: #c9cdd4;
    --text: #1d2129;
    --text-2: #4e5969;
    --text-3: #86909c;
    --green: #00b42a;
    --orange: #ff7d00;
    --red: #f53f3f;
    --radius: 8px;
    --radius-sm: 6px;
    --shadow-sm: 0 1px 3px rgba(29,33,41,.06);
    --shadow-md: 0 4px 16px rgba(29,33,41,.10);
    --shadow-lg: 0 12px 40px rgba(29,33,41,.16);
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--text);
    font: 14px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
      "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .wrap { max-width: 1120px; margin: 0 auto; padding: 4px 16px 48px; }
  /* ── 产品头部：白底 + 底分隔线 + logo 徽标（火山/字节内部控制台观感，非整条蓝色块）── */
  .appbar {
    background: var(--card); border-bottom: 1px solid var(--border);
    position: sticky; top: 0; z-index: 8; box-shadow: var(--shadow-sm);
  }
  .appbar-in {
    max-width: 1120px; margin: 0 auto; padding: 11px 16px;
    display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
  }
  .brand { display: flex; align-items: center; gap: 10px; }
  .brand .logo {
    width: 32px; height: 32px; border-radius: 8px; flex: none;
    background: linear-gradient(135deg, #165dff, #4080ff); color: #fff;
    display: flex; align-items: center; justify-content: center; font-size: 17px;
    box-shadow: 0 2px 6px rgba(22,93,255,.35);
  }
  .brand h1 { font-size: 16px; margin: 0; font-weight: 600; letter-spacing: .2px; line-height: 1.2; }
  .brand .sub { font-size: 12px; color: var(--text-3); }
  .gsum { margin-left: auto; display: flex; gap: 6px; flex-wrap: wrap; }
  .gtag {
    background: var(--bg); color: var(--text-2); border: 1px solid var(--border);
    border-radius: var(--radius-sm); padding: 2px 10px; font-size: 12px; white-space: nowrap;
  }
  /* ── tabbar：Arco 胶囊 Tab（白底描边，选中蓝底白字 + 轻投影）──────────────── */
  .tabbar {
    display: flex; gap: 6px; flex-wrap: wrap; align-items: center; margin: 18px 0 2px;
  }
  .tab {
    border: 1px solid var(--border); color: var(--text-2); background: var(--card);
    border-radius: var(--radius-sm); padding: 5px 16px; font-size: 13px; cursor: pointer;
    transition: color .15s, border-color .15s, background .15s;
  }
  .tab:hover { color: var(--blue); border-color: var(--blue); background: var(--blue-tint); }
  .tab.on { background: var(--blue); color: #fff; font-weight: 600; border-color: var(--blue); box-shadow: var(--shadow-sm); }
  .tab.add { border-style: dashed; color: var(--blue); }
  .tab.add:hover { background: var(--blue-tint); }
  .cols { display: grid; grid-template-columns: minmax(0, 7fr) minmax(0, 5fr); gap: 16px; margin-top: 16px; }
  @media (max-width: 900px) { .cols { grid-template-columns: 1fr; } }
  .card {
    background: var(--card); border: 1px solid var(--border); border-radius: var(--radius);
    padding: 18px 20px; margin-bottom: 16px; box-shadow: var(--shadow-sm);
  }
  .card h2 { font-size: 15px; margin: 0 0 12px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-weight: 600; }
  .card h2 .right { margin-left: auto; font-weight: 400; }
  .hr { border: 0; border-top: 1px solid var(--border); margin: 12px 0; }
  .note { color: var(--text-3); font-size: 12px; }
  .tag {
    display: inline-block; border-radius: var(--radius-sm); padding: 1px 8px; font-size: 12px;
    background: var(--bg); color: var(--text-2); margin-right: 6px; white-space: nowrap;
  }
  .tag.blue { background: #e8f3ff; color: #165dff; }
  .tag.green { background: #e8ffea; color: #009a29; }
  .tag.orange { background: #fff3e8; color: #d25f00; }
  .tag.red { background: #ffece8; color: #cb272d; }
  .btn {
    display: inline-block; border-radius: var(--radius-sm); border: 1px solid var(--border-2);
    background: var(--card); color: var(--text); padding: 5px 16px; font-size: 13px; line-height: 20px;
    cursor: pointer; transition: color .15s, border-color .15s, background .15s;
  }
  .btn:hover { color: var(--blue); border-color: var(--blue); background: var(--blue-tint); }
  .btn.primary { background: var(--blue); border-color: var(--blue); color: #fff; }
  .btn.primary:hover { background: var(--blue-hover); border-color: var(--blue-hover); color: #fff; }
  .btn.primary:active { background: var(--blue-dark); border-color: var(--blue-dark); }
  .btn.disabled, .btn.disabled:hover { opacity: .45; cursor: not-allowed; color: var(--text); border-color: var(--border-2); background: var(--card); }
  .btn.danger, .btn.danger:hover { background: var(--red); border-color: var(--red); color: #fff; }
  .btn.danger:hover { filter: brightness(.95); }
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
    background: var(--card); z-index: 10; box-shadow: -8px 0 24px rgba(29,33,41,.12);
    transition: right .2s ease; padding: 18px 20px; overflow-y: auto;
  }
  .drawer.open { right: 0; }
  .drawer-mask.open { display: block; }
  .drawer h3 { margin: 0 0 4px; font-size: 16px; }
  .opt-row { display: flex; gap: 8px; margin: 6px 0 2px; flex-wrap: wrap; }
  .backend-row { display: flex; align-items: center; gap: 8px; padding: 6px 0; }
  .backend-row .grow { flex: 1; min-width: 0; }
  .bk-group { margin: 6px 0 2px; }
  .bk-group .gh { font-weight: 600; font-size: 13px; margin: 8px 0 2px; }
  .bk-sub { padding-left: 10px; border-left: 2px solid var(--border); margin-left: 2px; }
  /* 后端管理卡（总览）：每后端一行 + 下载进度 */
  .mgr-row { display: flex; align-items: center; gap: 10px; padding: 8px 0; border-bottom: 1px solid var(--border); flex-wrap: wrap; }
  .mgr-row:last-child { border-bottom: 0; }
  .mgr-row .grow { flex: 1; min-width: 0; }
  .progress {
    height: 6px; background: var(--bg); border-radius: 4px; overflow: hidden; margin: 6px 0 2px;
  }
  .progress > div { height: 100%; background: var(--blue); width: 0; transition: width .25s; }
  .insttail {
    background: #0e1117; color: #c9d1d9; border-radius: 6px; padding: 6px 8px; margin-top: 6px;
    font: 11px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
    max-height: 120px; overflow-y: auto; white-space: pre-wrap; word-break: break-all;
  }
  #toast {
    position: fixed; left: 50%; bottom: 36px; transform: translateX(-50%);
    background: #1f2329; color: #fff; border-radius: 8px; padding: 8px 18px;
    font-size: 13px; display: none; z-index: 20; max-width: 80vw;
  }
  .empty { color: var(--text-3); text-align: center; padding: 18px 0; }
  /* 首次使用欢迎 hero（零 bot 着陆：一句话装好桥后到这里扫码建第一个机器人）。 */
  .firstrun { text-align: center; padding: 26px 16px 22px; }
  .firstrun .fr-emoji { font-size: 40px; line-height: 1; }
  .firstrun .fr-title { font-size: 16px; font-weight: 600; margin: 12px 0 6px; color: var(--text); }
  .firstrun .fr-sub { font-size: 13px; color: var(--text-2); max-width: 420px; margin: 0 auto 16px; line-height: 1.7; }
  .firstrun .fr-cta { font-size: 14px; padding: 9px 26px; box-shadow: 0 4px 12px rgba(22,93,255,.28); }
  #wizMask, #confirmMask {
    position: fixed; inset: 0; background: rgba(0,0,0,.45); display: none; z-index: 30;
    overflow-y: auto; padding: 40px 16px;
  }
  #confirmMask { z-index: 40; }
  #wizMask.open, #confirmMask.open { display: block; }
  .switch { cursor: pointer; user-select: none; }
  .bot-row { display: flex; align-items: center; gap: 8px; padding: 9px 0; border-bottom: 1px solid var(--border); flex-wrap: wrap; }
  .bot-row:last-child { border-bottom: 0; }
  .bot-row .grow { flex: 1; min-width: 0; }
  .wiz {
    background: var(--card); border-radius: var(--radius); max-width: 560px; margin: 0 auto;
    padding: 22px 24px 26px; box-shadow: var(--shadow-lg);
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
  .qrbox {
    display: flex; flex-direction: column; align-items: center; gap: 8px; margin: 14px 0;
    padding: 14px; border: 1px solid var(--border); border-radius: var(--radius); background: #fff;
  }
  .qrbox svg { display: block; }
  .qr-count { font-size: 12px; color: var(--text-2); }
  .adv-toggle { font-size: 12px; color: var(--blue); cursor: pointer; user-select: none; margin-top: 10px; display: inline-block; }
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
<header class="appbar">
  <div class="appbar-in">
    <div class="brand">
      <span class="logo">🤖</span>
      <div>
        <h1>Codex Bridge 控制台</h1>
        <span class="sub">本机 · 127.0.0.1 · 多机器人 / 多后端统一管理</span>
      </div>
    </div>
    <div class="gsum" id="globalSummary"></div>
  </div>
</header>
<div class="wrap">
  <div class="tabbar" id="tabbar"></div>

  <!-- 单内容容器：renderRoute 按 hash 路由清空重渲（总览 / 某 bot） -->
  <div id="tabContent"></div>

  <div class="note" style="text-align:center">
    数据每 5 秒自动刷新 · 写操作（🧠 后端切换 / 🔐 权限 / ✋ 免@ / 🗜️ 自动压缩）与飞书 DM 卡片共享同一服务层；daemon 在跑时实时生效，只读预览（daemon 未跑）下不可写。
  </div>
</div>

<div class="drawer-mask" id="drawerMask"></div>
<div class="drawer" id="drawer"></div>

<!-- ➕ 添加机器人向导：内容由 JS 按步骤渲染（扫码 / 手填 → checklist → 完成） -->
<div id="wizMask"><div class="wiz" id="wizBody"></div></div>

<!-- 二次确认弹窗（重启 daemon / 删除机器人等破坏性操作） -->
<div id="confirmMask"><div class="wiz" id="confirmBody" style="max-width:440px"></div></div>

<div id="toast"></div>

<!-- 本地自带的 GSAP 动画引擎（/vendor 路由经 cookie 鉴权，不走 CDN）。同步加载置于下面
     的 app 脚本之前 → window.gsap 就绪；即便它加载失败，app 的动画层是无操作降级。 -->
<script src="/vendor/gsap.min.js"></script>
<script>
(function () {
  'use strict';

  // ── 纯逻辑片段（DOM-free，单测同款）：parseRoute / qrSvg / depTriState / … ─────
${UI_PURE_JS}

  // ── 全局单例状态 ────────────────────────────────────────────────────────────
  var state = null;          // /api/state 快照
  var daemon = null;         // /api/daemon
  var diag = null;           // /api/diagnosis 结果（当前 bot 的探测；切 Tab 必清）
  var catalog = null;        // /api/backends 缓存（后端管理卡 + 项目 picker 复用）
  var drawerProject = null;  // 抽屉里打开的项目名
  var diagBotId = null;      // diag 属于哪个 bot（防串台）

  // ── 动画层（GSAP 渐进增强）─────────────────────────────────────────────────
  // 没加载到 GSAP，或用户在系统里开了「减少动态效果」(prefers-reduced-motion) → fx.on=false，
  // 所有方法降级为「直接设最终态/无操作」，绝不影响任何功能或可访问性。动画只碰 transform/
  // autoAlpha（合成层，不触发 layout），进场只在路由切换时播一次（5s 刷新整页重渲不重放）。
  var fx = (function () {
    var g = window.gsap;
    var reduce = false;
    try { reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches; } catch (e) {}
    var on = !!g && !reduce;
    if (g) { try { g.defaults({ ease: 'power2.out', duration: 0.4 }); } catch (e) {} }
    function arr(x) {
      if (!x) return [];
      return (x.length !== undefined && !x.nodeType) ? Array.prototype.slice.call(x) : [x];
    }
    return {
      on: on,
      // 卡片/行进场：自下而上淡入 + 错峰（仅路由切换时调）。
      enter: function (targets, opts) {
        var els = arr(targets); if (!on || !els.length) return;
        opts = opts || {};
        g.from(els, {
          autoAlpha: 0, y: opts.y == null ? 12 : opts.y, duration: opts.duration || 0.42,
          stagger: opts.stagger == null ? 0.06 : opts.stagger, overwrite: 'auto',
          clearProps: 'transform,opacity,visibility',
        });
      },
      // 弹层（向导/确认）：缩放 + 上移淡入。
      popIn: function (target) {
        if (!on || !target) return;
        g.from(target, {
          autoAlpha: 0, y: 14, scale: 0.96, duration: 0.34, ease: 'back.out(1.5)',
          overwrite: 'auto', clearProps: 'transform,opacity,visibility',
        });
      },
      // toast：从下方滑入 / 滑出（!on 时直接走 done）。
      toastIn: function (target) {
        if (!on || !target) return;
        g.fromTo(target, { autoAlpha: 0, y: 16 }, { autoAlpha: 1, y: 0, duration: 0.3, overwrite: 'auto' });
      },
      toastOut: function (target, done) {
        if (!on || !target) { if (done) done(); return; }
        g.to(target, { autoAlpha: 0, y: 12, duration: 0.24, overwrite: 'auto', onComplete: done });
      },
      // 进度条宽度：平滑补间（比直接赋 width 的跳变顺；!on 时直接赋值）。
      width: function (target, pct) {
        if (!target) return;
        if (!on) { target.style.width = pct + '%'; return; }
        g.to(target, { width: pct + '%', duration: 0.4, ease: 'power1.out', overwrite: 'auto' });
      },
    };
  })();

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
    fx.toastIn(t);
    clearTimeout(t._tm);
    t._tm = setTimeout(function () {
      fx.toastOut(t, function () { t.style.display = 'none'; });
    }, 3200);
  }

  // 当前路由派生的 botId（不再是「点 Tab 改的全局变量」，从 hash 派生）。
  function currentBotId() {
    var r = parseRoute(location.hash);
    return r.tab === 'bot' ? r.botId : null;
  }

  // 二次确认弹窗（破坏性操作通用）。
  function confirmDialog(opts) {
    var mask = $('confirmMask');
    var body = $('confirmBody');
    body.textContent = '';
    body.appendChild(el('h3', null, opts.title));
    (opts.lines || []).forEach(function (line) { body.appendChild(el('div', 'note', line)); });
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
    fx.popIn(body);
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
  function connText(s) {
    if (s === 'connected') return '✅ 已连接';
    if (s === 'connecting') return '⏳ 连接中';
    if (s === 'reconnecting') return '↻ 重连中';
    if (s === 'disconnected') return '❌ 已断开';
    return s;
  }
  function familyName(f) { return f === 'codex' ? 'Codex' : f === 'claude' ? 'Claude' : f; }
  function botTitle(b) { return (b.running ? '🟢 ' : '⚪ ') + (b.botName || b.name); }

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
  function fmtBytes(n) {
    if (typeof n !== 'number' || n <= 0) return '0 B';
    var u = ['B', 'KB', 'MB', 'GB'];
    var i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
  }

  function botOf(appId) {
    if (!state) return null;
    for (var i = 0; i < state.bots.length; i++) if (state.bots[i].appId === appId) return state.bots[i];
    return null;
  }

  // ── 数据拉取 ──────────────────────────────────────────────────────────────
  function loadState() {
    fetch('/api/state').then(function (r) {
      if (r.status === 401) { toast('登录态失效：请用启动日志里带 token 的 URL 重新打开'); throw new Error('401'); }
      return r.json();
    }).then(function (s) {
      state = s;
      renderTabbar();
      renderGlobalSummary();
      // 当前可见 Tab 重渲（后台 Tab 不白费 DOM）。抽屉打开时由 renderRoute 内保留。
      renderRoute();
    }).catch(function () { /* 下个周期重试 */ });
  }
  function loadDaemon() {
    fetch('/api/daemon').then(function (r) { return r.json(); })
      .then(function (d) { daemon = d; if (parseRoute(location.hash).tab === 'overview') { var b = $('daemonBody'); if (b) renderDaemon(b); } })
      .catch(function () { /* 下个周期重试 */ });
  }
  function loadCatalog() {
    return fetch('/api/backends').then(function (r) { return r.json(); })
      .then(function (c) { catalog = c; return c; })
      .catch(function () { return null; });
  }

  // ── 路由：单 #tabContent 按 hash 清空重渲 ───────────────────────────────────
  var _lastRouteKey = null;
  function renderRoute() {
    var r = parseRoute(location.hash);
    var key = r.tab + ':' + (r.botId || '');
    var navigated = key !== _lastRouteKey; // 仅「切换路由」时播进场；5s 刷新整页重渲不重放
    _lastRouteKey = key;
    renderTabbar();
    var box = $('tabContent');
    if (!box) return;
    box.textContent = '';
    if (r.tab === 'overview') renderOverviewTab(box);
    else renderBotTab(box, r.botId);
    if (navigated) fx.enter(box.querySelectorAll('.card'));
  }

  function renderTabbar() {
    var bar = $('tabbar');
    if (!bar) return;
    bar.textContent = '';
    var route = parseRoute(location.hash);
    var ov = el('button', 'tab' + (route.tab === 'overview' ? ' on' : ''), '📊 总览');
    ov.onclick = function () { go({ tab: 'overview' }); };
    bar.appendChild(ov);
    var bots = (state && state.bots) || [];
    bots.forEach(function (b) {
      var on = route.tab === 'bot' && route.botId === b.appId;
      var t = el('button', 'tab' + (on ? ' on' : ''), botTitle(b));
      t.onclick = function () { go({ tab: 'bot', botId: b.appId }); };
      bar.appendChild(t);
    });
    var add = el('button', 'tab add', '➕ 添加机器人');
    add.onclick = function () { openWizard(); };
    bar.appendChild(add);
  }

  function renderGlobalSummary() {
    var box = $('globalSummary');
    if (!box) return;
    box.textContent = '';
    var s = summarizeState(state);
    box.appendChild(el('span', 'gtag', 'v' + ((state && state.version) || '?')));
    box.appendChild(el('span', 'gtag', '🟢 在线 ' + s.online + '/' + s.total));
    box.appendChild(el('span', 'gtag', '📁 项目 ' + s.projects));
    if (daemon && daemon.running && daemon.uptimeMs !== undefined) box.appendChild(el('span', 'gtag', '⏱ ' + fmtUptime(daemon.uptimeMs)));
  }

  // hash 路由切换：切 Tab 必清上一个 bot 的 diag（防串台），关抽屉。
  function go(route) {
    diag = null; diagBotId = null;
    closeDrawer();
    if (route.tab === 'bot') location.hash = '#bot/' + encodeURIComponent(route.botId);
    else location.hash = '#overview';
  }
  window.addEventListener('hashchange', function () { diag = null; diagBotId = null; closeDrawer(); renderRoute(); });

  // ════════════════════════════════════════════════════════════════════════════
  //  总览 Tab（全局维度）
  // ════════════════════════════════════════════════════════════════════════════
  function renderOverviewTab(root) {
    var cols = el('div', 'cols');
    var left = el('div');
    var right = el('div');

    // 🛰️ 后台 daemon + 升级
    var daemonCard = el('div', 'card');
    var dh = el('h2'); dh.appendChild(document.createTextNode('🛰️ 后台 daemon'));
    var dright = el('span', 'right');
    var restartBtn = el('button', 'btn', '🔁 重启');
    restartBtn.onclick = askRestart;
    dright.appendChild(restartBtn);
    dh.appendChild(dright);
    daemonCard.appendChild(dh);
    var daemonBody = el('div', 'note', '加载中…'); daemonBody.id = 'daemonBody';
    daemonCard.appendChild(daemonBody);
    daemonCard.appendChild(el('hr', 'hr'));
    var updateBody = el('div', 'note', '版本检查中…'); updateBody.id = 'updateBody';
    daemonCard.appendChild(updateBody);
    left.appendChild(daemonCard);
    renderDaemon(daemonBody);
    loadUpdate(updateBody);

    // 🤖 多机器人聚合
    var botsCard = el('div', 'card');
    var bh = el('h2'); bh.appendChild(document.createTextNode('🤖 多机器人 '));
    var bcount = el('span', 'right note'); bh.appendChild(bcount);
    botsCard.appendChild(bh);
    var botsList = el('div'); botsCard.appendChild(botsList);
    left.appendChild(botsCard);
    renderBots(botsList, bcount);

    // 🧠 后端管理卡
    var mgrCard = el('div', 'card');
    var mh = el('h2'); mh.appendChild(document.createTextNode('🧠 后端管理 '));
    var mright = el('span', 'right note', '装哪个后端就能用哪个 agent');
    mh.appendChild(mright);
    mgrCard.appendChild(mh);
    var mgrBody = el('div', 'note', '加载中…');
    mgrCard.appendChild(mgrBody);
    left.appendChild(mgrCard);
    renderBackendManager(mgrBody);

    // 🩺 宿主机体检
    var hostCard = el('div', 'card');
    var hh = el('h2'); hh.appendChild(document.createTextNode('🩺 宿主机体检 '));
    var hright = el('span', 'right');
    var hostBtn = el('button', 'btn', '重新检测');
    hright.appendChild(hostBtn); hh.appendChild(hright);
    hostCard.appendChild(hh);
    var hostBody = el('div', 'note', '点「重新检测」查看本机后端环境与运行时信息。');
    hostCard.appendChild(hostBody);
    hostBtn.onclick = function () { loadHostDoctor(hostBody); };
    left.appendChild(hostCard);

    // 📜 全局实时日志（SSE 连接在页面级常驻，这里只是把 ring 缓冲挂回 DOM）
    var logCard = el('div', 'card');
    var lh = el('h2'); lh.appendChild(document.createTextNode('📜 实时日志 '));
    var lstatus = el('span', 'right note', logState.status); lstatus.id = 'logStatus';
    lh.appendChild(lstatus); logCard.appendChild(lh);
    var logbox = el('div'); logbox.id = 'logbox'; logCard.appendChild(logbox);
    logCard.appendChild(el('div', 'note', '当日文件日志 SSE 实时跟随；切 Tab 不断连。'));
    right.appendChild(logCard);
    mountLogBox(logbox);

    cols.appendChild(left); cols.appendChild(right);
    root.appendChild(cols);
  }

  function renderDaemon(box) {
    box.textContent = '';
    var d = daemon;
    if (!d) { box.textContent = '加载中…'; return; }
    if (!d.supported) {
      box.appendChild(el('div', null, '⚠️ 本平台不支持后台服务'));
      box.appendChild(el('div', 'note', '用 feishu-codex-bridge run 前台运行；重启请在终端 Ctrl+C 后重跑。'));
      return;
    }
    var line = el('div', 'statline');
    if (d.running) line.appendChild(el('span', 'tag green', '✅ 运行中' + (d.pid ? ' · pid ' + d.pid : '')));
    else line.appendChild(el('span', 'tag orange', d.installed ? '⚠️ 已安装但未在运行' : '⚪ 未安装为后台服务'));
    line.appendChild(el('span', 'tag', d.platformName || '后台服务'));
    line.appendChild(el('span', 'tag blue', 'v' + d.version));
    box.appendChild(line);
    if (d.uptimeMs !== undefined && d.running) box.appendChild(el('div', 'note', '已运行 ' + fmtUptime(d.uptimeMs)));
    if (d.selfHosted) box.appendChild(el('div', 'note', '⚠️ 手动运行，未注册为开机自启 —— 关机/登出后不会自动拉起。运行 feishu-codex-bridge install 注册为后台服务。'));
    if (d.lastExit !== undefined && d.lastExit !== '0') box.appendChild(el('div', 'note', '上次退出码：' + d.lastExit));
  }

  function loadUpdate(box) {
    fetch('/api/update/check').then(function (r) { return r.json(); })
      .then(function (u) { renderUpdate(box, u); })
      .catch(function () { box.textContent = '⚠️ 版本检查失败（网络或 npm registry）'; });
  }
  function renderUpdate(box, u) {
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
  // 202 = 已发起（detached helper 接管）；501 = 只读预览（无 daemon）。
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

  // ── 多机器人聚合（总览：全局总览 + enabled 快速开关 + ➕；删除主入口在 bot Tab 头部）──
  function renderBots(box, countEl) {
    box.textContent = '';
    if (!state || state.bots.length === 0) {
      // 首次使用欢迎 hero：一句话装好桥后落到这里，醒目引导扫码建第一个机器人（全程浏览器）。
      box.className = '';
      var hero = el('div', 'firstrun');
      hero.appendChild(el('div', 'fr-emoji', '🚀'));
      hero.appendChild(el('div', 'fr-title', '欢迎！还差最后一步：创建你的第一个飞书机器人'));
      hero.appendChild(el('div', 'fr-sub', '点下面的按钮，用飞书扫码即可创建并授权——全程在这个网页里完成，不用碰终端。'));
      var cta = el('button', 'btn primary fr-cta', '➕ 扫码创建第一个机器人');
      cta.onclick = function () { openWizard(); };
      hero.appendChild(cta);
      box.appendChild(hero);
      if (countEl) countEl.textContent = '';
      return;
    }
    box.className = '';
    if (countEl) countEl.textContent = '共 ' + state.bots.length + ' 个 · 绿点=在线';
    state.bots.forEach(function (b) {
      var row = el('div', 'bot-row');
      var grow = el('div', 'grow');
      var head = el('div', 'statline');
      var nameLink = el('span', null, botTitle(b));
      nameLink.style.cursor = 'pointer';
      nameLink.style.fontWeight = '600';
      nameLink.onclick = function () { go({ tab: 'bot', botId: b.appId }); };
      head.appendChild(nameLink);
      head.appendChild(el('span', 'tag', b.tenant === 'lark' ? 'Lark' : '飞书'));
      head.appendChild(el('span', 'tag', b.appId));
      if (b.current) head.appendChild(el('span', 'tag blue', '主'));
      grow.appendChild(head);
      grow.appendChild(el('div', 'note',
        (b.running ? '运行中' + (b.pid ? ' · pid ' + b.pid : '') : '未在运行') +
        ' · ' + ((b.projects && b.projects.length) || 0) + ' 个项目'));
      row.appendChild(grow);
      var sw = el('button', 'btn' + (b.active ? ' primary' : ''), b.active ? '✅ 已启用' : '⛔ 已停用');
      sw.title = b.active ? '点一下停用（退出活跃集）' : '点一下启用（加入活跃集）';
      sw.onclick = function () { toggleBotEnabled(b); };
      row.appendChild(sw);
      var open = el('button', 'btn', '管理 →');
      open.onclick = function () { go({ tab: 'bot', botId: b.appId }); };
      row.appendChild(open);
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
              go({ tab: 'overview' });
              loadState();
            } else toast('❌ ' + (resp.body.message || ('HTTP ' + resp.status)));
          })
          .catch(function () { toast('❌ 请求失败'); });
      },
    });
  }

  // ── 🧠 后端管理卡（按 agentFamily 分组：已装✅/未装⬇️下载/external 手动）──────
  function renderBackendManager(box) {
    box.textContent = '';
    box.className = 'note';
    if (!catalog) {
      box.textContent = '加载中…';
      loadCatalog().then(function (c) { if (c && parseRoute(location.hash).tab === 'overview') renderBackendManager(box); });
      return;
    }
    box.className = '';
    box.appendChild(el('div', 'note', '当前全局默认后端：' + catalog.defaultBackend + '（项目未显式选择时路由到它）'));
    groupBackends(catalog.entries).forEach(function (grp) {
      var g = el('div', 'bk-group');
      var gh = el('div', 'gh', familyName(grp.family));
      g.appendChild(gh);
      var sub = el('div', 'bk-sub');
      grp.entries.forEach(function (e) { sub.appendChild(renderBackendManagerRow(e, box)); });
      g.appendChild(sub);
      box.appendChild(g);
    });
  }

  function renderBackendManagerRow(e, mgrBox) {
    var row = el('div', 'mgr-row');
    var grow = el('div', 'grow');
    var tri = depTriState(e);
    var head = el('div', 'statline');
    var stateTag = tri.state === 'installed' ? 'tag green' : tri.state === 'downloadable' ? 'tag blue' : 'tag orange';
    head.appendChild(el('span', null, e.displayName + (e.version ? ' ' + e.version : '') + (e.isDefault ? '（默认）' : '')));
    head.appendChild(el('span', stateTag, tri.label));
    head.appendChild(el('span', 'tag', e.access));
    if (e.approxSizeMB) head.appendChild(el('span', 'tag', '约 ' + e.approxSizeMB + 'M'));
    grow.appendChild(head);
    if (e.blurb) grow.appendChild(el('div', 'note', e.blurb));
    if (tri.action === 'manual' && e.hint) grow.appendChild(el('div', 'note', '装法：' + e.hint));
    row.appendChild(grow);

    if (tri.action === 'download') {
      var dl = el('button', 'btn primary', '⬇️ 下载' + (e.approxSizeMB ? '（约 ' + e.approxSizeMB + 'M）' : ''));
      dl.onclick = function () { startBackendInstall(e, row, dl); };
      row.appendChild(dl);
    }
    return row;
  }

  // 后端按需安装：POST /api/backends/:id/install 的 SSE（{type:'log'} → done/error）。
  function startBackendInstall(entry, row, btn) {
    btn.className = 'btn primary disabled'; btn.disabled = true; btn.textContent = '⏳ 安装中…';
    var bar = el('div', 'progress'); var fill = el('div'); bar.appendChild(fill);
    row.appendChild(bar);
    var tail = el('div', 'insttail');
    row.appendChild(tail);
    var lines = 0;
    // 224M 下载没有真进度百分比 —— 用「日志行数」做一个观感进度（封顶 92% 等 done）。
    function bump() { lines++; var pct = Math.min(92, 8 + lines * 4); fx.width(fill, pct); }

    fetch('/api/backends/' + encodeURIComponent(entry.id) + '/install', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
    }).then(function (resp) {
      if (!resp.body || !resp.body.getReader) { tail.textContent = '（浏览器不支持流式读取，请改用终端安装）'; return; }
      var reader = resp.body.getReader();
      var dec = new TextDecoder();
      var buf = '';
      function pump() {
        return reader.read().then(function (chunk) {
          if (chunk.done) return;
          buf += dec.decode(chunk.value, { stream: true });
          var parts = buf.split('\\n\\n');
          buf = parts.pop();
          parts.forEach(function (block) { handleInstallBlock(block, tail, fill, btn, entry, bump); });
          return pump();
        });
      }
      return pump();
    }).catch(function () {
      btn.className = 'btn primary'; btn.disabled = false; btn.textContent = '⬇️ 重试';
      tail.textContent += '\\n❌ 安装请求失败';
    });
  }

  function handleInstallBlock(block, tail, fill, btn, entry, bump) {
    var msg = parseSseDataBlock(block);
    if (!msg) return;
    if (msg.type === 'log') {
      bump();
      tail.textContent += (tail.textContent ? '\\n' : '') + msg.chunk;
      tail.scrollTop = tail.scrollHeight;
    } else if (msg.type === 'done') {
      fx.width(fill, 100);
      btn.textContent = '✅ 已安装'; btn.className = 'btn disabled';
      toast('✅ 「' + entry.displayName + '」安装完成');
      // 刷新 catalog（depState 转 installed）→ 重渲后端管理卡。
      loadCatalog().then(function () { if (parseRoute(location.hash).tab === 'overview') renderRoute(); });
    } else if (msg.type === 'error') {
      btn.className = 'btn primary'; btn.disabled = false; btn.textContent = '⬇️ 重试';
      var hint = msg.code === 'not_wired_yet'
        ? '需要 daemon 在跑（当前为只读预览）'
        : msg.code === 'aborted' ? '已取消' : (msg.message || '安装失败');
      tail.textContent += (tail.textContent ? '\\n' : '') + '❌ ' + hint;
      toast('❌ ' + hint);
    }
  }

  // ── 宿主机体检 ──────────────────────────────────────────────────────────────
  function loadHostDoctor(box) {
    box.textContent = '🔍 正在检测…';
    fetch('/api/host-doctor').then(function (r) { return r.json(); })
      .then(function (h) { renderHostDoctor(box, h); })
      .catch(function () { box.textContent = '⚠️ 体检请求失败'; });
  }
  function renderHostDoctor(box, h) {
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

  // ════════════════════════════════════════════════════════════════════════════
  //  每 bot 一个 Tab（单 bot 维度）
  // ════════════════════════════════════════════════════════════════════════════
  function renderBotTab(root, botId) {
    var b = botOf(botId);
    if (!b) {
      var empty = el('div', 'card');
      empty.appendChild(el('div', 'empty', '这个机器人不存在或已删除。'));
      var back = el('button', 'btn primary', '← 回总览');
      back.onclick = function () { go({ tab: 'overview' }); };
      empty.appendChild(back);
      root.appendChild(empty);
      return;
    }

    // 头部：bot 概览 + 停用/删除（下沉到 bot Tab 头部）
    var headCard = el('div', 'card');
    var hh = el('h2');
    hh.appendChild(document.createTextNode('📡 ' + (b.botName || b.name) + ' '));
    var hright = el('span', 'right');
    var sw = el('button', 'btn' + (b.active ? ' primary' : ''), b.active ? '✅ 已启用' : '⛔ 已停用');
    sw.onclick = function () { toggleBotEnabled(b); };
    hright.appendChild(sw);
    var del = el('button', 'btn danger', '🗑️ 删除');
    del.style.marginLeft = '8px';
    del.onclick = function () { askDeleteBot(b); };
    hright.appendChild(del);
    hh.appendChild(hright);
    headCard.appendChild(hh);
    renderBotOverview(headCard, b);
    root.appendChild(headCard);

    var cols = el('div', 'cols');
    var left = el('div');
    var right = el('div');

    // 🩺 事件订阅诊断 + setup-status
    var diagCard = el('div', 'card');
    var dh = el('h2'); dh.appendChild(document.createTextNode('🩺 接入诊断 '));
    var dright = el('span', 'right');
    var diagBtn = el('button', 'btn', '🩺 诊断');
    diagBtn.onclick = function () { loadDiagnosis(botId, diagBody); };
    dright.appendChild(diagBtn); dh.appendChild(dright);
    diagCard.appendChild(dh);
    var diagBody = el('div', 'note', '点「🩺 诊断」检测事件订阅与各后端环境（约 3 秒）。');
    diagCard.appendChild(diagBody);
    left.appendChild(diagCard);
    // 切回来时若 diag 仍属本 bot，直接重渲。
    if (diag && diagBotId === botId) renderDiagnosis(diagBody);

    // 📁 项目列表
    var projCard = el('div', 'card');
    var ph = el('h2'); ph.appendChild(document.createTextNode('📁 项目列表 '));
    var pcount = el('span', 'right note'); ph.appendChild(pcount);
    projCard.appendChild(ph);
    var projList = el('div'); projCard.appendChild(projList);
    left.appendChild(projCard);
    renderProjects(projList, pcount, b);

    cols.appendChild(left);
    cols.appendChild(right);
    root.appendChild(cols);

    // 抽屉若在该 bot 打开过，恢复（轮询不刷没）。
    if (drawerProject) renderDrawer(drawerProject);
  }

  function renderBotOverview(card, b) {
    var line1 = el('div', 'statline');
    line1.appendChild(el('span', 'tag', b.tenant === 'lark' ? 'Lark' : '飞书'));
    line1.appendChild(el('span', 'tag', b.appId));
    if (b.current) line1.appendChild(el('span', 'tag blue', '主 bot'));
    if (b.active) line1.appendChild(el('span', 'tag blue', '活跃集'));
    card.appendChild(line1);
    var line2 = el('div', 'statline');
    if (b.running) {
      line2.appendChild(el('span', 'tag green', '✅ bridge 运行中 · pid ' + b.pid));
      if (b.connection) {
        var connOk = b.connection === 'connected';
        line2.appendChild(el('span', 'tag ' + (connOk ? 'green' : 'orange'), '长连接 ' + connText(b.connection)));
      }
    } else {
      line2.appendChild(el('span', 'tag orange', '⚠️ bridge 未在运行'));
      line2.appendChild(el('span', 'note', '终端执行 run / start 后这里显示实时状态'));
    }
    card.appendChild(line2);
    card.appendChild(el('div', 'note', '版本 v' + state.version + ' · 快照 ' + new Date(state.generatedAt).toLocaleTimeString()));
  }

  function loadDiagnosis(botId, box) {
    box.textContent = '🔍 正在检测事件订阅与各后端环境…';
    fetch('/api/diagnosis?bot=' + encodeURIComponent(botId)).then(function (r) { return r.json(); })
      .then(function (d) {
        diag = d; diagBotId = botId;
        // 只有当前仍停在该 bot Tab 才渲染（避免切走后串台）。
        if (currentBotId() === botId) renderDiagnosis(box);
      })
      .catch(function () { box.textContent = '⚠️ 诊断请求失败'; });
  }
  function renderDiagnosis(box) {
    box.textContent = '';
    box.className = 'note';
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

  function renderProjects(box, countEl, b) {
    box.textContent = '';
    var projects = (b && b.projects) || [];
    if (countEl) countEl.textContent = '共 ' + projects.length + ' 个项目';
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

  // ── 项目详情抽屉 ────────────────────────────────────────────────────────────
  function openDrawer(name) {
    drawerProject = name;
    renderDrawer(name);
    $('drawer').classList.add('open');
    $('drawerMask').classList.add('open');
  }
  function closeDrawer() {
    drawerProject = null;
    var d = $('drawer'); if (d) d.classList.remove('open');
    var m = $('drawerMask'); if (m) m.classList.remove('open');
  }

  function findProject(name) {
    var b = botOf(currentBotId());
    var list = (b && b.projects) || [];
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    return null;
  }

  // 写操作统一入口：200 ✅；501 = 只读预览；其余把服务端中文 message 弹出来。
  function postWrite(path, body) {
    var botId = currentBotId();
    fetch(path + '?bot=' + encodeURIComponent(botId), {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
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

    // 🔐 权限
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

    // 🧠 后端 —— 按 agent 分组 picker（catalog 驱动；未装灰显「去总览下载」；档位不支持灰显）
    d.appendChild(el('div', null, '🧠 后端'));
    d.appendChild(el('div', 'note', '当前 ' + p.backend + ' · 切换只对新话题生效；已有话题会话仍走原后端'));
    renderBackendPicker(d, p);
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
    fetch('/api/project/' + encodeURIComponent(p.name) + '/sessions?bot=' + encodeURIComponent(currentBotId()))
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

  // 项目后端 picker：按 agent 分组（catalog）；未装的灰显 + 「去总览下载」；
  // 档位不支持灰显（supportedModes 不含项目档）；已装可切走 /backend 写路由。
  function renderBackendPicker(d, p) {
    if (!catalog) {
      d.appendChild(el('div', 'note', '（正在加载后端列表…）'));
      loadCatalog().then(function () { if (drawerProject === p.name) renderDrawer(p.name); });
      return;
    }
    groupBackends(catalog.entries).forEach(function (grp) {
      var g = el('div', 'bk-group');
      g.appendChild(el('div', 'gh', familyName(grp.family)));
      var sub = el('div', 'bk-sub');
      grp.entries.forEach(function (e) { sub.appendChild(renderPickerRow(e, p)); });
      g.appendChild(sub);
      d.appendChild(g);
    });
  }

  function renderPickerRow(e, p) {
    var row = el('div', 'backend-row');
    var grow = el('div', 'grow');
    var tri = depTriState(e);
    var isCurrent = e.id === p.backend;
    // 档位不支持：supportedModes 存在且不含项目两档之一（沿用 validateBackendSwitch 语义）。
    var modes = e.supportedModes || null;
    var modeBlocked = modes && (modes.indexOf(p.mode) < 0 || modes.indexOf(p.guestMode) < 0);
    var label = e.displayName + (e.version ? ' ' + e.version : '');
    grow.appendChild(el('div', null, label + (e.isDefault ? '（默认）' : '') + (isCurrent ? ' · ✓ 使用中' : '')));
    var sub = el('div', 'note', e.access + (e.blurb ? ' · ' + e.blurb : ''));
    grow.appendChild(sub);
    if (tri.state !== 'installed') {
      grow.appendChild(el('div', 'note', tri.action === 'download' ? '未安装 —— 去「📊 总览 › 🧠 后端管理」下载' : ('未安装 —— ' + (e.hint || '需手动安装'))));
    } else if (modeBlocked) {
      grow.appendChild(el('div', 'note', '该后端仅支持 ' + (modes || []).join('/') + ' 档；当前项目权限档不兼容，先调权限再切。'));
    }
    row.appendChild(grow);

    if (tri.state === 'installed' && !isCurrent && !modeBlocked) {
      var sw = el('button', 'btn primary', '切换');
      sw.onclick = function () { postWrite('/api/project/' + encodeURIComponent(p.name) + '/backend', { backend: e.id }); };
      row.appendChild(sw);
    } else if (tri.state !== 'installed' && tri.action === 'download') {
      var goDl = el('button', 'btn', '去下载');
      goDl.onclick = function () { closeDrawer(); go({ tab: 'overview' }); };
      row.appendChild(goDl);
    }
    return row;
  }

  // ════════════════════════════════════════════════════════════════════════════
  //  ➕ 添加机器人向导（扫码为主 + 手填降级折叠 → checklist → 完成）
  // ════════════════════════════════════════════════════════════════════════════
  var wizStep = 1;          // 1=扫码/手填 2=checklist 3=完成
  var wizBotId = null;
  var wizPoll = null;       // setup-status 轮询定时器
  var wizSetup = null;
  var wizTenant = 'feishu';
  var wizManualOpen = false;
  var wizEs = null;         // 扫码 SSE EventSource
  var wizQrSessionId = null;
  var wizCountdown = null;  // 二维码过期倒计时

  function openWizard() {
    wizStep = 1; wizBotId = null; wizSetup = null; wizManualOpen = false;
    stopWizPoll(); stopWizQr();
    $('wizMask').classList.add('open');
    renderWizard();
    fx.popIn($('wizBody'));
  }
  function closeWizard() {
    stopWizPoll(); stopWizQr();
    $('wizMask').classList.remove('open');
    loadState();
  }
  function stopWizPoll() { if (wizPoll) { clearInterval(wizPoll); wizPoll = null; } }
  function stopWizQr() {
    if (wizEs) { try { wizEs.close(); } catch (e) {} wizEs = null; }
    if (wizCountdown) { clearInterval(wizCountdown); wizCountdown = null; }
    if (wizQrSessionId) {
      // 主动取消后台扫码会话（防僵尸轮询打飞书）。
      fetch('/api/bots/register-qr?sessionId=' + encodeURIComponent(wizQrSessionId), { method: 'DELETE' }).catch(function () {});
      wizQrSessionId = null;
    }
  }

  function wizStepBar(active) {
    var bar = el('div', 'steps');
    ['① 扫码创建', '② 接入检测', '③ 完成'].forEach(function (lab, i) {
      var n = i + 1;
      bar.appendChild(el('div', 'step' + (n === active ? ' on' : (n < active ? ' done' : '')), lab));
    });
    return bar;
  }

  function renderWizard() {
    if (wizStep === 1) return renderWizScan();
    if (wizStep === 2) return renderWizChecklist();
    return renderWizDone();
  }

  // ── ① 扫码（默认主 CTA）+ 手填折叠（降级 fallback）─────────────────────────
  function renderWizScan() {
    var w = $('wizBody');
    w.textContent = '';
    w.appendChild(el('h3', null, '➕ 添加机器人'));
    w.appendChild(el('div', 'note', '用飞书 App 扫一下二维码 —— 自动创建应用、拿密钥入库、设你为管理员。'));
    w.appendChild(wizStepBar(1));

    var qrWrap = el('div', 'qrbox');
    qrWrap.id = 'wizQrWrap';
    qrWrap.appendChild(el('div', 'note', '正在生成二维码…'));
    w.appendChild(qrWrap);

    var statusLine = el('div', 'note'); statusLine.id = 'wizScanStatus';
    statusLine.style.textAlign = 'center';
    w.appendChild(statusLine);

    // 手填降级（折叠次级入口）
    var toggle = el('span', 'adv-toggle', wizManualOpen ? '收起手填 ▲' : '已有飞书应用？手动填 App ID/Secret →');
    toggle.onclick = function () { wizManualOpen = !wizManualOpen; renderWizManual(manualBox); toggle.textContent = wizManualOpen ? '收起手填 ▲' : '已有飞书应用？手动填 App ID/Secret →'; };
    w.appendChild(toggle);
    var manualBox = el('div'); w.appendChild(manualBox);
    renderWizManual(manualBox);

    var actions = el('div', 'actions');
    var cancel = el('button', 'btn', '取消');
    cancel.onclick = closeWizard;
    actions.appendChild(cancel);
    w.appendChild(actions);

    startWizQr();
  }

  // 扫码 SSE：EventSource /api/bots/register-qr/stream → qr / status / done / error。
  function startWizQr() {
    stopWizQr();
    var es = new EventSource('/api/bots/register-qr/stream');
    wizEs = es;
    es.addEventListener('qr', function (ev) {
      var info; try { info = JSON.parse(ev.data); } catch (e) { return; }
      wizQrSessionId = info.sessionId || null;
      renderWizQrCode(info.qrUrl, info.expireIn);
      setScanStatus('📱 用飞书扫码并确认创建…');
    });
    es.addEventListener('status', function (ev) {
      var info; try { info = JSON.parse(ev.data); } catch (e) { return; }
      setScanStatus(scanStatusText(info.status));
    });
    es.addEventListener('done', function (ev) {
      var info; try { info = JSON.parse(ev.data); } catch (e) { return; }
      stopWizQr();
      wizBotId = info.appId;
      wizSetup = info.botName ? { botName: info.botName } : null;
      wizStep = 2; renderWizard(); startWizPoll();
    });
    es.addEventListener('error', function (ev) {
      // EventSource 原生 onerror（无 data）只是连接抖动；带 data 的是服务端 error 事件。
      if (!ev.data) return;
      var info; try { info = JSON.parse(ev.data); } catch (e) { return; }
      var text = scanErrorText(info.code, info.message);
      stopWizQr();
      if (text === null) return; // abort：静默不弹
      renderWizQrError(text);
    });
  }

  function setScanStatus(msg) { var s = $('wizScanStatus'); if (s) s.textContent = msg; }

  function renderWizQrCode(url, expireIn) {
    var wrap = $('wizQrWrap');
    if (!wrap) return;
    wrap.textContent = '';
    var holder = el('div');
    holder.innerHTML = qrSvg(url, { size: 220 });
    wrap.appendChild(holder);
    var cd = el('div', 'qr-count');
    wrap.appendChild(cd);
    var link = el('a', null, '也可以在浏览器打开 ↗');
    link.href = url; link.target = '_blank'; link.rel = 'noopener';
    link.style.fontSize = '12px';
    wrap.appendChild(link);
    var remain = typeof expireIn === 'number' ? expireIn : 600;
    if (wizCountdown) clearInterval(wizCountdown);
    function tick() {
      if (remain <= 0) {
        clearInterval(wizCountdown); wizCountdown = null;
        cd.textContent = '二维码已过期';
        renderWizQrExpired();
        return;
      }
      cd.textContent = '二维码 ' + remain + ' 秒后过期';
      remain--;
    }
    tick();
    wizCountdown = setInterval(tick, 1000);
  }

  function renderWizQrExpired() {
    var wrap = $('wizQrWrap');
    if (!wrap) return;
    var re = el('button', 'btn primary', '🔄 重新生成二维码');
    re.style.marginTop = '8px';
    re.onclick = function () { startWizQr(); var w = $('wizQrWrap'); if (w) { w.textContent = ''; w.appendChild(el('div', 'note', '正在生成二维码…')); } };
    wrap.appendChild(re);
  }

  function renderWizQrError(text) {
    var wrap = $('wizQrWrap');
    if (!wrap) return;
    wrap.textContent = '';
    wrap.appendChild(el('div', 'note', '❌ ' + text));
    var re = el('button', 'btn primary', '🔄 重新生成');
    re.style.marginTop = '8px';
    re.onclick = function () { wrap.textContent = ''; wrap.appendChild(el('div', 'note', '正在生成二维码…')); startWizQr(); };
    wrap.appendChild(re);
  }

  // ── 手填降级（折叠面板，接既有 POST /api/bots）──────────────────────────────
  function renderWizManual(box) {
    box.textContent = '';
    if (!wizManualOpen) return;
    box.appendChild(el('hr', 'hr'));
    box.appendChild(el('label', null, 'App ID'));
    var idIn = el('input'); idIn.type = 'text'; idIn.id = 'wizAppId'; idIn.placeholder = 'cli_xxxxxxxxxxxxxxxx'; idIn.autocomplete = 'off';
    box.appendChild(idIn);
    box.appendChild(el('label', null, 'App Secret'));
    var secIn = el('input'); secIn.type = 'password'; secIn.id = 'wizAppSecret'; secIn.placeholder = '••••••••••••••••'; secIn.autocomplete = 'new-password';
    box.appendChild(secIn);
    box.appendChild(el('div', 'note', '🔒 密钥仅用于一次性探活验证后，加密存储在本机 keystore（AES-256-GCM）；不回显、不进日志。'));
    box.appendChild(el('label', null, '版本'));
    var radioRow = el('div', 'radio-row');
    [['feishu', '飞书（feishu.cn）'], ['lark', 'Lark（larksuite.com）']].forEach(function (pair) {
      var lbl = el('label'); lbl.style.fontWeight = '400'; lbl.style.margin = '0';
      var r = el('input'); r.type = 'radio'; r.name = 'wizTenant'; r.value = pair[0];
      if (pair[0] === wizTenant) r.checked = true;
      r.onchange = function () { wizTenant = pair[0]; };
      lbl.appendChild(r); lbl.appendChild(document.createTextNode(' ' + pair[1]));
      radioRow.appendChild(lbl);
    });
    box.appendChild(radioRow);
    var msg = el('div', 'note'); msg.id = 'wizFormMsg'; msg.style.color = 'var(--red)';
    box.appendChild(msg);
    var submit = el('button', 'btn primary', '验证并添加'); submit.id = 'wizSubmit';
    submit.style.marginTop = '8px';
    submit.onclick = submitWizForm;
    box.appendChild(submit);
  }

  function submitWizForm() {
    var appId = (($('wizAppId') || {}).value || '').trim();
    var appSecret = (($('wizAppSecret') || {}).value || '').trim();
    var msg = $('wizFormMsg');
    if (msg) msg.textContent = '';
    if (!appId || !appSecret) { if (msg) msg.textContent = 'App ID 与 App Secret 都要填。'; return; }
    var btn = $('wizSubmit');
    btn.textContent = '验证中…'; btn.className = 'btn primary disabled'; btn.disabled = true;
    fetch('/api/bots', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ appId: appId, appSecret: appSecret, tenant: wizTenant }),
    }).then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (resp) {
        if ($('wizAppSecret')) $('wizAppSecret').value = '';
        if (resp.status === 201 && resp.body.ok) {
          stopWizQr();
          wizBotId = resp.body.bot.appId;
          wizStep = 2; renderWizard(); startWizPoll();
        } else {
          btn.textContent = '验证并添加'; btn.className = 'btn primary'; btn.disabled = false;
          if (msg) msg.textContent = '❌ ' + (resp.body.message || ('添加失败（HTTP ' + resp.status + '）'));
        }
      })
      .catch(function () {
        btn.textContent = '验证并添加'; btn.className = 'btn primary'; btn.disabled = false;
        if (msg) msg.textContent = '❌ 请求失败，请重试。';
      });
  }

  // ── ② checklist（复用 getSetupStatus 5s 轮询）─────────────────────────────
  function startWizPoll() { stopWizPoll(); pollWizSetup(); wizPoll = setInterval(pollWizSetup, 5000); }
  function pollWizSetup() {
    if (!wizBotId) return;
    fetch('/api/bots/' + encodeURIComponent(wizBotId) + '/setup-status')
      .then(function (r) { return r.json(); })
      .then(function (s) {
        wizSetup = s;
        if (wizStep === 2) renderWizChecklist();
        if (s.event && s.event.state === 'ok') stopWizPoll();
      })
      .catch(function () { /* 下个周期重试 */ });
  }

  function checkItem(ico, title, desc, extra) {
    var item = el('div', 'check-item');
    var ic = el('div', 'ico');
    if (ico === 'spin') ic.appendChild(el('span', 'spin')); else ic.textContent = ico;
    item.appendChild(ic);
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
    if (!s || !s.credentials) {
      w.appendChild(checkItem('spin', '正在检测…', '首次拉取约 2~3 秒'));
      w.appendChild(wizChecklistActions(false));
      return;
    }
    w.appendChild(checkItem(
      s.credentials && s.credentials.ok ? '✅' : '❌',
      '密钥有效',
      s.credentials && s.credentials.ok ? '凭据探活通过' + (s.botName ? '（' + s.botName + '）' : '') : ('凭据校验失败：' + ((s.credentials && s.credentials.reason) || '未知'))
    ));
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

  // ── ③ 完成：建群引导 + 跳到该 bot 的 Tab ─────────────────────────────────────
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
    w.appendChild(el('div', 'note', '提示：私聊机器人发任意消息即可唤出私聊管理台；Web 控制台与私聊卡片共享同一套设置，双端实时一致。'));
    // 新建的机器人要等 daemon 重启、被 supervisor 接管后才真正上线（尤其「引导控制台」是
    // 零 bot 起的，得重启才会连上这个新 bot）。给一条醒目提示 + 一键重启（确认弹窗里会说明
    // 重启会短暂打断其它在跑的机器人）。
    var liveTip = el('div', 'note');
    liveTip.style.cssText = 'margin-top:10px;padding:10px 12px;background:var(--blue-tint);border-radius:8px;color:var(--text-2)';
    liveTip.textContent = '⚡ 让它上线：新机器人需重启 daemon 后由后台接管。点下面「重启使其上线」即可（首次创建时重启很安全，不影响别的机器人）。';
    w.appendChild(liveTip);
    var actions = el('div', 'actions');
    var restart = el('button', 'btn', '🔁 重启使其上线');
    restart.onclick = askRestart;
    actions.appendChild(restart);
    actions.appendChild(el('div', 'grow'));
    var done = el('button', 'btn primary', '完成并进入该机器人 →');
    done.onclick = function () { var id = wizBotId; closeWizard(); if (id) go({ tab: 'bot', botId: id }); };
    actions.appendChild(done);
    w.appendChild(actions);
  }

  // ── 日志 SSE（页面级常驻 ring 缓冲；切 Tab 不断连，重新 mount 把缓冲挂回 DOM）──
  var MAX_LOG_LINES = 500;
  var logState = { status: '连接中…', lines: [], box: null };
  function startLogStream() {
    var es = new EventSource('/api/logs/stream');
    es.onopen = function () { logState.status = '🟢 已连接'; setLogStatus(); };
    es.onerror = function () { logState.status = '🔁 重连中…'; setLogStatus(); };
    es.onmessage = function (ev) {
      if (!ev.data) return;
      var cls = null;
      if (ev.data.indexOf('stream.timing') >= 0 || ev.data.indexOf('"phase":"agent"') >= 0 || ev.data.indexOf('agent.') >= 0) cls = 'hl';
      if (ev.data.indexOf('"level":"warn"') >= 0) cls = 'warn';
      if (ev.data.indexOf('"level":"error"') >= 0) cls = 'err';
      logState.lines.push({ text: ev.data, cls: cls });
      while (logState.lines.length > MAX_LOG_LINES) logState.lines.shift();
      appendLogLine(logState.lines[logState.lines.length - 1]);
    };
  }
  function setLogStatus() { var s = $('logStatus'); if (s) s.textContent = logState.status; }
  function mountLogBox(box) {
    logState.box = box;
    box.textContent = '';
    logState.lines.forEach(function (ln) { box.appendChild(el('div', ln.cls, ln.text)); });
    box.scrollTop = box.scrollHeight;
    setLogStatus();
  }
  function appendLogLine(ln) {
    var box = logState.box;
    if (!box || !box.isConnected) return; // 不在当前 Tab：只进 ring，不碰 DOM
    var stick = box.scrollTop + box.clientHeight >= box.scrollHeight - 24;
    box.appendChild(el('div', ln.cls, ln.text));
    while (box.childNodes.length > MAX_LOG_LINES) box.removeChild(box.firstChild);
    if (stick) box.scrollTop = box.scrollHeight;
  }

  // ── 启动 ─────────────────────────────────────────────────────────────────
  $('drawerMask').onclick = closeDrawer;
  $('confirmMask').onclick = function (e) { if (e.target === $('confirmMask')) $('confirmMask').classList.remove('open'); };
  startLogStream();
  loadCatalog();
  loadState();
  loadDaemon();
  renderRoute();
  // 一次性首屏入场：白色产品头部从上滑入（仅首次，5s 刷新不重放）。
  if (fx.on) {
    try {
      window.gsap.from('.appbar', { autoAlpha: 0, y: -14, duration: 0.45, clearProps: 'transform,opacity,visibility' });
    } catch (e) {}
  }
  setInterval(loadState, 5000);  // /api/state 5s 刷新
  setInterval(loadDaemon, 5000); // daemon 状态/时长同频
})();
</script>
</body>
</html>
`;
