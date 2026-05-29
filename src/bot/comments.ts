import type { CommentEvent, LarkChannel } from '@larksuiteoapi/node-sdk';
import type { TenantBrand } from '../config/schema';
import { log } from '../core/logger';

/**
 * Cloud-doc comment plumbing: when someone @-mentions the bot in a Feishu
 * document comment (the `drive.notice.comment_add_v1` event, surfaced by the
 * SDK as a normalized `comment` event), these helpers read the comment text,
 * resolve wiki nodes, post the agent's answer back into the same thread, and
 * drive the best-effort "Typing" reaction. The agent run itself lives in the
 * orchestrator (handle-message.ts) so it can share the codex backend, session
 * store, and concurrency semaphore.
 *
 * File types supported by drive.v1.fileComment.* — other types (slides,
 * bitable, mindnote) use different APIs and are out of scope for now.
 */
export const SUPPORTED_FILE_TYPES = new Set(['doc', 'docx', 'sheet', 'file']);

/** Hard cap on the reply length we post into a comment (Feishu rejects very
 * long comment bodies); answers longer than this are truncated with an ellipsis. */
export const REPLY_MAX_CHARS = 2000;

export type CommentFileType = 'doc' | 'docx' | 'sheet' | 'file';

export interface ResolvedTarget {
  fileToken: string;
  fileType: CommentFileType;
}

export interface CommentContext {
  question: string;
  quote?: string;
  isWhole: boolean;
  /** reply_id of the reply carrying the @bot mention — the anchor we react on.
   * Undefined when we couldn't pinpoint a reply. */
  targetReplyId?: string;
}

interface ReplyContentElement {
  type: 'text_run' | 'docs_link' | 'person';
  text_run?: { text: string };
  docs_link?: { url: string };
  person?: { user_id: string };
}
interface CommentReply {
  reply_id?: string;
  content?: { elements?: ReplyContentElement[] };
}
interface CommentGetResponse {
  data?: { reply_list?: { replies?: CommentReply[] }; quote?: string; is_whole?: boolean };
}
interface CommentListItem {
  comment_id?: string;
  reply_list?: { replies?: CommentReply[] };
  is_whole?: boolean;
  quote?: string;
}
interface CommentListResponse {
  data?: { items?: CommentListItem[]; has_more?: boolean; page_token?: string };
}

/** Pull the Feishu API error code off a thrown rawClient error (axios shape). */
export function apiErrCode(err: unknown): number | undefined {
  return (err as { response?: { data?: { code?: number } } })?.response?.data?.code;
}

export interface ResolvedComment {
  target: ResolvedTarget;
  ctx: CommentContext;
}

/**
 * Resolve a comment event to its (target, context), trying the event's OWN
 * token first — the common case (a normal doc/docx/sheet/file), which needs no
 * wiki scope and makes no extra API call. Only if that yields no question do we
 * treat the token as a knowledge-base (wiki) node, resolve it to the underlying
 * doc, and retry. So `wiki:*` scope is needed solely for wiki-hosted docs, and
 * a regular doc never triggers the (noisy, scope-gated) wiki getNode call.
 * Returns null when the file type is unsupported or no question was found.
 */
export async function resolveComment(
  channel: LarkChannel,
  evt: CommentEvent,
): Promise<ResolvedComment | null> {
  if (!SUPPORTED_FILE_TYPES.has(evt.fileType)) return null;

  const direct: ResolvedTarget = {
    fileToken: evt.fileToken,
    fileType: evt.fileType as CommentFileType,
  };
  const directCtx = await fetchCommentContext(channel, direct, evt).catch((err) => {
    if (apiErrCode(err) === 1069307) log.warn('comment', 'no-access', { token: direct.fileToken });
    else log.fail('comment', err, { step: 'fetch-direct' });
    return null;
  });
  if (directCtx?.question) return { target: direct, ctx: directCtx };

  // No question via the direct token → it may be a wiki node. Resolve + retry.
  const wiki = await resolveWikiNode(channel, evt.fileToken);
  if (wiki) {
    const wikiCtx = await fetchCommentContext(channel, wiki, evt).catch((err) => {
      log.fail('comment', err, { step: 'fetch-wiki' });
      return null;
    });
    if (wikiCtx?.question) return { target: wiki, ctx: wikiCtx };
  }
  return null;
}

/**
 * If `fileToken` is a knowledge-base (wiki) node, return its underlying
 * (obj_token, obj_type); otherwise null. Swallows errors (not a wiki node, or
 * `wiki:*` scope ungranted) — callers only reach here after the direct token
 * already failed, so a miss just means "give up".
 */
async function resolveWikiNode(
  channel: LarkChannel,
  fileToken: string,
): Promise<ResolvedTarget | null> {
  try {
    const r = await channel.rawClient.wiki.v2.space.getNode({ params: { token: fileToken } });
    const node = (r as { data?: { node?: { obj_token?: string; obj_type?: string } } })?.data?.node;
    if (node?.obj_token && node.obj_type && SUPPORTED_FILE_TYPES.has(node.obj_type)) {
      log.info('comment', 'wiki-resolved', { objToken: node.obj_token, objType: node.obj_type });
      return { fileToken: node.obj_token, fileType: node.obj_type as CommentFileType };
    }
  } catch {
    // not a wiki node, or wiki scope not granted — fine, caller gives up
  }
  return null;
}

