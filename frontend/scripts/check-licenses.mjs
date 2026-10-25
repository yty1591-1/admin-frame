#!/usr/bin/env node
/**
 * 许可证门禁（FE-17 / LIC-01）。
 * 使用 pnpm 自带的 licenses 汇总（无需额外依赖），按允许列表 fail-closed。
 */
import { execSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const allowlist = JSON.parse(
  readFileSync(new URL('../license-allowlist.json', import.meta.url), 'utf8'),
).allowed;

const allowed = new Set(allowlist);

function normalize(license) {
  return license
    .replace(/[()]/g, '')
    .split(/\s+OR\s+/i)
    .map((part) => part.trim())
    .filter((part) => part !== '');
}

function isAllowed(license) {
  if (typeof license !== 'string' || license.trim() === '') {
    return false;
  }
  return normalize(license).some(
    (part) => allowed.has(part) || allowed.has(part.split(/\s+WITH\s+/i)[0]),
  );
}

function collectEntries(payload) {
  const entries = [];
  if (Array.isArray(payload)) {
    for (const item of payload) {
      entries.push({ name: item.name ?? 'unknown', license: item.license });
    }
    return entries;
  }
  for (const [license, group] of Object.entries(payload)) {
    if (!Array.isArray(group)) {
      continue;
    }
    for (const item of group) {
      entries.push({ name: item.name ?? 'unknown', license: item.license ?? license });
    }
  }
  return entries;
}

const root = fileURLToPath(new URL('..', import.meta.url));
// 命令为常量，经 shell 解析以兼容各平台 pnpm 垫片（Windows 为 .cmd）。
const raw = execSync('pnpm licenses list --json', {
  cwd: root,
  encoding: 'utf8',
  maxBuffer: 32 * 1024 * 1024,
});

const entries = collectEntries(JSON.parse(raw));
const rejected = entries.filter((entry) => !isAllowed(entry.license));

if (rejected.length > 0) {
  const unique = new Map();
  for (const entry of rejected) {
    unique.set(`${entry.name}@${entry.license}`, entry);
  }
  for (const entry of unique.values()) {
    console.error(`ERROR: 许可证不在允许列表: ${entry.name} (${entry.license ?? 'unknown'})`);
  }
  process.exit(1);
}

console.log(`OK: licenses (${entries.length} packages)`);
