import type { CAC } from 'cac';

import { access, mkdtemp, readFile, rm } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { extname, join } from 'node:path';

import { execa, getStagedFiles } from '@vben/node-utils';

import { exceedsThreshold } from './threshold';

const require = createRequire(import.meta.url);
const circularScannerCli =
  require.resolve('circular-dependency-scanner/dist/cli.js');

// 默认配置
const DEFAULT_CONFIG = {
  allowedExtensions: ['.cjs', '.js', '.jsx', '.mjs', '.ts', '.tsx', '.vue'],
  ignoreDirs: [
    'dist',
    '.turbo',
    'output',
    '.cache',
    'scripts',
    'internal',
    'packages/effects/request/src/',
    'packages/@core/ui-kit/menu-ui/src/',
    'packages/@core/ui-kit/popup-ui/src/',
  ],
  threshold: 0, // 循环依赖的阈值
} as const;

// 类型定义
type CircularDependencyResult = string[];

interface CheckCircularConfig {
  allowedExtensions?: string[];
  ignoreDirs?: string[];
  threshold?: number;
}

interface CommandOptions {
  config?: CheckCircularConfig;
  staged: boolean;
  verbose: boolean;
}

// 缓存机制
const cache = new Map<string, CircularDependencyResult[]>();

async function detectCircularDependencies({
  cwd,
  ignorePattern,
  staged,
}: {
  cwd: string;
  ignorePattern: string;
  staged: boolean;
}): Promise<CircularDependencyResult[]> {
  const tempDir = await mkdtemp(join(tmpdir(), 'vsh-check-circular-'));
  const outputFile = join(tempDir, 'circles.json');

  try {
    const args = [circularScannerCli, cwd, '--output', outputFile];

    if (staged) {
      args.push('--absolute');
    }

    args.push('--ignore', ignorePattern);

    await execa(process.execPath, args, {
      cwd,
    });

    await access(outputFile);
    const output = await readFile(outputFile, 'utf8');
    return JSON.parse(output) as CircularDependencyResult[];
  } catch (error) {
    if ((error as NodeJS.ErrnoException)?.code === 'ENOENT') {
      return [];
    }
    throw error;
  } finally {
    await rm(tempDir, { force: true, recursive: true });
  }
}

/**
 * 格式化循环依赖的输出
 * @param circles - 循环依赖结果
 */
function formatCircles(circles: CircularDependencyResult[]): void {
  if (circles.length === 0) {
    console.log('✅ No circular dependencies found');
    return;
  }

  console.log('⚠️ Circular dependencies found:');
  circles.forEach((circle, index) => {
    console.log(`\nCircular dependency #${index + 1}:`);
    circle.forEach((file) => console.log(`  → ${file}`));
  });
}

/**
 * 检查项目中的循环依赖
 * @param options - 检查选项
 * @param options.staged - 是否只检查暂存区文件
 * @param options.verbose - 是否显示详细信息
 * @param options.config - 自定义配置
 * @returns Promise<void>
 */
async function checkCircular({
  config = {},
  staged,
  verbose,
}: CommandOptions): Promise<void> {
  try {
    // 合并配置
    const finalConfig = {
      ...DEFAULT_CONFIG,
      ...config,
    };

    // 阈值非法时回退为 0，避免 NaN 让门禁静默通过
    const threshold = Number.isFinite(finalConfig.threshold)
      ? finalConfig.threshold
      : 0;

    // 生成忽略模式
    const ignorePattern = `**/{${finalConfig.ignoreDirs.join(',')}}/**`;

    // 检查缓存
    const cacheKey = `${staged}-${process.cwd()}-${ignorePattern}`;
    if (cache.has(cacheKey)) {
      const cachedResults = cache.get(cacheKey);
      if (cachedResults) {
        if (verbose) {
          formatCircles(cachedResults);
        }
        if (exceedsThreshold(cachedResults.length, threshold)) {
          process.exitCode = 1;
        }
      }
      return;
    }

    // 检测循环依赖
    const results = await detectCircularDependencies({
      cwd: process.cwd(),
      ignorePattern,
      staged,
    });

    let found = results;

    if (staged) {
      let files = await getStagedFiles();
      const allowedExtensions = new Set(finalConfig.allowedExtensions);

      // 过滤文件列表
      files = files.filter((file) => allowedExtensions.has(extname(file)));

      const circularFiles: CircularDependencyResult[] = [];
      const seen = new Set<CircularDependencyResult>();

      for (const file of files) {
        for (const result of results) {
          const resultFiles = result.flat();
          if (!seen.has(result) && resultFiles.includes(file)) {
            seen.add(result);
            circularFiles.push(result);
          }
        }
      }

      found = circularFiles;
    }

    // 更新缓存
    cache.set(cacheKey, found);
    if (verbose) {
      formatCircles(found);
    }

    // 超过阈值即判失败，退出码必须非 0，否则门禁形同虚设
    if (exceedsThreshold(found.length, threshold)) {
      console.error(
        `\n❌ Circular dependencies: ${found.length} found, threshold is ${threshold}`,
      );
      process.exitCode = 1;
    }
  } catch (error) {
    console.error(
      '❌ Error checking circular dependencies:',
      error instanceof Error ? error.message : error,
    );
    process.exitCode = 1;
  }
}

/**
 * 定义检查循环依赖的命令
 * @param cac - CAC实例
 */
function defineCheckCircularCommand(cac: CAC): void {
  cac
    .command('check-circular')
    .option('--staged', 'Only check staged files')
    .option('--verbose', 'Show detailed information')
    .option('--threshold <number>', 'Threshold for circular dependencies', {
      default: 0,
    })
    .option('--ignore-dirs <dirs>', 'Directories to ignore, comma separated')
    .usage('Analyze project circular dependencies')
    .action(async ({ ignoreDirs, staged, threshold, verbose }) => {
      const config: CheckCircularConfig = {
        threshold: Number(threshold),
        ...(ignoreDirs && { ignoreDirs: ignoreDirs.split(',') }),
      };

      await checkCircular({
        config,
        staged,
        verbose: verbose ?? true,
      });
    });
}

export { type CheckCircularConfig, defineCheckCircularCommand };
