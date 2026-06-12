import type { ModelInfo, ReasoningEffort, ThreadSummary } from '../agent/types';
import { actions, button, card, hr, linkButton, md, note, selectStatic, type CardElement, type CardObject } from './cards';

/** Action ids for the `/model` card. */
export const MC = {
  model: 'model.set',
  effort: 'model.effort',
} as const;

/** Action ids for the `/resume` card. */
export const RES = {
  pick: 'resume.pick',
} as const;

const EFFORT_LABEL: Record<ReasoningEffort, string> = {
  none: '无',
  minimal: '极简',
  low: '低',
  medium: '中',
  high: '高',
  xhigh: '极高',
};

// ── /model ────────────────────────────────────────────────────────────────

/** Server-side state for a pending `/model` card, keyed by its messageId. */
export interface ModelCardState {
  chatId: string;
  /** the topic (session) whose model/effort this card edits */
  threadId: string;
  requesterOpenId: string;
  models: ModelInfo[];
  model: string;
  effort: ReasoningEffort;
  createdAt: number;
  /** transient confirmation line */
  note?: string;
}

/** The `/model` card: pick model + reasoning effort for the current session. */
export function buildModelCard(state: ModelCardState): CardObject {
  const visible = state.models.filter((m) => !m.hidden);
  const cur = state.models.find((m) => m.id === state.model);
  const efforts = cur?.supportedEfforts.length ? cur.supportedEfforts : (['low', 'medium', 'high'] as ReasoningEffort[]);
  const elements = [
    md('🧠 **模型 / 推理强度**'),
    note('选择后下一轮生效'),
    hr(),
    actions([
      selectStatic({
        actionId: MC.model,
        placeholder: '选择模型',
        initial: state.model,
        options: visible.map((m) => ({ label: m.displayName, value: m.id })),
      }),
      selectStatic({
        actionId: MC.effort,
        placeholder: 'effort',
        initial: state.effort,
        options: efforts.map((e) => ({ label: `effort：${EFFORT_LABEL[e]}`, value: e })),
      }),
    ]),
  ];
  if (state.note) elements.push(note(state.note));
  return card(elements, { summary: '模型设置' });
}

// ── /resume ─────────────────────────────────────────────────────────────────

/** Server-side state for a pending `/resume` card, keyed by its messageId. */
export interface ResumeCardState {
  chatId: string;
  /** the `@bot /resume` message — reply_in_thread to it creates the topic */
  originalMsgId: string;
  requesterOpenId: string;
  cwd: string;
  projectName?: string;
  threads: ThreadSummary[];
  createdAt: number;
  /** in-flight guard (anti double-click) */
  launching?: boolean;
}

/** Max length of the session title shown inside a picker button. */
const RESUME_TITLE_MAX = 30;

/**
 * The `/resume` card: recent codex threads under this cwd. Each thread is ONE
 * button labeled `↩️ <time> · <title>` (time first, title truncated to one line)
 * — modeled on codex's own `resume` TUI — so it's unambiguous which button
 * resumes which session even when titles are long, messy, or repeated. Same-
 * title sessions are told apart by the minute-precise timestamp.
 */
export function buildResumeCard(state: ResumeCardState): CardObject {
  const elements = [md('🕘 **恢复历史会话**'), note(metaNote(state)), hr()];
  if (state.threads.length === 0) {
    elements.push(md('_该目录下还没有历史会话。直接 @我 即可新建。_'));
  } else {
    elements.push(note('点一条即恢复 —— 在新话题里打开历史、可直接继续。'));
    for (const t of state.threads) {
      const title = (t.name?.trim() || t.preview.trim() || '(无摘要)').replace(/\s+/g, ' ');
      const label = `↩️ ${pickerTime(t.updatedAt || t.createdAt)} · ${truncate(title, RESUME_TITLE_MAX)}`;
      elements.push(actions([button(label, { a: RES.pick, t: t.codexThreadId })]));
    }
  }
  return card(elements, { summary: '恢复历史会话' });
}

