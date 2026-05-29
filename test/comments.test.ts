import { describe, expect, it, vi } from 'vitest';
import type { CommentEvent, LarkChannel } from '@larksuiteoapi/node-sdk';
import {
  buildCommentPrompt,
  fetchCommentContext,
  postCommentReply,
  resolveComment,
  stripMarkdown,
  SUPPORTED_FILE_TYPES,
  type ResolvedTarget,
} from '../src/bot/comments';
import { finalMessageText, initialState, reduce } from '../src/card/run-state';
import type { AgentEvent } from '../src/agent/types';

/** An API error in the shape thrown by the lark rawClient (axios-style). */
function apiError(code: number): Error {
  return Object.assign(new Error(`feishu ${code}`), { response: { data: { code } } });
}

function evt(overrides: Partial<CommentEvent> = {}): CommentEvent {
  return {
    fileToken: 'doccnAAA',
    fileType: 'docx',
    commentId: 'cmt1',
    operator: { openId: 'ou_user' },
    mentionedBot: true,
    timestamp: 0,
    ...overrides,
  } as CommentEvent;
}

const docxTarget: ResolvedTarget = { fileToken: 'doccnAAA', fileType: 'docx' };

describe('stripMarkdown', () => {
  it('removes bold/italic/heading/inline-code/bullet/quote markers', () => {
    expect(stripMarkdown('**bold**')).toBe('bold');
    expect(stripMarkdown('__bold__')).toBe('bold');
    expect(stripMarkdown('# Heading')).toBe('Heading');
    expect(stripMarkdown('`code`')).toBe('code');
    expect(stripMarkdown('- item')).toBe('item');
    expect(stripMarkdown('> quote')).toBe('quote');
  });

  it('strips fenced code-block markers but keeps the body', () => {
    expect(stripMarkdown('```js\nfoo()\n```')).toBe('foo()\n');
  });

  it('leaves plain text untouched', () => {
    expect(stripMarkdown('multi-agent 不是最优解。')).toBe('multi-agent 不是最优解。');
  });

  it('still strips real *italic* / _italic_ that hug their content', () => {
    expect(stripMarkdown('this *word* here')).toBe('this word here');
    expect(stripMarkdown('this _word_ here')).toBe('this word here');
  });

  it('leaves a lone asterisk/underscore used as an operator alone', () => {
    expect(stripMarkdown('a * b and c * d')).toBe('a * b and c * d');
    expect(stripMarkdown('time _ space')).toBe('time _ space');
    expect(stripMarkdown('snake_case_var')).toBe('snake_case_var');
  });
});

describe('finalMessageText (run-state)', () => {
  const fold = (events: AgentEvent[]) => {
    let s = initialState;
    for (const e of events) s = reduce(s, e);
    return s;
  };

  it('returns the LAST non-empty agent message, not all of them concatenated', () => {
    // codex emits preamble/progress messages then the answer; we want only the answer.
    const s = fold([
      { type: 'thinking', itemId: 'r', text: 'pondering' },
      { type: 'text', itemId: 'a', text: '我先查一下版本' }, // preamble message
      { type: 'tool_use', itemId: 't', title: 'ls' },
      { type: 'text', itemId: 'b', text: '最新 Current 是 26.2.0' }, // final answer
      { type: 'done', turnId: 'x' },
    ]);
    expect(finalMessageText(s)).toBe('最新 Current 是 26.2.0');
  });

  it('returns empty string when the turn produced no text (reasoning/tools only)', () => {
    const s = fold([
      { type: 'thinking', itemId: 'r', text: 'pondering' },
      { type: 'tool_use', itemId: 't', title: 'ls' },
    ]);
    expect(finalMessageText(s)).toBe('');
  });
});

