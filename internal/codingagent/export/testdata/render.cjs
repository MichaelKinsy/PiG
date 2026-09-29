// The probe executes the exported page's real rendering functions and vendored
// libraries without a browser layout engine. It stops before DOM event wiring.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');

const html = fs.readFileSync(process.argv[2], 'utf8');
const scripts = [...html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)];
const payload = scripts.find(s => s[1].includes('id="session-data"'))[2];
const context = vm.createContext({
  atob, TextDecoder, Uint8Array, URLSearchParams,
  window: { location: { search: '' } },
  document: {
    getElementById: () => ({ textContent: payload }),
    querySelector: () => null,
  },
});
for (const [, attributes, source] of scripts) {
  if (attributes.includes('id="session-data"')) continue;
  if (source.includes('// Search input')) {
    const prefix = source.slice(0, source.indexOf('// Search input'));
    vm.runInContext(prefix + `
      globalThis.render = { renderEntry, getTreeNodeDisplayHtml, formatExpandableOutput, safeMarkedParse, renderHeader };
    })();`, context);
  } else {
    vm.runInContext(source, context);
  }
}
const render = context.render;
assert.ok(render, 'exported application script must load');
const user = (text, images = []) => ({
  id: 'id"<&', type: 'message',
  message: { role: 'user', content: [{ type: 'text', text }, ...images] },
});
const skill = '<skill name="review" location="/skills/SKILL.md">\n**instructions**\n</skill>';
const skillOnly = render.renderEntry(user(skill));
assert.match(skillOnly, /class="skill-invocation"/);
assert.match(skillOnly, /<strong>instructions<\/strong>/);
assert.doesNotMatch(skillOnly, /class="user-message"/);
assert.doesNotMatch(skillOnly, /&lt;skill/);
const skillWithPrompt = render.renderEntry(user(skill + '\n\nactual prompt'));
assert.match(skillWithPrompt, /<\/div>\s*<\/div><div class="user-message">/);
assert.match(skillWithPrompt, /<p>actual prompt<\/p>/);
const tree = render.getTreeNodeDisplayHtml(user(skill + '\n\nactual prompt'));
assert.match(tree, /skill:<\/span> review · .*user:<\/span> actual prompt/);
assert.match(render.renderEntry(user('ordinary')), /class="user-message"/);

const image = { type: 'image', mimeType: 'image/png" onerror="bad', data: '" onerror="bad' };
for (const text of [skill, 'ordinary']) {
  const output = render.renderEntry(user(text, [image]));
  assert.match(output, /class="user-message"/);
  assert.doesNotMatch(output, /" onerror="bad/);
  assert.match(output, /image\/png&quot; onerror=&quot;bad;base64,&quot; onerror=&quot;bad/);
  assert.match(output, /id="entry-id&quot;&lt;&amp;"/);
  assert.match(output, /data-entry-id="id&quot;&lt;&amp;"/);
}
for (const url of ['javascript:alert(1)', 'java\tscript:alert(1)', 'vbscript:evil', 'data:text/html,bad', 'custom:bad']) {
  const output = render.safeMarkedParse(`[link](<${url}>) ![image](<${url}>)`);
  assert.doesNotMatch(output, /<(?:a|img)\b/);
}
for (const url of ['https://example.com', 'mailto:me@example.com', 'tel:123', 'ftp://example.com', '/relative']) {
  assert.match(render.safeMarkedParse(`[link](${url})`), /<a href=/);
}
assert.match(render.safeMarkedParse('[link](https://example.com/?q=%22)'), /href="https:\/\/example.com\/\?q=%22"/);
const preview = render.formatExpandableOutput('one\ntwo\nthree', 2, 'plaintext');
assert.match(preview, /<code class="hljs">one\ntwo<\/code>/);
assert.match(preview, /\(1 more lines\)/);
const plain = render.formatExpandableOutput('  one\n\ttwo\nthree', 2);
assert.match(plain, /<div>  one<\/div><div>   two<\/div>/);
console.log('export rendering assertions passed');
