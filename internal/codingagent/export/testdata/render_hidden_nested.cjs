// The probe executes the exported page's real rendering functions for the 0.99.1 viewer additions: custom messages
// that the terminal hides (template.js:1330-1335), the hidden-message toggle in the header (template.js:1409-1413) and
// the nested calls of a tool result (template.js:933-946). It stops before DOM event wiring, like render.cjs.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');

const html = fs.readFileSync(process.argv[2], 'utf8');
const scripts = [...html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)];
const payload = scripts.find(s => s[1].includes('id="session-data"'))[2];
const context = vm.createContext({
  atob, TextDecoder, Uint8Array, URLSearchParams,
  window: { location: { search: '' } },
  // The viewer's toggle states are declared after the part of the script this probe runs (template.js:1819-1821).
  thinkingExpanded: true, toolOutputsExpanded: false, showHiddenMessages: false,
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
      globalThis.render = { renderEntry, renderHeader, entries };
    })();`, context);
  } else {
    vm.runInContext(source, context);
  }
}
const { render } = context;
assert.ok(render, 'exported application script must load');
const entry = id => render.entries.find(e => e.id === id);

// A custom message the terminal hides is rendered hidden, with a label; a shown one is not.
const hidden = render.renderEntry(entry('m1'));
assert.match(hidden, /class="hook-message hook-message-hidden"/);
assert.match(hidden, /\[note\] · Hidden in terminal/);
assert.match(hidden, /secret/);
const shown = render.renderEntry(entry('m2'));
assert.match(shown, /class="hook-message"/);
assert.doesNotMatch(shown, /hook-message-hidden|Hidden in terminal/);
assert.match(shown, /visible/);

// The stylesheet hides the hidden messages until the body has show-hidden-messages.
assert.match(html, /body:not\(\.show-hidden-messages\) \.hook-message-hidden\s*\{\s*display: none;/);
assert.match(html, /\.header-toggle-btn\[aria-pressed="true"\]/);

// The header names the H key and carries the toggle button.
const header = render.renderHeader();
assert.match(header, /T toggle thinking · O toggle tools · H toggle hidden messages/);
assert.match(header, /data-action="toggle-hidden-messages" aria-pressed="false"/);
assert.match(header, />Show hidden messages<\/button>/);
assert.match(header, /data-action="toggle-thinking" aria-pressed="true"/);
assert.match(header, /data-action="toggle-tools" aria-pressed="false"/);

// A tool call lists the calls its tool made, without their results.
const call = render.renderEntry(entry('a1'));
assert.match(call, /Nested calls: 3/);
assert.match(call, /✓ read \{&quot;path&quot;:&quot;a&quot;\} 5ms/);
assert.match(call, /✗ bash \[arguments omitted, 9000 bytes\]/);
assert.match(call, /<div>    boom<\/div><div>    line2<\/div>/);
assert.match(call, /… slow \{\}/);
assert.doesNotMatch(call, /Nested calls: 3 \(incomplete record\)/);
const incomplete = render.renderEntry(entry('a2'));
assert.match(incomplete, /Nested calls: 1 \(incomplete record\)/);
assert.doesNotMatch(render.renderEntry(entry('a3')), /Nested calls/);
console.log('export hidden-message and nested-call assertions passed');