describe('buildCommentPrompt', () => {
  it('includes the question/token/scope/url, and forbids self-posting + demands final-answer-only plain text', () => {
    const p = buildCommentPrompt(docxTarget, { question: '这样对吗?', isWhole: true }, 'feishu');
    expect(p).toContain('这样对吗?');
    expect(p).toContain('doccnAAA');
    expect(p).toContain('全文评论');
    expect(p).toContain('https://feishu.cn/docx/doccnAAA');
    expect(p).toContain('纯文本'); // the no-markdown instruction
    expect(p).toContain('系统会自动'); // the bridge posts the reply, not the agent
    expect(p).toContain('最终答案'); // only the final answer, no process narration
  });

  it('renders the selected quote for inline comments and uses the lark host', () => {
    const p = buildCommentPrompt(docxTarget, { question: 'q', isWhole: false, quote: 'line one\nline two' }, 'lark');
    expect(p).toContain('行内评论');
    expect(p).toContain('> line one\n> line two');
    expect(p).toContain('https://larksuite.com/docx/doccnAAA');
  });
});

describe('SUPPORTED_FILE_TYPES', () => {
  it('covers doc/docx/sheet/file and excludes slides/bitable/mindnote', () => {
    for (const t of ['doc', 'docx', 'sheet', 'file']) expect(SUPPORTED_FILE_TYPES.has(t)).toBe(true);
    for (const t of ['slides', 'bitable', 'mindnote', 'wiki']) expect(SUPPORTED_FILE_TYPES.has(t)).toBe(false);
  });
});

describe('resolveComment', () => {
  // get returns a reply ONLY for the given file_token; list is always empty.
  function channelFor(replyTokens: Record<string, string>, getNode = vi.fn()) {
    const get = vi.fn((payload?: { path?: { file_token?: string } }) => {
      const token = payload?.path?.file_token ?? '';
      const text = replyTokens[token];
      if (text === undefined) return Promise.resolve({ data: { reply_list: { replies: [] } } });
      return Promise.resolve({
        data: { reply_list: { replies: [{ reply_id: 'r1', content: { elements: [{ type: 'text_run', text_run: { text } }] } }] } },
      });
    });
    const list = vi.fn(() => Promise.resolve({ data: { items: [] } }));
    const channel = {
      rawClient: {
        drive: { v1: { fileComment: { get, list } } },
        wiki: { v2: { space: { getNode } } },
      },
    } as unknown as LarkChannel;
    return { channel, getNode };
  }

  it('uses the event token directly and never calls wiki getNode for a normal doc', async () => {
    const { channel, getNode } = channelFor({ doccnAAA: '直接命中的答案' });
    const r = await resolveComment(channel, evt({ fileToken: 'doccnAAA', fileType: 'docx' }));
    expect(r?.target).toEqual({ fileToken: 'doccnAAA', fileType: 'docx' });
    expect(r?.ctx.question).toBe('直接命中的答案');
    expect(getNode).not.toHaveBeenCalled(); // no wiki scope needed for regular docs
  });

  it('falls back to wiki node resolution when the direct token has no comment', async () => {
    const getNode = vi.fn().mockResolvedValue({ data: { node: { obj_token: 'realdocx', obj_type: 'docx' } } });
    // direct token 'wikiNode' yields nothing; the resolved 'realdocx' has the reply.
    const { channel } = channelFor({ realdocx: 'wiki 文档里的答案' }, getNode);
    const r = await resolveComment(channel, evt({ fileToken: 'wikiNode', fileType: 'docx' }));
    expect(getNode).toHaveBeenCalledWith({ params: { token: 'wikiNode' } });
    expect(r?.target).toEqual({ fileToken: 'realdocx', fileType: 'docx' });
    expect(r?.ctx.question).toBe('wiki 文档里的答案');
  });

  it('returns null for unsupported file types without calling any API', async () => {
    const { channel, getNode } = channelFor({});
    const get = channel.rawClient.drive.v1.fileComment.get as ReturnType<typeof vi.fn>;
    expect(await resolveComment(channel, evt({ fileType: 'bitable' }))).toBeNull();
    expect(get).not.toHaveBeenCalled();
    expect(getNode).not.toHaveBeenCalled();
  });

  it('returns null when neither the direct token nor a wiki node yields a comment', async () => {
    const getNode = vi.fn().mockRejectedValue(apiError(404)); // not a wiki node
    const { channel } = channelFor({}, getNode);
    expect(await resolveComment(channel, evt({ fileToken: 'doccnAAA', fileType: 'docx' }))).toBeNull();
  });
});

