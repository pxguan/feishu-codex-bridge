import type { LarkChannel } from '@larksuiteoapi/node-sdk';
import { log } from '../core/logger';
import type { CardObject } from './cards';

/**
 * Min gap between throttled whole-card stream updates. Finer = smoother chunked
 * growth (each push shows the full text so far). Feishu caps a single card at
 * ~10 ops/sec; 150ms ≈ 6.6/sec keeps headroom (streaming_mode's QPS exemption no
 * longer applies now that it's off — see {@link ../card/cards}).
 */
const STREAM_THROTTLE_MS = 150;

/**
 * A run card backed by a single CardKit 2.0 entity. The whole card is
 * re-rendered from {@link RunState} each tick and pushed via
 * cardkit.v1.card.update; each push instantly renders the current full text
 * (streaming_mode is off — its client typewriter only applies to the
 * element-level content API, not whole-card updates, and ran far behind token
 * arrival). Throttled to STREAM_THROTTLE_MS so growth tracks the model in
 * chunks with zero trailing, and collapsible panels (reasoning / tools)
 * re-render cleanly. All updates share one strictly-increasing `seq` — Feishu
 * rejects out-of-order updates.
 *
 * Unlike im.v1.message.patch (unconditional, reverts a card touched during a
 * click's callback window), this is the correct surface for a card that both
 * streams and carries clickable controls (⏹).
 */
export class RunCardStream {
  private cardId = '';
  private _messageId = '';
  private seq = 0;
  private lastPush = 0;
  private lastContent = '';
  // [DIAG] per-turn push counters — surfaced via stats() for the stream.timing log.
  private pushCount = 0;
  private totalRttMs = 0;
  private maxRttMs = 0;
  // Coalesced streaming. The consume loop records the latest card in `pending`
  // and a single pump() drains it — NON-BLOCKING, so consuming codex events never
  // stalls on a card.update round-trip. Awaiting each push serially (the old
  // shape) drained the event backlog at one event per ~RTT, so codex finished
  // long before the card caught up ("已回完、飞书还在慢慢打字"). Coalescing pushes
  // only the most recent snapshot per round-trip, so the card tracks the model.
  private pending: CardObject | null = null;
  private pumpChannel: LarkChannel | null = null;
  private pumpPromise: Promise<void> | null = null;

  get messageId(): string {
    return this._messageId;
  }

  /** [DIAG] actual card.update push count + round-trip stats for this card. */
  stats(): { pushCount: number; totalRttMs: number; maxRttMs: number } {
    return { pushCount: this.pushCount, totalRttMs: this.totalRttMs, maxRttMs: this.maxRttMs };
  }

  /**
   * Record the latest card and ensure the pump is running. Returns immediately;
   * calls that arrive while a push is in flight collapse into a single push of
   * the most recent card once that round-trip completes. Use this from the
   * event-consume loop instead of awaiting {@link streamCard} per event.
   */
  streamCoalesced(channel: LarkChannel, fullCard: CardObject): void {
    this.pending = fullCard;
    this.pumpChannel = channel;
    if (!this.pumpPromise) this.pumpPromise = this.pump();
  }

  /** Await any in-flight coalesced push so the final streaming frame lands (and
   * `seq` stays ordered) before the terminal update. Call after the loop ends. */
  async drain(): Promise<void> {
    if (this.pumpPromise) await this.pumpPromise;
  }

  private async pump(): Promise<void> {
    try {
      while (this.pending && this.pumpChannel) {
        const card = this.pending;
        this.pending = null;
        const t0 = Date.now();
        await this.streamCard(this.pumpChannel, card, true);
        // RTT (~hundreds of ms) usually spaces pushes on its own; this floor only
        // bites if a round-trip is unusually fast, keeping us under Feishu's
        // single-card ~10 ops/sec cap.
        const gap = STREAM_THROTTLE_MS - (Date.now() - t0);
        if (this.pending && gap > 0) await new Promise((r) => setTimeout(r, gap));
      }
    } finally {
      this.pumpPromise = null;
    }
  }

