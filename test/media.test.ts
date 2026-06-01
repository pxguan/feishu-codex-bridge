import { describe, expect, it } from 'vitest';
import type { NormalizedMessage } from '@larksuiteoapi/node-sdk';
import { imageKeysFromContent, messageHasImages } from '../src/bot/media';

function msg(overrides: Partial<NormalizedMessage> = {}): NormalizedMessage {
  return {
    messageId: 'om_x',
    chatId: 'oc_x',
    chatType: 'group',
    senderId: 'ou_x',
    content: '',
    rawContentType: 'text',
    resources: [],
    mentions: [],
    mentionAll: false,
    mentionedBot: true,
    createTime: 0,
    ...overrides,
  } as NormalizedMessage;
}

describe('messageHasImages', () => {
  it('is true when an image resource is present', () => {
    expect(messageHasImages(msg({ resources: [{ type: 'image', fileKey: 'img_1' }] }))).toBe(true);
  });
  it('is true for merge_forward even with no top-level resources', () => {
    expect(messageHasImages(msg({ rawContentType: 'merge_forward', resources: [] }))).toBe(true);
  });
  it('is false for plain text / non-image resources', () => {
    expect(messageHasImages(msg())).toBe(false);
    expect(messageHasImages(msg({ resources: [{ type: 'file', fileKey: 'file_1' }] }))).toBe(false);
  });
});

describe('imageKeysFromContent', () => {
  it('extracts the top-level image_key from a plain image message', () => {
    expect(imageKeysFromContent('image', JSON.stringify({ image_key: 'img_v3_abc' }))).toEqual(['img_v3_abc']);
  });

  it('walks a post (rich text) body for embedded img tags', () => {
    const post = JSON.stringify({
      title: 't',
      content: [
        [
          { tag: 'text', text: 'see ' },
          { tag: 'img', image_key: 'img_p1' },
        ],
        [{ tag: 'img', image_key: 'img_p2' }],
      ],
    });
    expect(imageKeysFromContent('post', post)).toEqual(['img_p1', 'img_p2']);
  });

  it('finds img tags inside a locale-wrapped post', () => {
    const post = JSON.stringify({
      zh_cn: { title: '', content: [[{ tag: 'img', image_key: 'img_loc' }]] },
    });
    expect(imageKeysFromContent('post', post)).toEqual(['img_loc']);
  });

  it('returns [] for non-image content and bad JSON', () => {
    expect(imageKeysFromContent('text', JSON.stringify({ text: 'hi' }))).toEqual([]);
    expect(imageKeysFromContent('image', JSON.stringify({})).length).toBe(0);
    expect(imageKeysFromContent('image', 'not json')).toEqual([]);
    expect(imageKeysFromContent('image', undefined)).toEqual([]);
  });
});
