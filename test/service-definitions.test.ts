import { describe, expect, it } from 'vitest';
import { buildUnit, SYSTEMD_UNIT_NAME } from '../src/service/systemd';
import { buildLauncherCmd, WINDOWS_TASK_NAME } from '../src/service/schtasks';

// The systemd unit (Linux/WSL) and the schtasks .cmd launcher (Windows) are
// generated text that can't be exercised on the mac dev box — lock their shape
// here so a refactor can't silently break the daemon definitions.

describe('systemd unit (buildUnit)', () => {
  const unit = buildUnit();

  it('is a well-formed user service with crash-restart + login autostart', () => {
    expect(unit).toContain('[Service]');
    expect(unit).toContain('Type=simple');
    expect(unit).toContain('Restart=always');
    expect(unit).toContain('RestartSec=5');
    expect(unit).toContain('[Install]');
    expect(unit).toContain('WantedBy=default.target');
  });

  it('runs the bridge `run` subcommand and appends to the shared log files', () => {
    expect(unit).toMatch(/ExecStart=".+" ".+" run/);
    expect(unit).toContain('StandardOutput=append:');
    expect(unit).toContain('StandardError=append:');
    expect(unit).toContain('Environment="PATH=');
  });

  it('unit name is a .service', () => {
    expect(SYSTEMD_UNIT_NAME).toMatch(/\.service$/);
  });
});

describe('schtasks launcher (.cmd)', () => {
  const cmd = buildLauncherCmd();

  it('is a CRLF batch script that sets PATH and runs the bridge', () => {
    expect(cmd.startsWith('@echo off')).toBe(true);
    expect(cmd).toContain('\r\n'); // cmd.exe needs CRLF
    expect(cmd).toContain('set "PATH=');
    expect(cmd).toMatch(/".+" ".+" run /);
  });

  it('appends stdout and stderr to the log files', () => {
    expect(cmd).toContain('>> "');
    expect(cmd).toContain('2>> "');
  });

  it('task name has no backslashes (no Task Scheduler folder)', () => {
    expect(WINDOWS_TASK_NAME).not.toContain('\\');
  });
});