/** Transient "resuming…" card — interactive controls removed (anti double-click). */
export function buildResumeLaunchingCard(state: ResumeCardState): CardObject {
  return card([md('⏳ 正在恢复历史会话…'), note(metaNote(state))], { summary: '恢复中' });
}

/** Terminal success card — the resumed session opened as a new topic above. */
export function buildResumeDoneCard(state: ResumeCardState): CardObject {
  return card([md('✅ 已恢复 —— 已在上方新话题打开，可直接继续。'), note(metaNote(state))], { summary: '已恢复' });
}

/** Failure card after a failed resume launch. */
export function buildResumeErrorCard(state: ResumeCardState, message: string): CardObject {
  return card([md(`❌ 恢复失败：${truncate(message, 200)}`), note(metaNote(state))], { summary: '恢复失败' });
}

function metaNote(state: { cwd: string; projectName?: string }): string {
  const parts = [`📂 \`${state.cwd}\``];
  if (state.projectName) parts.unshift(`📁 ${state.projectName}`);
  return parts.join('   ');
}

function truncate(s: string, n: number): string {
  const t = s.trim();
  return t.length > n ? `${t.slice(0, n)}…` : t;
}

/** Coarse relative time from a unix-seconds (or millis) timestamp. */
export function relativeTime(unixSeconds: number): string {
  if (!unixSeconds) return '未知时间';
  const ms = unixSeconds < 1e12 ? unixSeconds * 1000 : unixSeconds;
  const diff = Date.now() - ms;
  const min = Math.floor(diff / 60_000);
  if (min < 1) return '刚刚';
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day} 天前`;
  return new Date(ms).toLocaleDateString('zh-CN');
}

/**
 * Timestamp for the resume picker buttons: friendly for recent sessions,
 * minute-precise (absolute) for older ones so same-title sessions stay
 * distinguishable (coarse "20 天前" would collide on duplicates).
 */
export function pickerTime(unixSeconds: number): string {
  if (!unixSeconds) return '未知时间';
  const ms = unixSeconds < 1e12 ? unixSeconds * 1000 : unixSeconds;
  const min = Math.floor((Date.now() - ms) / 60_000);
  if (min < 1) return '刚刚';
  if (min < 60) return `${min}分钟前`;
  const d = new Date(ms);
  const now = new Date();
  const p2 = (n: number): string => String(n).padStart(2, '0');
  const hm = `${p2(d.getHours())}:${p2(d.getMinutes())}`;
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  if (sameDay) return `今天 ${hm}`;
  const md = `${p2(d.getMonth() + 1)}-${p2(d.getDate())}`;
  return d.getFullYear() === now.getFullYear() ? `${md} ${hm}` : `${d.getFullYear()}-${md} ${hm}`;
}

// ── /help & 建群欢迎卡 ────────────────────────────────────────────────────────

/** Where the user is when they ask for help — drives which commands we list. */
export type HelpScope = 'main' | 'topic' | 'single';

/** First bullet describing how to talk to the bot, honoring the group's
 * effective 免@ state so the card never promises免@ when it's actually off
 * (e.g. a joined single-session group, which defaults off). */
function talkLine(noMention: boolean, tail: string): string {
  return noMention
    ? `· 直接发消息（免@）→ ${tail}`
    : `· **@我 + 内容** → ${tail}（本群默认需 @；\`/settings\` 可开启免@）`;
}

/** The `/help` card: commands available **right here** (this exact scope).
 * `noMention` is the group's effective 免@ state (`noMention ?? defaultNoMention`).
 * `isAdmin` gates the owner-only commands (`/settings`、`/resume`): non-admins
 * don't see them listed (they'd be denied anyway — see handle-message 的门控). */
