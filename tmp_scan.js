const fs = require('fs');
const p = 'C:/Users/14593/.cursor/projects/d-MyDocument-admin-frame/agent-transcripts/45ea54b8-6d5f-41be-bd8e-44ada49722c8/45ea54b8-6d5f-41be-bd8e-44ada49722c8.jsonl';
const lines = fs.readFileSync(p, 'utf8').split('\n');
lines.forEach((l, i) => {
  if (!l.includes('transport')) return;
  let o;
  try { o = JSON.parse(l); } catch { return; }
  const c = o.message && o.message.content;
  if (!Array.isArray(c)) return;
  for (const part of c) {
    if (part.type !== 'tool_use') continue;
    const input = part.input || {};
    if (typeof input.path !== 'string') continue;
    if (!input.path.includes('transport')) continue;
    console.log(i, part.name, input.path, 'keys=' + Object.keys(input).join(','));
  }
});