/**
 * Fetch the comment thread and extract the @bot question. Tries `.get` first;
 * if it fails for ANY reason — commonly 1069307 on block-anchored comments, but
 * also transient/network errors — we fall back to paging `.list`. A genuinely
 * inaccessible comment then yields empty replies / an empty question and is
 * skipped upstream, so the broad catch only ever trades one API shape for the
 * other, never masks a hard failure.
 */
export async function fetchCommentContext(
  channel: LarkChannel,
  target: ResolvedTarget,
  evt: CommentEvent,
): Promise<CommentContext> {
  let replies: CommentReply[] = [];
  let quote: string | undefined;
  let isWhole = false;
  try {
    const r = (await channel.rawClient.drive.v1.fileComment.get({
      params: { file_type: target.fileType },
      path: { file_token: target.fileToken, comment_id: evt.commentId },
    })) as CommentGetResponse;
    replies = r?.data?.reply_list?.replies ?? [];
    quote = r?.data?.quote || undefined;
    isWhole = Boolean(r?.data?.is_whole);
  } catch (err) {
    log.warn('comment', 'get-failed-fallback-list', { code: apiErrCode(err) });
    const found = await findCommentViaList(channel, target, evt.commentId);
    replies = found?.reply_list?.replies ?? [];
    quote = found?.quote || undefined;
    isWhole = Boolean(found?.is_whole);
  }

  // Prefer the reply the event points at (the one with the @mention); else the
  // last reply in the thread.
  const targetReply =
    (evt.replyId ? replies.find((rr) => rr.reply_id === evt.replyId) : undefined) ??
    replies[replies.length - 1];
  const question = elementsToText(targetReply?.content?.elements ?? []);
  return { question, quote, isWhole, targetReplyId: targetReply?.reply_id };
}

function elementsToText(elements: ReplyContentElement[]): string {
  return elements
    .map((el) => {
      if (el.type === 'text_run') return el.text_run?.text ?? '';
      if (el.type === 'docs_link') return el.docs_link?.url ?? '';
      return '';
    })
    .join('')
    .trim();
}

async function findCommentViaList(
  channel: LarkChannel,
  target: ResolvedTarget,
  commentId: string,
): Promise<CommentListItem | null> {
  let pageToken: string | undefined;
  for (let page = 0; page < 10; page++) {
    const r = (await channel.rawClient.drive.v1.fileComment.list({
      params: {
        file_type: target.fileType,
        page_size: 100,
        ...(pageToken ? { page_token: pageToken } : {}),
      },
      path: { file_token: target.fileToken },
    })) as CommentListResponse;
    const items = r?.data?.items ?? [];
    const hit = items.find((it) => it.comment_id === commentId);
    if (hit) return hit;
    if (!r?.data?.has_more || !r.data.page_token) {
      // Walked the whole comment list (or the doc has none) without a match.
      return null;
    }
    pageToken = r.data.page_token;
  }
  // Hit the 10-page (≈1000-comment) safety cap without finding the target —
  // distinct from a genuinely empty mention so a missing reply is diagnosable.
  log.warn('comment', 'comment-not-found-after-paging', { commentId, pagesScanned: 10 });
  return null;
}

const DOC_HOSTS: Record<TenantBrand, string> = {
  feishu: 'feishu.cn',
  lark: 'larksuite.com',
};

/** Build the codex prompt for a doc-comment mention. */
export function buildCommentPrompt(
  target: ResolvedTarget,
  ctx: CommentContext,
  tenant: TenantBrand,
): string {
  const docUrl = `https://${DOC_HOSTS[tenant]}/${target.fileType}/${target.fileToken}`;
  const parts: string[] = [];
  parts.push('我在飞书云文档的评论里被 @了，需要你回答评论中的问题。文档信息：');
  parts.push(`- 链接：${docUrl}`);
  parts.push(`- file_token：${target.fileToken}`);
  parts.push(`- 类型：${target.fileType}`);
  parts.push(`- 评论范围：${ctx.isWhole ? '全文评论（针对整篇文档）' : '行内评论（针对选中的文字）'}`);
  if (ctx.quote) {
    parts.push('');
    parts.push(`用户选中的原文：\n> ${ctx.quote.replace(/\n/g, '\n> ')}`);
  }
  parts.push('');
  parts.push(`用户的问题：${ctx.question}`);
  parts.push('');
  parts.push(
    '如果回答需要文档正文内容，可用 lark-cli 只读地获取（仅用于读取，不要用它写任何东西）：\n' +
      `  lark-cli docs +fetch --doc ${target.fileToken} --api-version v2`,
  );
  parts.push('');
  parts.push('【非常重要，务必遵守】');
  parts.push(
    '1. 不要自己去发表 / 回复 / 修改任何飞书评论或文档（也不要用 lark-cli 或任何工具去发评论）——' +
      '系统会自动把你下面给出的最终回复发到这条评论里，你只管把答案写出来。',
  );
  parts.push('2. 只输出要发给用户的「最终答案」本身，不要复述分析过程、步骤、或「我现在去…」这类说明。');
  parts.push(
    '3. 用纯文本，不要用 markdown 标记（不要 ** __ # - * > \` 之类），不要代码块；' +
      '评论框不渲染 markdown，会原样显示这些符号。回答简洁直接。',
  );
  return parts.join('\n');
}

