import { createInterface } from 'node:readline/promises';
import { loadConfig, saveConfig, buildEncryptedAccountConfig } from '../config/store';
import { setSecret } from '../config/keystore';
import { isComplete, secretKeyForApp, type AppConfig } from '../config/schema';
import { resolveAppSecret } from '../config/secret-resolver';
import { runRegistrationWizard } from './wizard';
import { validateAppCredentials } from '../utils/feishu-auth';
import { buildScopeGrantUrl, buildEventConfigUrl, labelScope } from '../config/scopes';
import { resolveCodexBin } from '../agent/codex-appserver/locate';
import { openUrl } from '../utils/open-url';
import { log } from '../core/logger';
import { useBotDir } from '../config/paths';
import { ensureRegistry, addBot, currentBot, findBot, loadBots, uniqueName, type BotEntry } from '../config/bots';

export interface OnboardResult {
  cfg: AppConfig;
  secret: string;
  /** required scopes still ungranted at validation time (undefined = couldn't check). */
  missingScopes?: string[];
}

/** Verify codex CLI is present (needed to run AND to spawn the per-session app-server). */
export function ensureCodex(): boolean {
  if (resolveCodexBin()) return true;
  console.error(
    '✗ 未找到 codex CLI。请先安装 Codex 并登录：\n' +
      '    • 安装：npm i -g @openai/codex（或安装 Codex.app，或用 CODEX_BIN 指向已有二进制）\n' +
      '    • 登录：codex login\n' +
      '  然后重跑。可先用 `feishu-codex-bridge doctor` 自检。',
  );
  return false;
}

/**
 * Bring a bot to a runnable state and return its config + secret.
 *
 * - With `opts.bot` (a name or appId), resolves THAT specific bot — used by
 *   `run --bot <name>` and the multi-bot supervisor's children. A missing
 *   selector is an error (never the create-wizard, even with `allowCreate`).
 * - Without a selector, resolves the registry's `current` bot (migrating a
 *   legacy flat install on first run). With `allowCreate`, a missing current
 *   bot triggers the scan-QR wizard (named `default`) — this is what makes the
 *   implicit `run`/`start` "init if not initialized". Without it (or off a TTY)
 *   a missing bot is an error.
 * - Validates credentials; on missing scopes it prints a non-blocking notice
 *   (which features are gated, where to grant, that 诊断 can grant later) and
 *   auto-opens the grant page on a TTY — but NEVER blocks startup or reads
 *   stdin, so a human at a terminal and codex/automation behave identically.
 *
 * Returns null (after printing why) on any failure.
 */
export async function ensureOnboarded(
  opts: { allowCreate?: boolean; bot?: string } = {},
): Promise<OnboardResult | null> {
  if (!ensureCodex()) return null;

  const reg = await ensureRegistry();
  const entry = opts.bot ? findBot(reg, opts.bot) : currentBot(reg);
  if (!entry) {
    if (opts.bot) {
      console.error(
        `✗ 找不到机器人「${opts.bot}」。用 \`feishu-codex-bridge bot list\` 查看已注册的机器人。`,
      );
      return null;
    }
    if (!opts.allowCreate) {
      console.error('✗ 尚未配置任何飞书机器人。请先运行 `feishu-codex-bridge bot init`（或前台 `run`）扫码创建。');
      return null;
    }
    return registerNewBot('default');
  }

  useBotDir(entry.appId);
  const cfg = await loadConfig();
  if (!isComplete(cfg)) {
    console.error(`✗ 当前机器人「${entry.name}」(${entry.appId}) 配置缺失或损坏。可 \`bot rm ${entry.name}\` 后重新 \`bot init\`。`);
    return null;
  }
  const r = await validateAndReport(cfg);
  if (r === null) return null;
  return { cfg, secret: r.secret, missingScopes: r.missingScopes };
}

/**
 * Run the scan-QR wizard to create a brand-new feishu app, persist it (keystore
 * + per-bot config dir + registry), validate, and surface the scope link.
 * `desiredName` defaults to the bot's display name (slugified); pass `'default'`
 * for the implicit first-run from `run`/`start`. Returns null on failure.
 */
