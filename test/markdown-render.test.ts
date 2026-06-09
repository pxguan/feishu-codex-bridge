import { describe, expect, it } from 'vitest';
import { buildCleanCard, extractCardFences, renderRichText } from '../src/card/markdown-render';

/** Collect every element with a given tag from a card body / element list. */
function tags(node: unknown, tag: string, acc: any[] = []): any[] {
  if (Array.isArray(node)) node.forEach((n) => tags(n, tag, acc));
  else if (node && typeof node === 'object') {
    const o = node as Record<string, unknown>;
    if (o.tag === tag) acc.push(o);
    for (const k of Object.keys(o)) tags(o[k], tag, acc);
  }
  return acc;
}

describe('extractCardFences', () => {
  it('pulls the ```feishu-card fence and strips it from the text', () => {
    const text = '前言\n\n```feishu-card\n# 标题\n正文\n```\n\n后记';
    const { fences, stripped } = extractCardFences(text);
    expect(fences).toEqual(['# 标题\n正文']);
    expect(stripped).not.toContain('feishu-card');
    expect(stripped).toContain('前言');
    expect(stripped).toContain('后记');
  });

  it('handles multiple fences and leaves plain text untouched', () => {
    const plain = '没有卡片，只有 `code` 和 **bold**。';
    expect(extractCardFences(plain)).toEqual({ fences: [], stripped: plain });

    const two = '```feishu-card\nA\n```\nmid\n```feishu-card\nB\n```';
    const { fences } = extractCardFences(two);
    expect(fences).toEqual(['A', 'B']);
  });
});

describe('renderRichText', () => {
  it('plain text → a single markdown element (fast path)', () => {
    const els = renderRichText('就是一段**普通**文字');
    expect(els).toHaveLength(1);
    expect(els[0]).toMatchObject({ tag: 'markdown', content: '就是一段**普通**文字' });
  });

  it('interleaves text and uploaded images, splitting at the ref', () => {
    const map = new Map([['shot.png', 'img_key_123']]);
    const els = renderRichText('看这张图：\n\n![管理台](shot.png)\n\n然后呢', map);
    expect(els.map((e: any) => e.tag)).toEqual(['markdown', 'img', 'markdown']);
    const img = tags(els, 'img')[0];
    expect(img.img_key).toBe('img_key_123');
    expect(img.alt.content).toBe('管理台');
    expect((els[0] as any).content).toContain('看这张图');
    expect((els[2] as any).content).toContain('然后呢');
  });

  it('keeps the literal markdown when the image is unresolved (no upload)', () => {
    const els = renderRichText('图：![x](missing.png) 完', new Map());
    expect(tags(els, 'img')).toHaveLength(0);
    expect((els[0] as any).content).toContain('![x](missing.png)');
  });

  it('resolves http(s) URLs the same way (keyed by the verbatim src)', () => {
    const url = 'https://example.com/a.png';
    const els = renderRichText(`![](${url})`, new Map([[url, 'img_remote']]));
    expect(tags(els, 'img')[0].img_key).toBe('img_remote');
  });

  it('drops a ```feishu-card fence from the answer (hoisted to a clean card)', () => {
    const els = renderRichText('答复：\n\n```feishu-card\n# T\nbody\n```');
    // only the lead-in text remains; the fence is gone
    expect(tags(els, 'markdown').every((m: any) => !m.content.includes('feishu-card'))).toBe(true);
    expect((els[0] as any).content).toContain('答复');
  });

  it('preserves a Feishu mention tag verbatim (so codex can @ a user)', () => {
    const els = renderRichText('已处理完，请验收 <at id=ou_abcd1234></at>');
    expect(els).toHaveLength(1);
    expect((els[0] as any).content).toBe('已处理完，请验收 <at id=ou_abcd1234></at>');
  });

  it('preserves a mention even when interleaved with an uploaded image', () => {
    const map = new Map([['shot.png', 'img_key_1']]);
    const els = renderRichText('看图 <at id=ou_x></at>\n\n![s](shot.png)', map);
    expect((els[0] as any).content).toContain('<at id=ou_x></at>');
    expect(tags(els, 'img')).toHaveLength(1);
  });

  it('preserves a bare mention with no surrounding text', () => {
    const els = renderRichText('<at id=ou_x></at>');
    expect(els).toHaveLength(1);
    expect((els[0] as any).content).toBe('<at id=ou_x></at>');
  });
});

describe('buildCleanCard', () => {
  it('hoists the leading heading into a blue header and maps blocks', () => {
    const md = [
      '# 更新说明',
      '',
      '本次更新重点提升了体验。',
      '',
      '![管理台](admin.png)',
      '',
      '---',
      '',
      '**新增功能**',
      '- A',
      '- B',
      '',
      '> 一句话总结：更顺手了。',
    ].join('\n');
    const card: any = buildCleanCard(md, new Map([['admin.png', 'img_admin']]));

    expect(card.schema).toBe('2.0');
    expect(card.header.title.content).toBe('更新说明');
    expect(card.header.template).toBe('blue');
    expect(card.config.summary.content).toBe('更新说明');

    const els = card.body.elements as any[];
    expect(tags(els, 'img')[0].img_key).toBe('img_admin');
    expect(tags(els, 'hr')).toHaveLength(1);
    // the quote block becomes a grey note (div with grey lark_md), not markdown
    const note = els.find((e) => e.tag === 'div' && e.text?.text_color === 'grey');
    expect(note.text.content).toContain('一句话总结');
  });

  it('a title-only fence still yields a valid non-empty body', () => {
    const card: any = buildCleanCard('# 只有标题');
    expect(card.header.title.content).toBe('只有标题');
    expect((card.body.elements as any[]).length).toBeGreaterThan(0);
  });

  it('no leading heading → no header bar', () => {
    const card: any = buildCleanCard('直接正文，没有标题。');
    expect(card.header).toBeUndefined();
    expect((card.body.elements as any[])[0].content).toContain('直接正文');
  });
});
