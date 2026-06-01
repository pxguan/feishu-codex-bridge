import { mkdir, readdir, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import type { LarkChannel, NormalizedMessage } from '@larksuiteoapi/node-sdk';
import { paths } from '../config/paths';
import { log } from '../core/logger';

/**
 * Inbound image handling: download the images a user sends to (or forwards to)
 * the bot so they can be passed to codex as `localImage` inputs — codex reads
 * the local file directly (see {@link AgentInput.images} / `toUserInput`).
 *
 * The SDK normalizer already surfaces a message's images two ways:
 *   - `msg.resources` (type 'image') — for plain image messages AND rich-text
 *     (post) images. `fileKey` is the Feishu `image_key`, in the bot's chat.
 *   - merge_forward (合并转发) content embeds `![image](key)` per sub-message but
 *     leaves `msg.resources` EMPTY (convertMergeForward returns no resources),
 *     so forwarded images must be recovered by walking the sub-messages.
 *
 * User-sent images can ONLY be downloaded via `im.v1.messageResource.get`
 * (message-resource/get); the standalone `im.v1.image.get` (what the SDK's
 * `channel.downloadResource` uses) is limited to images the BOT itself
 * uploaded. Per Feishu, message-resource/get needs the bot to share the
 * resource's chat and "暂不支持获取合并转发消息中的子消息的资源文件" — so forwarded
 * sub-message images are attempted best-effort and skipped (logged, not
 * thrown) when Feishu rejects them.
 */

/** Cap per message so a flood of images can't wedge a turn or fill the disk. */
const MAX_IMAGES = 9;
/** Downloaded files live this long; codex reads them within the turn (seconds),
 * so an hour is generous. Pruned lazily on the next download. */
const MEDIA_TTL_MS = 60 * 60_000;

interface ImageRef {
  /** the message that DIRECTLY holds the resource — `msg.messageId` for a plain
   * image, the sub-message id for a forwarded one. */
  messageId: string;
  /** Feishu image_key (img_v3_…). */
  fileKey: string;
}

const EXT_BY_CONTENT_TYPE: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpg',
  'image/jpg': 'jpg',
  'image/gif': 'gif',
  'image/webp': 'webp',
  'image/bmp': 'bmp',
  'image/heic': 'heic',
  'image/heif': 'heif',
  'image/tiff': 'tiff',
};

/** Cheap synchronous check: does this message carry images worth downloading?
 * Lets the hot path skip the async resource walk entirely. */
export function messageHasImages(msg: NormalizedMessage): boolean {
  if ((msg.resources ?? []).some((r) => r.type === 'image')) return true;
  // merge_forward never lists resources; its sub-messages may still hold images.
  return msg.rawContentType === 'merge_forward';
}

/**
 * Download every image in `msg` to local files and return their absolute paths
 * (codex reads them directly). Best-effort end to end: a failed gather or a
 * single failed download is logged and skipped, never thrown — a missing image
 * must not break the turn.
 */
export async function collectInboundImages(channel: LarkChannel, msg: NormalizedMessage): Promise<string[]> {
  let refs: ImageRef[];
  try {
    refs = await gatherRefs(channel, msg);
  } catch (err) {
    log.warn('intake', 'image-gather-failed', { err: String(err) });
    return [];
  }
  if (refs.length === 0) return [];

  await pruneOldMedia();
  try {
    await mkdir(paths.mediaDir, { recursive: true });
  } catch {
    /* mkdir failure surfaces on writeFile below */
  }

  const out: string[] = [];
  let index = 0;
  for (const ref of refs.slice(0, MAX_IMAGES)) {
    const path = await downloadOne(channel, ref, index++);
    if (path) out.push(path);
  }
  log.info('intake', 'images', { found: refs.length, downloaded: out.length });
  return out;
}

/** Collect (messageId, fileKey) pairs for every image: direct resources first,
 * then any inside a forwarded message. Deduped by fileKey. */
async function gatherRefs(channel: LarkChannel, msg: NormalizedMessage): Promise<ImageRef[]> {
  const refs: ImageRef[] = [];
  const seen = new Set<string>();
  const add = (messageId: string, fileKey: string | undefined): void => {
    if (!fileKey || seen.has(fileKey)) return;
    seen.add(fileKey);
    refs.push({ messageId, fileKey });
  };

  for (const r of msg.resources ?? []) {
    if (r.type === 'image') add(msg.messageId, r.fileKey);
  }

  if (msg.rawContentType === 'merge_forward') {
    // `im.v1.message.get` on a merge_forward returns a FLAT list: the parent
    // first, then every descendant (the same shape the SDK's converter walks).
    const items = await fetchSubMessages(channel, msg.messageId);
    for (const sub of items) {
      if (!sub.message_id || sub.message_id === msg.messageId) continue;
      for (const key of imageKeysFromContent(sub.msg_type, sub.body?.content)) {
        add(sub.message_id, key);
      }
    }
  }

  return refs;
}