export async function registerNewBot(desiredName?: string): Promise<OnboardResult | null> {
  // The scan needs a human at a terminal. A headless context (the launchd
  // service, CI) must never enter the wizard — `registerApp` would print a QR
  // to a log nobody reads and poll forever. Fail fast with a pointer instead.
  if (!process.stdout.isTTY) {
    console.error(
      '✗ 当前不是交互式终端，无法扫码创建飞书应用。\n' +
        '  请在终端前台运行 `feishu-codex-bridge bot init`（或 `run` / `start`）扫码 onboarding。',
    );
    return null;
  }

  const wizardCfg = await runRegistrationWizard();
  const app = wizardCfg.accounts.app;
  if (typeof app.secret !== 'string') {
    console.error('✗ 向导未返回明文密钥，无法继续。');
    return null;
  }

  // Validate with the plaintext secret before persisting — bad creds shouldn't
  // leave a half-registered bot behind.
  const v = await validateAppCredentials(app.id, app.secret, app.tenant);
  if (!v.ok) {
    console.error(`✗ 应用凭据校验失败：${v.reason}`);
    return null;
  }

  await setSecret(secretKeyForApp(app.id), app.secret);
  useBotDir(app.id); // from here all per-bot files land under bots/<appId>/
  const cfg = await buildEncryptedAccountConfig(app.id, app.tenant, wizardCfg.preferences);
  await saveConfig(cfg);

  const reg = await loadBots();
  const name = uniqueName(reg, desiredName ?? v.botName ?? 'default');
  await addBot({ name, appId: app.id, tenant: app.tenant, botName: v.botName, createdAt: Date.now() });

  console.log(`✓ 已创建机器人「${name}」  bot: ${v.botName ?? '-'}  appId: ${app.id}`);
  log.info('onboard', 'bot-created', { name, appId: app.id, bot: v.botName ?? null });
  noticeMissingScopes(cfg, v.missingScopes);

  const secret = await resolveAppSecret(cfg);
  return { cfg, secret, missingScopes: v.missingScopes };
}

/** Resolve secret, validate credentials, report result; on missing scopes,
 *  print the non-blocking notice (see {@link noticeMissingScopes}). */
async function validateAndReport(
  cfg: AppConfig,
): Promise<{ secret: string; missingScopes?: string[] } | null> {
  const secret = await resolveAppSecret(cfg);
  const v = await validateAppCredentials(cfg.accounts.app.id, secret, cfg.accounts.app.tenant);
  if (!v.ok) {
    console.error(`✗ 应用凭据校验失败：${v.reason}`);
    console.error('  应用可能被禁用/未发布；可重跑 `feishu-codex-bridge bot init` 重新扫码。');
    return null;
  }
  console.log(`✓ 凭据校验通过  bot: ${v.botName ?? '-'}  appId: ${cfg.accounts.app.id}`);
  log.info('onboard', 'credentials-ok', { appId: cfg.accounts.app.id, bot: v.botName ?? null });
  noticeMissingScopes(cfg, v.missingScopes);
  return { secret, missingScopes: v.missingScopes };
}

/**
 * Gate used by `start` before daemonizing — the one truly-manual step. Missing
 * scopes are deliberately NOT handled here: {@link validateAndReport} already
 * printed the non-blocking notice during onboarding, and we never block daemon
 * install on scopes (some tenants can't grant on demand; 诊断 grants later). What
 * remains is events & callbacks, which have NO API to verify (not even a read)
 * and no deep-preselect — so we print exact click-paths and trust the operator's
 * Enter. No-op off a TTY (scripted re-install / codex). Always returns true on a
 * TTY now; the boolean is kept for the signature `start` relies on.
 */
