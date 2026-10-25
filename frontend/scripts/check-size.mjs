#!/usr/bin/env node
/**
 * 体积预算门禁（FE-19 / 前端架构 §13.1）。
 * 首屏 JS（gzip）= index.html 直接引用的入口与 preload chunk 之和；
 * 其余按路由懒加载 chunk 逐个校验。
 */
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const root = fileURLToPath(new URL('..', import.meta.url));

const BUDGETS = [
  { app: 'admin-web', dir: 'apps/admin-web/dist', initialKb: 300 },
  { app: 'biz-web', dir: 'apps/biz-web/dist', initialKb: 260 },
];

const CHUNK_KB = 120;

function gzipKb(file) {
  return gzipSync(readFileSync(file)).length / 1024;
}

const failures = [];

for (const budget of BUDGETS) {
  const dist = path.join(root, budget.dir);
  const indexPath = path.join(dist, 'index.html');
  if (!existsSync(indexPath)) {
    failures.push(`${budget.app}: 缺少构建产物，请先执行 pnpm build`);
    continue;
  }

  const html = readFileSync(indexPath, 'utf8');
  const initial = new Set(
    [...html.matchAll(/(?:src|href)="\/?(assets\/[^"]+\.js)"/g)].map((match) => match[1]),
  );

  const assetsDir = path.join(dist, 'assets');
  const files = readdirSync(assetsDir).filter((file) => file.endsWith('.js'));

  let initialKb = 0;
  for (const file of files) {
    const relative = `assets/${file}`;
    const kb = gzipKb(path.join(assetsDir, file));
    if (initial.has(relative)) {
      initialKb += kb;
      continue;
    }
    if (kb > CHUNK_KB) {
      failures.push(
        `${budget.app}: 路由 chunk ${relative} ${kb.toFixed(1)} KB 超过 ${CHUNK_KB} KB`,
      );
    }
  }

  if (initialKb > budget.initialKb) {
    failures.push(`${budget.app}: 首屏 JS ${initialKb.toFixed(1)} KB 超过 ${budget.initialKb} KB`);
  }
  console.log(`${budget.app}: 首屏 JS ${initialKb.toFixed(1)} KB / ${budget.initialKb} KB`);
}

if (failures.length > 0) {
  for (const failure of failures) {
    console.error(`ERROR: ${failure}`);
  }
  process.exit(1);
}

console.log('OK: size budgets');
