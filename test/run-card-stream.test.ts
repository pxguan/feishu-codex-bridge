import { describe, expect, it } from 'vitest';
import { RunCardStream } from '../src/card/run-card-stream';
import { card, mdStream } from '../src/card/cards';

/** A live frame whose only element is the streamed answer ({@link mdStream}). */
function frame(answer: string) {
  return card([mdStream(answer, 'answer')], { streaming: true });
}

/**
 * Minimal fake LarkChannel.rawClient for the streaming pump: records every
 * cardElement.content / card.settings call; `contentErrs` are thrown (once
 * each, in order) by the first content pushes.
 */
function fakeChannel(contentErrs: unknown[]) {
  const contents: Array<{ content: string; sequence: number }> = [];
  const settings: Array<{ settings: string; sequence: number }> = [];
  let errIdx = 0;
  return {
    contents,
    settings,
    rawClient: {
      cardkit: {
        v1: {
          card: {
            create: async () => ({ data: { card_id: 'c_1' } }),
            update: async () => ({}),
            settings: async (p: { data: { settings: string; sequence: number } }) => {
              settings.push(p.data);
              return {};
            },
          },
          cardElement: {
            content: async (p: { data: { content: string; sequence: number } }) => {
              if (errIdx < contentErrs.length) throw contentErrs[errIdx++];
              contents.push(p.data);
              return {};
            },
          },
        },
      },
      im: { v1: { message: { create: async () => ({ data: { message_id: 'om_1' } }) } } },
    },
  } as any;
}

/** Create the card, establish the pump's baseline, then grow the answer so the
 * pump routes the second frame to the element typewriter (streamElement). */
async function growAnswer(ch: any): Promise<RunCardStream> {
  const s = new RunCardStream();
  await s.create(ch, 'oc_1', frame('hello'), {});
  s.streamCoalesced(ch, frame('hello'), 'answer'); // baseline (deduped — no push)
  await s.drain();
  s.streamCoalesced(ch, frame('hello world'), 'answer'); // answer grew → element route
  await s.drain();
  return s;
}

describe('RunCardStream.streamElement — streaming_mode recovery', () => {
  it('on 300309 (streaming_mode auto-off) re-enables streaming via settings and resends the frame', async () => {
    const ch = fakeChannel([{ response: { data: { code: 300309, msg: 'streaming mode disabled' } } }]);
    await growAnswer(ch);

    expect(ch.settings).toHaveLength(1);
    expect(JSON.parse(ch.settings[0].settings)).toEqual({ config: { streaming_mode: true } });
    expect(ch.contents).toHaveLength(1);
    expect(ch.contents[0].content).toBe('hello world'); // the SAME frame, re-pushed
    // sequence stays strictly increasing across the failed push → settings → resend
    expect(ch.settings[0].sequence).toBeLessThan(ch.contents[0].sequence);
  });

  it('on 300317 (sequence out-of-order) retries the push once, without touching settings', async () => {
    const ch = fakeChannel([{ code: 300317 }]); // top-level code shape
    await growAnswer(ch);

    expect(ch.settings).toHaveLength(0);
    expect(ch.contents).toHaveLength(1);
    expect(ch.contents[0].content).toBe('hello world');
  });

  it('does not retry a genuine error — logged and dropped, no settings call', async () => {
    const ch = fakeChannel([{ response: { data: { code: 99991663, msg: 'rate limited' } } }]);
    await growAnswer(ch); // must not throw (pump swallows + logs)

    expect(ch.settings).toHaveLength(0);
    expect(ch.contents).toHaveLength(0);
  });
});