export async function confirmReadyForDaemon(result: OnboardResult): Promise<boolean> {
  if (!process.stdin.isTTY) return true;
  const { app } = result.cfg.accounts;

  // Events AND callbacks have no API at all (not even a read to verify), and no
  // deep-preselect — only scopes have those. So this is the one truly-manual,
  // un-checkable step: print exact click-paths and trust the operator's Enter.
  // The classic trap: card.action.trigger is a *callback* (回调配置 tab), NOT an
  // event — searching for it under 「添加事件」 finds nothing. Spell out the tabs.
  const eventUrl = buildEventConfigUrl(app.id, app.tenant);
  const opened = openUrl(eventUrl);
  console.log('\n最后这几步飞书没有 API/深链可代办（连查询订阅状态的接口都没有），需你手动点：\n');
  console.log(`  【1】事件与回调（${opened ? '已自动打开' : '打开下面链接'}）：${eventUrl}`);
  console.log('       这页顶部有三个标签：「事件配置」「回调配置」「加密策略」。');
  console.log('       • 切到「事件配置」标签 → 「订阅方式」改「长连接」→ 点「添加事件」搜并勾选：');
  console.log('           im.message.receive_v1（接收消息）、application.bot.menu_v6（机器人菜单）');
  console.log('       • （可选）想要「在飞书文档评论里 @机器人就自动回复」，再加这一个事件：');
  console.log('           drive.notice.comment_add_v1（云文档新增评论）');
  console.log('           它依赖「文档评论」权限（docs:document.comment:read / :create，授权链接已预勾选）；不加则该功能静默关闭。');
  console.log('       • （可选）想要「把我加进已有群就能绑定成项目」，再加这两个事件：');
  console.log('           im.chat.member.bot.added_v1（机器人被加入群 → 私聊推送绑定卡）');
  console.log('           im.chat.member.bot.deleted_v1（机器人被移出群 → 自动解绑项目）');
  console.log('           它们依赖「群信息/群成员」权限（im:chat:readonly / im:chat.members:write_only，已预勾选）；不加则该功能静默关闭。');
  console.log('       • 切到「回调配置」标签 → 「订阅方式」改「长连接」→ 点「添加回调」勾选：');
  console.log('           card.action.trigger（卡片回传交互）');
  console.log('           ⚠️ 它是「回调」不是「事件」——在上面「添加事件」里搜不到，必须切到「回调配置」这个标签。');
  console.log('  【2】左侧栏「应用发布 → 版本管理与发布」→ 创建一个版本并发布。');
  console.log('\n  （保存「长连接」订阅方式要求长连接在线；若提示连接未建立，');
  console.log('    先开另一个终端跑 `feishu-codex-bridge run` 把桥连上，再回这页保存。）');
  await promptEnter('\n以上都点完后按 Enter 安装后台服务（Ctrl+C 取消）… ');
  return true;
}

async function promptEnter(message: string): Promise<string> {
  const rl = createInterface({ input: process.stdin, output: process.stdout });
  try {
    return await rl.question(message);
  } finally {
    rl.close();
  }
}

/**
 * 缺权限时只「告知」，绝不阻塞启动、绝不读 stdin —— 人和 codex/自动化行为一致。
 * 打印：缺哪些功能、去哪申请、之后可从私聊「🩺 诊断」补。TTY 下顺手自动开一次浏览器
 * 授权页（{@link openUrl} 自身在非 TTY 直接 no-op，所以 codex / supervisor 子进程 /
 * daemon 不会弹浏览器、只打印链接）。missingScopes 三态：undefined=没查成、空=已齐全。
 */
function noticeMissingScopes(cfg: AppConfig, missingScopes: string[] | undefined): void {
  if (missingScopes === undefined) {
    log.info('onboard', 'scope-check-skipped', { reason: 'scope list unavailable' });
    return;
  }
  if (missingScopes.length === 0) return;
  const url = buildScopeGrantUrl(cfg.accounts.app.id, cfg.accounts.app.tenant);
  const opened = openUrl(url); // TTY → 开浏览器并返回 true；非 TTY → 不开、返回 false
  // 纯 ASCII 边框：`-` 在 UTF-8 与 GBK 下编码相同，老式 cmd.exe（CP936）也不会乱码。
  const rule = '-'.repeat(64);
  console.log(`\n${rule}`);
  console.log(`⚠️  缺 ${missingScopes.length} 项权限（不影响启动，但这些功能开通前用不了）：`);
  for (const s of missingScopes) console.log(`   · ${labelScope(s)}`);
  console.log(
    opened
      ? '🌐 已自动打开浏览器授权页（即时生效、无需重启）：'
      : '   去这里申请（勾选 → 确认，即时生效、无需重启）：',
  );
  console.log(`   👉 ${url}`);
  console.log('   不想现在弄也行：之后私聊机器人 →「🩺 诊断」可随时再申请。');
  console.log(`${rule}\n`);
}

export type { BotEntry };
