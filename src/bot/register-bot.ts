import { setSecret } from '../config/keystore';
import { buildEncryptedAccountConfig, loadConfig, saveConfig } from '../config/store';
import { botPaths } from '../config/paths';
import { isComplete, secretKeyForApp, type TenantBrand } from '../config/schema';
import { validateAppCredentials } from '../utils/feishu-auth';
import { addBot, loadBots, uniqueName } from '../config/bots';
import { log } from '../core/logger';

/**
 * 非 TTY、非全局态的「直填 appId + appSecret 注册一个 bot」——day-0 场景：bot 还没
 * 连上之前飞书 DM 卡片根本不存在，只能从 Web 控制台 / CLI 手填密钥把它注册进来。
 *
 * 与扫码向导（bot/wizard.ts + onboarding.registerNewBot）的差异、以及为什么不复用它：
 *   - wizard 走 `registerApp` 扫码创建**新应用**并拿明文密钥，必须有人在 TTY 前扫码，
 *     daemon / Web 进程都满足不了；这里是用户**已在开发者后台建好应用**、手填既有
 *     appId+secret，纯写盘，无任何交互。
 *   - registerNewBot 用 `useBotDir()` 切**全局**当前目录再 saveConfig——daemon 进程内
 *     绝不能这么干（会把在跑 bot 的 paths 指歪，见 paths.ts useBotDir 警告）。这里全程
 *     走 {@link botPaths} 的显式路径写该 bot 自己的 config.json，零全局副作用。
 *
 * 安全：appSecret 只在本函数内一次性流过——真探活验证有效后立刻进 AES keystore
 * （config/keystore.ts），config.json 里存的是指向 keystore 的 exec SecretRef（明文
 * 绝不落 config / bots.json / 日志）。日志只记 appId + botName，绝不记 secret。
 *
 * 幂等：appId 已注册过 → addBot 按 appId 覆盖（替换 entry），keystore setSecret 覆盖
 * 旧密钥（用于「换了密钥重新填一遍」）。
 */

export interface RegisterBotInput {
  appId: string;
  appSecret: string;
  tenant: TenantBrand;
  /** 期望的短句柄（默认用探活拿到的 botName / appId 派生，registry 内唯一化）。 */
  desiredName?: string;
}

export interface RegisterBotResult {
  ok: true;
  name: string;
  appId: string;
  tenant: TenantBrand;
  botName?: string;
  /** 必需 scope 中尚未授权的（undefined = 没查成；空 = 已齐全）。 */
  missingScopes?: string[];
}

export interface RegisterBotFailure {
  ok: false;
  /** 机器可分支的失败原因：格式错 / 探活拒绝（密钥无效）/ 写盘失败。 */
  code: 'invalid_input' | 'credential_rejected' | 'persist_failed';
  /** 面向人的中文原因（UI toast / CLI 直出，绝不含 secret）。 */
  reason: string;
}

/** appId 形如 `cli_xxx`（飞书自建应用）；做一道轻校验，别让明显的脏输入打真 API。 */
const APP_ID_RE = /^cli_[A-Za-z0-9]{6,}$/;

/**
 * 校验 + 真探活 + 落盘注册一个 bot。绝不 throw——所有失败都落到
 * {@link RegisterBotFailure}，调用方（Web 路由 / CLI）按 code 分支映射状态码/文案。
 * `validate` 仅供测试注入（默认打真飞书 tenant_access_token 接口）。
 */
export async function registerBotFromCredentials(
  input: RegisterBotInput,
  validate: typeof validateAppCredentials = validateAppCredentials,
): Promise<RegisterBotResult | RegisterBotFailure> {
  const appId = input.appId?.trim() ?? '';
  const appSecret = input.appSecret?.trim() ?? '';
  const tenant: TenantBrand = input.tenant === 'lark' ? 'lark' : 'feishu';

  if (!appId || !appSecret) {
    return { ok: false, code: 'invalid_input', reason: 'App ID 与 App Secret 都不能为空。' };
  }
  if (!APP_ID_RE.test(appId)) {
    return {
      ok: false,
      code: 'invalid_input',
      reason: 'App ID 格式不对：应为开发者后台「凭证与基础信息」里的 App ID（形如 cli_ 开头）。',
    };
  }

  // 真探活：换 tenant_access_token，密钥无效直接拒绝——不让坏密钥落进 keystore。
  const v = await validate(appId, appSecret, tenant);
  if (!v.ok) {
    return {
      ok: false,
      code: 'credential_rejected',
      reason: `凭据校验失败：${v.reason ?? '未知原因'}。请核对 App ID / App Secret（应用可能被禁用或 Secret 已重置）。`,
    };
  }

  try {
    // secret 先进 keystore（AES-256-GCM），再写指向它的 exec SecretRef config——
    // 顺序保证 config 落盘时密钥已可解析；明文绝不进 config.json / bots.json。
    await setSecret(secretKeyForApp(appId), appSecret);

    const files = botPaths(appId);
    // 显式路径读旧 config（同 appId 重填时保留既有 preferences，如管理员名单），
    // 不存在 / 不完整就建全新的——绝不 useBotDir 切全局目录。
    const existing = await loadConfig(files.configFile);
    const preferences = isComplete(existing) ? existing.preferences : undefined;
    const cfg = await buildEncryptedAccountConfig(appId, tenant, preferences);
    await saveConfig(cfg, files.configFile);

    const reg = await loadBots();
    const name = uniqueName(reg, input.desiredName ?? v.botName ?? appId);
    await addBot({ name, appId, tenant, botName: v.botName, createdAt: Date.now() });

    log.info('register-bot', 'bot-registered', { name, appId, bot: v.botName ?? null });
    return { ok: true, name, appId, tenant, botName: v.botName, missingScopes: v.missingScopes };
  } catch (err) {
    log.fail('register-bot', err, { phase: 'persist', appId });
    return {
      ok: false,
      code: 'persist_failed',
      reason: `保存失败：${err instanceof Error ? err.message : String(err)}`,
    };
  }
}
