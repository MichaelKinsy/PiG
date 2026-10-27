// Ports packages/coding-agent/test/export-html-system-messages.test.ts (Pi #11261): the template's pure system-message helpers,
// loaded from the shipped template.js the way Pi's test slices them out and runs them in a fresh context.
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { runInNewContext } = require("node:vm");

const templateJs = readFileSync(process.argv[2], "utf8");

function loadSystemHelpers(entries) {
  const start = templateJs.indexOf("function systemContentText(content)");
  const end = templateJs.indexOf("const hasSystemEntries");
  assert.ok(start >= 0);
  assert.ok(end > start);
  const context = { entries };
  runInNewContext(
    `const byId = new Map(entries.map(entry => [entry.id, entry]));
    ${templateJs.slice(start, end)}
    helpers = { getSystemMessageText, renderSystemMessageUpdate, isLeadingSystemEntry, describeSystemUpdate };`,
    context,
  );
  return context.helpers;
}

const initial = {
  role: "system",
  content: "You are a helpful assistant.",
  sections: { rules: "<rules>\nbe concise\n</rules>", project_context: "<project_context>\nv1\n</project_context>" },
  toolsAdded: [{ name: "read", description: "Read a file", parameters: { type: "object", properties: {} } }],
  timestamp: 1,
};

const update = {
  role: "system",
  content: [{ type: "text", text: "Extra instructions" }],
  sections: { project_context: "<project_context>\nv2\n</project_context>", rules: null },
  toolsAdded: [{ name: "bash", description: "Run a command", parameters: { type: "object", properties: {} } }],
  toolsRemoved: [{ name: "read" }],
  timestamp: 2,
};

const entries = [
  { type: "message", id: "s1", parentId: null, message: initial },
  { type: "message", id: "u1", parentId: "s1", message: { role: "user", content: "hi" } },
  { type: "message", id: "s2", parentId: "u1", message: update },
];

// pi-ai's getSystemMessageText and renderSystemMessageUpdate results for these messages (the Go test checks them against the pinned package).
let helpers = loadSystemHelpers(entries);
assert.equal(
  helpers.getSystemMessageText(initial),
  "You are a helpful assistant.\n\n<rules>\nbe concise\n</rules>\n\n<project_context>\nv1\n</project_context>",
);
assert.equal(
  helpers.renderSystemMessageUpdate(update),
  'Extra instructions\n\nUpdated system prompt section "project_context":\n\n<project_context>\nv2\n</project_context>\n\nRemoved system prompt section "rules".',
);
console.log("SYSTEM_TEXT " + JSON.stringify([helpers.getSystemMessageText(initial), helpers.renderSystemMessageUpdate(update)]));

assert.equal(helpers.isLeadingSystemEntry(entries[0]), true);
assert.equal(helpers.isLeadingSystemEntry(entries[2]), false);
assert.equal(helpers.describeSystemUpdate(update), "instructions, project_context, -rules, tools +bash -read");

helpers = loadSystemHelpers([
  { type: "compaction", id: "c1", parentId: null, systemMessage: initial },
  { type: "message", id: "s2", parentId: "c1", message: update },
]);
assert.equal(helpers.isLeadingSystemEntry({ id: "s2", parentId: "c1" }), false);

assert.match(templateJs, /if \(msg\.role === 'system'\) \{\s*let html = '';/);
assert.ok(templateJs.includes("renderSystemPromptBlock('System Prompt', text)"));
assert.ok(templateJs.includes("renderToolsList('Available Tools', msg.toolsAdded)"));
console.log("ok");
