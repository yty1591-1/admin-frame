#!/usr/bin/env node
/**
 * 文案硬编码门禁（S3 / INF-I18N-01）。
 * 扫描 apps/*\/src 与 packages/ui/src 的字符串字面量/JSX 文本中的 CJK 字符；
 * 注释先剥离，避免 JSDoc 误报。整行 `// i18n-ignore <理由>` 可豁免（须写明理由）。
 * 已知边界：不解析模板字符串内的注释；英文硬编码不在本轮范围。
 */
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));

const SCAN_DIRS = ['apps/admin-web/src', 'apps/biz-web/src', 'packages/ui/src'];
const EXCLUDED = [/\.test\.(ts|tsx)$/, /\.spec\.(ts|tsx)$/];
const EXCLUDED_PATHS = [path.join('packages', 'i18n', 'src', 'messages')];

/** 汉字 + 中日韩标点 + 全角字符；这些字符不应出现在代码层。 */
const CJK = /[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uff00-\uffef]/;
const IGNORE_MARKER = /\/\/\s*i18n-ignore\s+\S+/;

/** 将块注释与行注释替换为空格，保留换行以便定位行号。 */
function stripComments(source) {
  let inBlock = false;
  let inLine = false;
  let output = '';

  for (let index = 0; index < source.length; index += 1) {
    const current = source[index];
    const next = source[index + 1];

    if (inLine) {
      if (current === '\n') {
        inLine = false;
        output += current;
      } else {
        output += ' ';
      }
      continue;
    }
    if (inBlock) {
      if (current === '*' && next === '/') {
        inBlock = false;
        output += '  ';
        index += 1;
      } else {
        output += current === '\n' ? '\n' : ' ';
      }
      continue;
    }
    if (current === '/' && next === '/') {
      inLine = true;
      output += '  ';
      index += 1;
      continue;
    }
    if (current === '/' && next === '*') {
      inBlock = true;
      output += '  ';
      index += 1;
      continue;
    }
    output += current;
  }

  return output;
}

function collectFiles(directory) {
  const absolute = path.join(root, directory);
  let entries;
  try {
    entries = readdirSync(absolute, { withFileTypes: true });
  } catch {
    return [];
  }

  const files = [];
  for (const entry of entries) {
    const relative = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...collectFiles(relative));
      continue;
    }
    if (!/\.(ts|tsx)$/.test(entry.name)) {
      continue;
    }
    if (EXCLUDED.some((pattern) => pattern.test(entry.name))) {
      continue;
    }
    if (EXCLUDED_PATHS.some((excluded) => relative.startsWith(excluded))) {
      continue;
    }
    files.push(relative);
  }
  return files;
}

const violations = [];

for (const directory of SCAN_DIRS) {
  for (const relative of collectFiles(directory)) {
    const original = readFileSync(path.join(root, relative), 'utf8');
    const stripped = stripComments(original).split('\n');
    const originalLines = original.split('\n');

    stripped.forEach((line, index) => {
      if (!CJK.test(line)) {
        return;
      }
      if (IGNORE_MARKER.test(originalLines[index] ?? '')) {
        return;
      }
      violations.push(`${relative.replaceAll('\\', '/')}:${String(index + 1)}: ${line.trim()}`);
    });
  }
}

if (violations.length > 0) {
  console.error('ERROR: 检测到硬编码中文文案，请迁移到 packages/i18n 字典：');
  for (const violation of violations) {
    console.error(`  ${violation}`);
  }
  process.exit(1);
}

console.log('OK: i18n scan (no hardcoded CJK literals)');