/**
 * Strip the most common markdown markers so a plain-text comment doesn't show
 * literal `**` / `#` / `> ` etc. Conservative — only touches bold, italic,
 * headings, blockquote, list bullets, and inline/fenced code.
 */
export function stripMarkdown(s: string): string {
  return (
    s
      // Fenced code FIRST: drop the ``` fences (with optional lang) but keep the
      // body — must run before the inline-code rule, else its backticks get
      // eaten pairwise and the fence is left half-stripped.
      .replace(/```[a-zA-Z]*\n?/g, '')
      .replace(/```/g, '')
      // headings: "# foo" -> "foo"
      .replace(/^#{1,6}\s+/gm, '')
      // bold/italic: **foo** / __foo__ / *foo* / _foo_. The single-delimiter
      // rules require the markers to hug non-space content (`(?!\s)`/`(?<!\s)`),
      // so a lone `*` or `_` used as an operator (`a * b`) is left alone.
      .replace(/\*\*([^*]+)\*\*/g, '$1')
      .replace(/__([^_]+)__/g, '$1')
      .replace(/(?<![*\w])\*(?!\s)([^*\n]+?)(?<!\s)\*(?!\w)/g, '$1')
      .replace(/(?<![_\w])_(?!\s)([^_\n]+?)(?<!\s)_(?!\w)/g, '$1')
      // inline code: `foo` -> foo
      .replace(/`([^`]+)`/g, '$1')
      // unordered list bullets + blockquote
      .replace(/^[-*]\s+/gm, '')
      .replace(/^>\s?/gm, '')
  );
}

/**
 * Post the answer as a reply in the same comment thread. Whole-document
 * comments have no thread (1069302) — fall back to a fresh top-level comment.
 */
export async function postCommentReply(
  channel: LarkChannel,
  target: ResolvedTarget,
  evt: CommentEvent,
  text: string,
): Promise<void> {
  try {
    await channel.rawClient.drive.v1.fileCommentReply.create({
      params: { file_type: target.fileType },
      path: { file_token: target.fileToken, comment_id: evt.commentId },
      data: { content: { elements: [{ type: 'text_run', text_run: { text } }] } },
    });
    log.info('comment', 'replied', { mode: 'in-thread' });
    return;
  } catch (err) {
    // 1069302: whole-document comments don't accept replies — they have no
    // thread, only a flat list. Anything else is a real failure.
    if (apiErrCode(err) !== 1069302) throw err;
    log.warn('comment', 'reply-rejected-fallback-create', { code: 1069302 });
  }

  // Whole-doc fallback: fileComment.create only accepts doc/docx.
  if (target.fileType !== 'doc' && target.fileType !== 'docx') {
    log.warn('comment', 'whole-doc-reply-unsupported', { fileType: target.fileType });
    return;
  }
  await channel.rawClient.drive.v1.fileComment.create({
    params: { file_type: target.fileType },
    path: { file_token: target.fileToken },
    data: {
      reply_list: { replies: [{ content: { elements: [{ type: 'text_run', text_run: { text } }] } }] },
    },
  });
  log.info('comment', 'replied', { mode: 'new-top-level' });
}

/**
 * Add a "Typing" reaction to a cloud-doc comment reply. Doc comments have their
 * own reaction endpoint (drive/v2/comment_reaction) — separate from IM message
 * reactions, and unlike IM it returns no reaction id: add/delete are the same
 * POST with an `action` field. Returns true if the call succeeded so callers
 * know whether to bother sending the matching remove. Best-effort.
 */
export async function addCommentReaction(
  channel: LarkChannel,
  target: ResolvedTarget,
  replyId: string,
): Promise<boolean> {
  return commentReaction(channel, target, replyId, 'add');
}

export async function removeCommentReaction(
  channel: LarkChannel,
  target: ResolvedTarget,
  replyId: string,
): Promise<void> {
  await commentReaction(channel, target, replyId, 'delete');
}

async function commentReaction(
  channel: LarkChannel,
  target: ResolvedTarget,
  replyId: string,
  action: 'add' | 'delete',
): Promise<boolean> {
  try {
    await channel.rawClient.drive.v2.commentReaction.updateReaction({
      params: { file_type: target.fileType },
      path: { file_token: target.fileToken },
      data: { action, reply_id: replyId, reaction_type: 'Typing' },
    });
    log.info('comment', `reaction-${action}`, { fileToken: target.fileToken, replyId });
    return true;
  } catch (err) {
    log.warn('comment', `reaction-${action}-failed`, {
      replyId,
      err: err instanceof Error ? err.message : String(err),
    });
    return false;
  }
}