  /** Create the entity from the initial (running) card and send a message
   * referencing it by card_id. Returns the carrier message id. */
  async create(
    channel: LarkChannel,
    chatId: string,
    initialCard: CardObject,
    opts: { replyTo?: string; replyInThread?: boolean },
  ): Promise<string> {
    const created = await channel.rawClient.cardkit.v1.card.create({
      data: { type: 'card_json', data: JSON.stringify(initialCard) },
    });
    const cardId = (created as { data?: { card_id?: string } }).data?.card_id;
    if (!cardId) {
      throw new Error(`cardkit.card.create returned no card_id: ${JSON.stringify(created).slice(0, 200)}`);
    }
    this.cardId = cardId;
    this.lastContent = JSON.stringify(initialCard);

    const content = JSON.stringify({ type: 'card', data: { card_id: cardId } });
    let messageId: string | undefined;
    if (opts.replyTo) {
      const r = await channel.rawClient.im.v1.message.reply({
        path: { message_id: opts.replyTo },
        data: { msg_type: 'interactive', content, reply_in_thread: opts.replyInThread ?? false },
      });
      messageId = (r as { data?: { message_id?: string } }).data?.message_id;
    } else {
      const r = await channel.rawClient.im.v1.message.create({
        params: { receive_id_type: 'chat_id' },
        data: { receive_id: chatId, msg_type: 'interactive', content },
      });
      messageId = (r as { data?: { message_id?: string } }).data?.message_id;
    }
    if (!messageId) throw new Error('run card send returned no message_id');
    this._messageId = messageId;
    return messageId;
  }

  /** Throttled whole-card stream update. Skips identical/too-soon pushes;
   * `force` flushes regardless (still de-duped on content). */
  async streamCard(channel: LarkChannel, fullCard: CardObject, force = false): Promise<void> {
    if (!this.cardId) return;
    const data = JSON.stringify(fullCard);
    if (data === this.lastContent) return;
    const now = Date.now();
    if (!force && now - this.lastPush < STREAM_THROTTLE_MS) return;
    this.lastPush = now;
    this.lastContent = data;
    const t0 = Date.now();
    try {
      await channel.rawClient.cardkit.v1.card.update({
        path: { card_id: this.cardId },
        data: { card: { type: 'card_json', data }, sequence: ++this.seq, uuid: `s_${this.cardId}_${this.seq}` },
      });
      const rtt = Date.now() - t0; // [DIAG]
      this.pushCount++;
      this.totalRttMs += rtt;
      if (rtt > this.maxRttMs) this.maxRttMs = rtt;
    } catch (err) {
      log.fail('card', err, { phase: 'run-stream', cardId: this.cardId, seq: this.seq });
    }
  }

  /** Forced whole-card replace for structural/terminal updates. A terminal
   * card built with streaming off clears the typewriter cursor. */
  async updateCard(channel: LarkChannel, fullCard: CardObject): Promise<void> {
    if (!this.cardId) return;
    const data = JSON.stringify(fullCard);
    this.lastContent = data;
    const push = async (): Promise<void> => {
      await channel.rawClient.cardkit.v1.card.update({
        path: { card_id: this.cardId },
        data: { card: { type: 'card_json', data }, sequence: ++this.seq, uuid: `u_${this.cardId}_${this.seq}` },
      });
    };
    try {
      await push();
    } catch (err) {
      // A terminal update fired right as a ⏹ click is still in its callback
      // window hits err 200810 ("card in ongoing interaction"). Wait out the
      // 3s window and retry once.
      log.fail('card', err, { phase: 'run-update', cardId: this.cardId, seq: this.seq, retry: true });
      await new Promise((r) => setTimeout(r, 3200));
      try {
        await push();
      } catch (err2) {
        log.fail('card', err2, { phase: 'run-update-retry', cardId: this.cardId, seq: this.seq });
      }
    }
  }
}