interface SubMessageItem {
  message_id?: string;
  msg_type?: string;
  body?: { content?: string };
}

async function fetchSubMessages(channel: LarkChannel, messageId: string): Promise<SubMessageItem[]> {
  try {
    const res = await channel.rawClient.im.v1.message.get({ path: { message_id: messageId } });
    return (res.data as { items?: SubMessageItem[] } | undefined)?.items ?? [];
  } catch (err) {
    log.warn('intake', 'submessages-failed', { messageId, err: String(err) });
    return [];
  }
}

/** Pull image_keys out of one sub-message's raw body content. Plain `image`
 * carries `image_key` at the top; `post` (rich text) nests `{tag:'img'}` nodes.
 * A generic walk covers both plus any other img-bearing shape. Exported for
 * testing — the forwarded-message parsing is the bug-prone part. */
export function imageKeysFromContent(msgType: string | undefined, content: string | undefined): string[] {
  if (!content) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(content);
  } catch {
    return [];
  }
  if (msgType === 'image') {
    const key = (parsed as { image_key?: string } | null)?.image_key;
    return key ? [key] : [];
  }
  const keys: string[] = [];
  walkForImageKeys(parsed, keys);
  return keys;
}

function walkForImageKeys(node: unknown, out: string[]): void {
  if (!node || typeof node !== 'object') return;
  if (Array.isArray(node)) {
    for (const child of node) walkForImageKeys(child, out);
    return;
  }
  const obj = node as Record<string, unknown>;
  if (obj.tag === 'img' && typeof obj.image_key === 'string') out.push(obj.image_key);
  for (const k of Object.keys(obj)) walkForImageKeys(obj[k], out);
}

async function downloadOne(channel: LarkChannel, ref: ImageRef, index: number): Promise<string | undefined> {
  try {
    const res = await channel.rawClient.im.v1.messageResource.get({
      path: { message_id: ref.messageId, file_key: ref.fileKey },
      params: { type: 'image' },
    });
    const ext = extFromHeaders(res.headers);
    const file = join(paths.mediaDir, `${safeName(ref.fileKey)}-${index}.${ext}`);
    await res.writeFile(file);
    return file;
  } catch (err) {
    // Forwarded sub-message images land here (Feishu rejects message-resource
    // for merge_forward children) — info, not error: the turn still proceeds.
    log.warn('intake', 'image-download-failed', { fileKey: ref.fileKey.slice(0, 24), err: String(err) });
    return undefined;
  }
}

function extFromHeaders(headers: unknown): string {
  const ct = readHeader(headers, 'content-type');
  if (ct) {
    const base = ct.split(';')[0]?.trim().toLowerCase();
    if (base && EXT_BY_CONTENT_TYPE[base]) return EXT_BY_CONTENT_TYPE[base];
  }
  return 'png';
}

function readHeader(headers: unknown, name: string): string | undefined {
  if (!headers || typeof headers !== 'object') return undefined;
  const h = headers as { get?: (n: string) => unknown } & Record<string, unknown>;
  const raw = typeof h.get === 'function' ? h.get(name) : (h[name] ?? h[name.toLowerCase()]);
  return typeof raw === 'string' ? raw : Array.isArray(raw) ? String(raw[0]) : undefined;
}

/** Feishu image_keys are filename-safe already; sanitize defensively + clamp. */
function safeName(fileKey: string): string {
  return fileKey.replace(/[^a-zA-Z0-9_-]/g, '').slice(-40) || 'img';
}

async function pruneOldMedia(): Promise<void> {
  let entries: string[];
  try {
    entries = await readdir(paths.mediaDir);
  } catch {
    return; // dir not created yet — nothing to prune
  }
  const cutoff = Date.now() - MEDIA_TTL_MS;
  for (const name of entries) {
    const file = join(paths.mediaDir, name);
    try {
      const st = await stat(file);
      if (st.mtimeMs < cutoff) await rm(file, { force: true });
    } catch {
      /* skip files that vanish or can't be stat'd */
    }
  }
}
