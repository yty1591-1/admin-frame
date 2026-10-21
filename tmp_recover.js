const fs = require('fs');
const path = require('path');
const p = 'C:/Users/14593/.cursor/projects/d-MyDocument-admin-frame/agent-transcripts/45ea54b8-6d5f-41be-bd8e-44ada49722c8/45ea54b8-6d5f-41be-bd8e-44ada49722c8.jsonl';
const lines = fs.readFileSync(p, 'utf8').split('\n');

function apply(original, ops) {
  let content = original;
  for (const op of ops) {
    if (op.name === 'Write') {
      content = op.input.contents;
      continue;
    }
    const oldStr = op.input.old_string;
    const newStr = op.input.new_string;
    if (typeof oldStr !== 'string' || typeof newStr !== 'string') continue;
    if (!content.includes(oldStr)) {
      console.error('MISS', op.line, JSON.stringify(oldStr.slice(0, 60)));
      continue;
    }
    content = op.input.replace_all === true
      ? content.split(oldStr).join(newStr)
      : content.replace(oldStr, newStr);
  }
  return content;
}

for (const base of ['dto.go', 'handler.go']) {
  const ops = [];
  lines.forEach((l, i) => {
    if (!l.includes('transport') || !l.includes(base)) return;
    let o;
    try { o = JSON.parse(l); } catch { return; }
    const c = o.message && o.message.content;
    if (!Array.isArray(c)) return;
    for (const part of c) {
      if (part.type !== 'tool_use') continue;
      if (!part.input || typeof part.input.path !== 'string') continue;
      if (!part.input.path.endsWith('\\' + base)) continue;
      if (part.name !== 'Write' && part.name !== 'StrReplace') continue;
      ops.push({ line: i, name: part.name, input: part.input });
    }
  });
  ops.sort((a, b) => a.line - b.line);
  console.log(base, 'ops:', ops.map((o) => o.line + ':' + o.name).join(' '));
  let content = '';
  for (const op of ops) {
    if (op.name === 'Write') {
      content = op.input.contents;
      continue;
    }
    const oldStr = op.input.old_string;
    const newStr = op.input.new_string;
    if (!content.includes(oldStr)) {
      console.error('  MISS at', op.line, JSON.stringify(oldStr.slice(0, 80)));
      continue;
    }
    content = op.input.replace_all === true
      ? content.split(oldStr).join(newStr)
      : content.replace(oldStr, newStr);
  }
  fs.mkdirSync('tmp_recovered', { recursive: true });
  fs.writeFileSync(path.join('tmp_recovered', base), content, 'utf8');
  console.log('  wrote', base, content.length, 'chars');
}