describe('fetchCommentContext', () => {
  function channelWith(getImpl: () => unknown, listImpl?: () => unknown): LarkChannel {
    return {
      rawClient: {
        drive: {
          v1: {
            fileComment: {
              get: vi.fn(getImpl as () => Promise<unknown>),
              list: vi.fn((listImpl ?? (() => Promise.resolve({ data: { items: [] } }))) as () => Promise<unknown>),
            },
          },
        },
      },
    } as unknown as LarkChannel;
  }

  const replies = [
    { reply_id: 'r1', content: { elements: [{ type: 'text_run', text_run: { text: 'first' } }] } },
    { reply_id: 'r2', content: { elements: [{ type: 'text_run', text_run: { text: '@bot 第二条' } }] } },
  ];

  it('picks the reply matching evt.replyId', async () => {
    const channel = channelWith(() =>
      Promise.resolve({ data: { reply_list: { replies }, quote: 'sel', is_whole: false } }),
    );
    const ctx = await fetchCommentContext(channel, docxTarget, evt({ replyId: 'r2' }));
    expect(ctx.question).toBe('@bot 第二条');
    expect(ctx.targetReplyId).toBe('r2');
    expect(ctx.quote).toBe('sel');
    expect(ctx.isWhole).toBe(false);
  });

  it('falls back to the last reply when replyId is absent', async () => {
    const channel = channelWith(() => Promise.resolve({ data: { reply_list: { replies } } }));
    const ctx = await fetchCommentContext(channel, docxTarget, evt());
    expect(ctx.targetReplyId).toBe('r2');
    expect(ctx.question).toBe('@bot 第二条');
  });

  it('falls back to .list when .get throws (e.g. 1069307)', async () => {
    const channel = channelWith(
      () => Promise.reject(apiError(1069307)),
      () =>
        Promise.resolve({
          data: { items: [{ comment_id: 'cmt1', reply_list: { replies }, is_whole: true }], has_more: false },
        }),
    );
    const ctx = await fetchCommentContext(channel, docxTarget, evt());
    expect(ctx.question).toBe('@bot 第二条');
    expect(ctx.isWhole).toBe(true);
  });
});

describe('postCommentReply', () => {
  function channel(replyCreate: () => unknown, commentCreate = vi.fn(() => Promise.resolve({}))) {
    const ch = {
      rawClient: {
        drive: {
          v1: {
            fileCommentReply: { create: vi.fn(replyCreate as () => Promise<unknown>) },
            fileComment: { create: commentCreate },
          },
        },
      },
    } as unknown as LarkChannel;
    return ch;
  }

  it('replies in-thread via fileCommentReply.create', async () => {
    const ch = channel(() => Promise.resolve({}));
    await postCommentReply(ch, docxTarget, evt(), 'answer');
    const reply = ch.rawClient.drive.v1.fileCommentReply.create as ReturnType<typeof vi.fn>;
    expect(reply).toHaveBeenCalledOnce();
    expect(reply.mock.calls[0]?.[0]).toMatchObject({
      params: { file_type: 'docx' },
      path: { file_token: 'doccnAAA', comment_id: 'cmt1' },
      data: { content: { elements: [{ type: 'text_run', text_run: { text: 'answer' } }] } },
    });
  });

  it('falls back to a top-level comment on 1069302 (whole-doc, no thread)', async () => {
    const commentCreate = vi.fn((_payload?: unknown) => Promise.resolve({}));
    const ch = channel(() => Promise.reject(apiError(1069302)), commentCreate);
    await postCommentReply(ch, { fileToken: 'doccnAAA', fileType: 'doc' }, evt({ fileType: 'doc' }), 'answer');
    expect(commentCreate).toHaveBeenCalledOnce();
    expect(commentCreate.mock.calls[0]?.[0]).toMatchObject({
      params: { file_type: 'doc' },
      path: { file_token: 'doccnAAA' },
    });
  });

  it('rethrows non-1069302 errors instead of masking them', async () => {
    const ch = channel(() => Promise.reject(apiError(99999)));
    await expect(postCommentReply(ch, docxTarget, evt(), 'x')).rejects.toThrow();
    expect(ch.rawClient.drive.v1.fileComment.create).not.toHaveBeenCalled();
  });
});