export function buildHelpCard(scope: HelpScope, noMention = true, isAdmin = false): CardObject {
  const elements: CardElement[] = [];
  if (scope === 'single') {
    const lines = [
      talkLine(noMention, '交给我处理'),
      '· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）',
      '· `/model` → 切换模型 / 推理强度',
      '· `/context` → 看上下文占比',
      '· `/compact` → 压缩上下文（释放空间）',
    ];
    if (isAdmin) lines.push('· `/settings` → 群设置（免@ 开关）');
    lines.push('· `/help` → 这张速查卡');
    elements.push(md('💬 **单会话群** — 整群就是一个会话，上下文连续。'), hr(), md(lines.join('\n')));
  } else if (scope === 'topic') {
    elements.push(
      md('🧵 **话题内** — 每个话题是一个独立会话。'),
      hr(),
      md(
        `${talkLine(noMention, '继续当前会话')}\n` +
          '· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）\n' +
          '· `/model` → 切换模型 / 推理强度\n' +
          '· `/context` → 看上下文占比\n' +
          '· `/compact` → 压缩上下文（释放空间）\n' +
          '· `/help` → 这张速查卡',
      ),
      note('开新话题：回到主群区 @我 + 内容。'),
    );
  } else {
    const lines = [
      '· **@我 + 内容** → 开一个新话题并开始',
      '· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）',
    ];
    if (isAdmin) lines.push('· `/resume` → 恢复历史会话', '· `/settings` → 群设置（免@ 开关）');
    lines.push('· `/model` → 需要在话题里用', '· `/help` → 这张速查卡');
    elements.push(md('👥 **主群区** — @我开话题，每个话题是独立会话。'), hr(), md(lines.join('\n')));
  }
  return card(elements, { header: { title: '🤖 可用命令', template: 'blue' }, summary: '可用命令' });
}

/**
 * Welcome card posted when a project group is created or a group is bound — a
 * full overview of every command this group supports, keyed off its session
 * kind. `noMention` is the group's effective 免@ state (so a joined
 * single-session group, which defaults off, doesn't promise免@). Adds a
 * "查看完整手册" link button when a doc URL is configured.
 */
export function buildWelcomeCard(kind: 'multi' | 'single', docUrl?: string, noMention = true): CardObject {
  const elements: CardElement[] = [
    md('👋 **欢迎使用 Codex Bridge** — 本群已绑定一个项目目录，在群里就能驱动本机 Codex 干活。'),
    hr(),
  ];
  if (kind === 'single') {
    elements.push(
      md('💬 **单会话群**（整群一个会话，上下文连续）'),
      md(
        `${talkLine(noMention, '交给我处理')}\n` +
          '· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）\n' +
          '· `/model` → 切换模型 / 推理强度\n' +
          '· `/settings` → 群设置（免@ 开关）\n' +
          '· `/help` → 命令速查卡',
      ),
    );
  } else {
    elements.push(
      md('👥 **主群区**'),
      md(
        '· **@我 + 内容** → 开一个新话题并开始（每话题独立会话）\n' +
          '· `/goal <目标>` → 自主多轮跑到完成（卡上 ⏹ 终止 / 🎯 结束目标）\n' +
          '· `/resume` → 恢复历史会话\n' +
          '· `/settings` → 群设置（免@ 开关）',
      ),
      md('🧵 **话题内**'),
      md(
        '· 直接发消息（免@）→ 继续当前会话\n' +
          '· `/model` → 切换模型 / 推理强度\n' +
          '· `/context` · `/compact` → 看 / 压缩上下文',
      ),
      note('任意场景发 `/help` 看当前可用命令。'),
    );
  }
  if (docUrl) {
    elements.push(hr(), actions([linkButton('📖 查看完整使用手册', docUrl, 'primary')]));
  }
  return card(elements, { header: { title: '🤖 本群使用说明', template: 'turquoise' }, summary: '本群使用说明' });
}
