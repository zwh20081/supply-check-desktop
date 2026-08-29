import { mkdir } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { arch, platform } from 'node:os';
import { resolve } from 'node:path';

const targetByHost: Record<string, string> = {
  'win32-x64': 'x86_64-pc-windows-msvc',
  'win32-arm64': 'aarch64-pc-windows-msvc',
  'darwin-x64': 'x86_64-apple-darwin',
  'darwin-arm64': 'aarch64-apple-darwin',
  'linux-x64': 'x86_64-unknown-linux-gnu',
  'linux-arm64': 'aarch64-unknown-linux-gnu',
};

const host = `${platform()}-${arch()}`;
const target = targetByHost[host];
if (!target) throw new Error(`不支持的 sidecar 构建平台: ${host}`);

const root = resolve(import.meta.dir, '..');
const outputDir = resolve(root, 'src-tauri', 'binaries');
await mkdir(outputDir, { recursive: true });
const extension = platform() === 'win32' ? '.exe' : '';
const output = resolve(outputDir, `supply-check-sdk-${target}${extension}`);
const goCommand = platform() === 'win32' ? 'go.exe' : 'go';
const linkerFlags = platform() === 'win32' ? '-s -w -H=windowsgui' : '-s -w';

const build = spawnSync(
  goCommand,
  ['build', '-trimpath', `-ldflags=${linkerFlags}`, '-o', output, '.'],
  {
    cwd: resolve(root, 'sidecar'),
    stdio: 'inherit',
    shell: false,
  },
);
if (build.error) throw build.error;
if (build.status !== 0) process.exit(build.status ?? 1);
console.log(`SDK sidecar: ${output}`);
