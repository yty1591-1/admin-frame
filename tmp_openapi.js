const fs = require('fs');

const adminPath = 'api/openapi/admin.yaml';
const bizPath = 'api/openapi/biz.yaml';
const admin = fs.readFileSync(adminPath, 'utf8').split('\n');
const bizRaw = fs.readFileSync(bizPath, 'utf8');
const biz = bizRaw.split('\n');

// 1) 提取 admin.yaml 中所有 IAM path 块（按 2 空格缩进的下一级 path 结束）。
const blocks = [];
for (let i = 0; i < admin.length; i++) {
  if (!admin[i].startsWith('  /admin/v1/iam')) continue;
  let j = i + 1;
  while (j < admin.length && !admin[j].startsWith('  /') && admin[j] !== 'components:') j++;
  blocks.push(admin.slice(i, j));
  i = j - 1;
}

const wanted = blocks.filter((block) => !block[0].startsWith('  /admin/v1/iam/authorize'));
const iamPaths = wanted.map((block) => {
  let text = block.join('\n');
  text = text.split('/admin/v1/iam').join('/biz/v1/iam');
  text = text.replace(/operationId: admin/g, 'operationId: biz');
  text = text.split('需要 `iam:permission:read`')
    .join('需要 `iam:role:read`（B 端不使用 A 端专属的 iam:permission:read）');
  text = text.replace(
    'B 端强制按 Principal 的 merchant_id 收窄。',
    '本端强制按 Principal 的 merchant_id 收窄，并拒绝 channel=admin 的创建请求。'
  );
  return text;
});

// 2) 提取 IAM schema 块并剔除 A 端专属的 Authorize*。
const iamStart = admin.findIndex((line) => line.trim() === '# ---- IAM ----');
if (iamStart < 0) throw new Error('IAM schema marker not found');
const schemaBlock = admin.slice(iamStart);
const filteredSchemas = [];
let skipIndent = null;
for (const line of schemaBlock) {
  const m = /^    ([A-Za-z0-9_]+):\s*$/.exec(line);
  if (m) {
    if (['AuthorizeRequest', 'AuthorizeResource', 'AuthorizeResult'].includes(m[1])) {
      skipIndent = '    ' + m[1] + ':';
      continue;
    }
    skipIndent = null;
  }
  if (skipIndent) continue;
  filteredSchemas.push(line);
}

// 3) 参数块（RoleID / BindingID / SubjectTypePath / SubjectIDPath / IfMatchVersion）。
const paramStart = admin.findIndex((line) => line.trim() === 'RoleID:');
const paramEnd = admin.findIndex((line) => line.trim() === 'OrderID:');
if (paramStart < 0 || paramEnd < 0) throw new Error('IAM parameters not found');
const iamParams = admin.slice(paramStart, paramEnd).filter((line, idx, arr) => {
  // 去掉 RoleID 之前可能的空行，保留相对缩进。
  return !(idx === 0 && line === '');
});

// 4) 插入 paths：在 biz.yaml 的 components: 之前。
const compIndex = biz.findIndex((line) => line === 'components:');
if (compIndex < 0) throw new Error('components: not found in biz.yaml');
const pathsOut = ['', ...iamPaths].join('\n').split('\n');
biz.splice(compIndex, 0, ...pathsOut);

// 5) 插入 parameters：在 biz 的 securitySchemes: 之前。
const secIndex = biz.findIndex((line) => line === '  securitySchemes:');
if (secIndex < 0) throw new Error('securitySchemes not found');
biz.splice(secIndex, 0, ...iamParams, '');

// 6) 追加 schemas：文件末尾（components.schemas 是最后一节）。
while (biz.length && biz[biz.length - 1] === '') biz.pop();
biz.push(...filteredSchemas);

// 7) 增加 iam tag。
const tagIndex = biz.findIndex((line) => line === '  - name: orders');
if (tagIndex >= 0 && !bizRaw.includes('  - name: iam')) {
  biz.splice(tagIndex, 0, '  - name: iam', '    description: 本商户角色、授权与绑定（自助管理）');
}

fs.writeFileSync(bizPath, biz.join('\n') + '\n');
console.log('IAM paths:', wanted.length, 'schemas:', filteredSchemas.filter((l) => /^    [A-Za-z0-9_]+:$/.test(l)).length);
