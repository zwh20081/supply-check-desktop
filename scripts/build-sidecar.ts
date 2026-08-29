import { mkdir } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { arch, platform } from 'node:os';
import { resolve } from 'node:path';

/** Rust target triple → Go 的 GOOS/GOARCH 与产物后缀。 */
const targets: Record<string, { goos: string; goarch: string; exe: boolean }> = {
  'x86_64-pc-windows-msvc': { goos: 'windows', goarch: 'amd64', exe: true },
  'aarch64-pc-windows-msvc': { goos: 'windows', goarch: 'arm64', exe: true },
  'x86_64-apple-darwin': { goos: 'darwin', goarch: 'amd64', exe: false },
  'aarch64-apple-darwin': { goos: 'darwin', goarch: 'arm64', exe: false },
  'x86_64-unknown-linux-gnu': { goos: 'linux', goarch: 'amd64', exe: false },
  'aarch64-unknown-linux-gnu': { goos: 'linux', goarch: 'arm64', exe: false },
};

const hostTargets: Record<string, string> = {
  'win32-x64': 'x86_64-pc-windows-msvc',
  'win32-arm64': 'aarch64-pc-windows-msvc',
  'darwin-x64': 'x86_64-apple-darwin',
  'darwin-arm64': 'aarch64-apple-darwin',
  'linux-x64': 'x86_64-unknown-linux-gnu',
  'linux-arm64': 'aarch64-unknown-linux-gnu',
};

// CI 交叉编译时用 --target 或 SIDECAR_TARGET 指定，本地开发默认取宿主平台。
const explicit = process.argv.find((item) => item.startsWith('--target='))?.slice('--target='.length)
  ?? process.env.SIDECAR_TARGET;

const host = `${platform()}-${arch()}`;
const target = explicit || hostTargets[host];
if (!target) throw new Error(`不支持的 sidecar 构建平台: ${host}`);

const spec = targets[target];
if (!spec) {
  throw new Error(`未知的 target triple: ${target}\n可用: ${Object.keys(targets).join(', ')}`);
}

const root = resolve(import.meta.dir, '..');
const outputDir = resolve(root, 'src-tauri', 'binaries');
await mkdir(outputDir, { recursive: true });
const output = resolve(outputDir, `supply-check-sdk-${target}${spec.exe ? '.exe' : ''}`);

// -H=windowsgui 抑制控制台窗口，只对 Windows 产物有意义
const linkerFlags = spec.goos === 'windows' ? '-s -w -H=windowsgui' : '-s -w';

const build = spawnSync(
  platform() === 'win32' ? 'go.exe' : 'go',
  ['build', '-trimpath', `-ldflags=${linkerFlags}`, '-o', output, '.'],
  {
    cwd: resolve(root, 'sidecar'),
    stdio: 'inherit',
    shell: false,
    env: {
      ...process.env,
      GOOS: spec.goos,
      GOARCH: spec.goarch,
      // 侧车是纯 Go，关掉 CGO 才能无工具链交叉编译
      CGO_ENABLED: '0',
    },
  },
);
if (build.error) throw build.error;
if (build.status !== 0) process.exit(build.status ?? 1);
console.log(`SDK sidecar (${spec.goos}/${spec.goarch}): ${output}`);
