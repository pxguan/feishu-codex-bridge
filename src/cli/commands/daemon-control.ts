import { spawnProcess } from '../../platform/spawn';
import { getServiceAdapter, isServiceRunning } from '../../service/adapter';
import { installLatest, isDevSource } from '../../service/update';
import { buildDaemonControlCommand, type DaemonControlAction } from '../../admin/host';
import { log } from '../../core/logger';

/**
 * detached helper：Web 控制台的「重启 daemon」/「升级」按钮的真正执行者。
 *
 * Web 控制台内嵌在 daemon 进程里——若在本进程直接 service stop→start，stop 会把
 * 自己（连同正响应这条 HTTP 的事件循环）杀掉，start 永远到不了。所以 server.ts
 * detached spawn 出一个**脱离本进程**的 helper（就是这里的 `__daemon-control`
 * 子命令）：它在 daemon 死后继续把 service 拉起来。
 *
 * - restart：service.restart()（launchd kickstart -k / systemd / 计划任务）。
 * - update：先 npm i -g <pkg>@latest，成功再 restart 让新代码生效。
 * 自身绝不 throw 出进程外（detached 没有调用方接错）；只落 service.err.log。
 */

/** detached spawn 出 helper 进程（web 进程内调用，立刻返回，绝不等它）。 */
export function spawnDaemonControl(action: DaemonControlAction): void {
  const { command, args } = buildDaemonControlCommand(action);
  const child = spawnProcess(command, args, {
    detached: true,
    stdio: 'ignore',
  });
  // 与父进程脱钩：父（被重启的 daemon）退出后 helper 仍存活，把 service 拉回来。
  child.unref();
}

/** helper 进程入口（`feishu-codex-bridge __daemon-control <action>`）。 */
export async function runDaemonControl(action: string): Promise<void> {
  try {
    if (action === 'update') {
      await doUpdate();
      return;
    }
    if (action === 'restart') {
      await doRestart('restart');
      return;
    }
    if (action === 'start') {
      await doStart();
      return;
    }
    if (action === 'stop') {
      await doStop();
      return;
    }
    log.warn('daemon-control', 'unknown-action', { action });
  } catch (err) {
    log.fail('daemon-control', err, { phase: action });
  }
}

/**
 * start：把后台服务装好并拉起（service install = launchd 写 plist + bootstrap）。
 * 由只读预览进程触发——预览本身没在跑 daemon，detached helper 把 service 装起来即可。
 * 已在跑就当幂等（install 内部 bootout+bootstrap 重建，等价重启）。
 */
async function doStart(): Promise<void> {
  await getServiceAdapter().install();
  log.info('daemon-control', 'start-issued', {});
}

/**
 * stop：停掉后台服务并移除自启（service uninstall = bootout + rm plist），与 CLI
 * `stop` 同义。在 web 进程里不能直停自己（会杀掉正响应这条 HTTP 的事件循环）→ 走
 * detached helper：本 daemon 被 bootout 杀掉后，helper 仍存活把 uninstall 走完。
 */
async function doStop(): Promise<void> {
  if (!isServiceRunning()) {
    // 没有 service manager 托管的实例（前台 run / 手动起）→ 没有可停的后台服务。
    log.info('daemon-control', 'stop-skipped-no-service', {});
    return;
  }
  await getServiceAdapter().uninstall();
  log.info('daemon-control', 'stop-issued', {});
}

async function doUpdate(): Promise<void> {
  if (isDevSource()) {
    // 源码开发模式：npm i -g 不会更新工作副本——什么都不做（Web 端已提示走 git pull）。
    log.info('daemon-control', 'update-skipped-dev', {});
    return;
  }
  const res = await installLatest();
  log.info('daemon-control', 'update-installed', { ok: res.ok });
  if (!res.ok) return; // 装失败就不重启（旧版继续跑，比半装的烂摊子安全）
  await doRestart('update');
}

async function doRestart(phase: string): Promise<void> {
  if (!isServiceRunning()) {
    // 服务没在跑（前台 run 的场景）→ 没有后台服务可重启；什么都不做。
    log.info('daemon-control', 'restart-skipped-no-service', { phase });
    return;
  }
  await getServiceAdapter().restart();
  log.info('daemon-control', 'restart-issued', { phase });
}
