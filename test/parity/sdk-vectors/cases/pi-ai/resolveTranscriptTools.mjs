// Calls of Pi's resolveTranscriptTools that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
// Pi 1.3.0 took a boolean, whether the transport supports tool additions. These cases keep their transcripts and the boolean they were written with; the cases export below gives each one's
// Pi 1.4.0 argument, the TranscriptToolSupport it became (true is "additions", false is "none").
const supportFlagCases = [
  { name: "case 0001", args: [[], true] },
  { name: "case 0002", args: [[], false] },
  { name: "case 0003", args: [[{"role": "user", "content": "hi", "timestamp": 5}], true] },
  { name: "case 0004", args: [[{"role": "user", "content": "hi", "timestamp": 5}], false] },
  { name: "case 0005", args: [[{"role": "system", "content": "base", "timestamp": 10, "sections": {"a": "A", "b": "B"}, "toolsAdded": [{"name": "read", "description": "read tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "write", "description": "write tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}], true] },
  { name: "case 0006", args: [[{"role": "system", "content": "base", "timestamp": 10, "sections": {"a": "A", "b": "B"}, "toolsAdded": [{"name": "read", "description": "read tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "write", "description": "write tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}], false] },
  { name: "case 0007", args: [[{"role": "system", "content": "base", "timestamp": 10, "sections": {"a": "A", "b": "B"}, "toolsAdded": [{"name": "read", "description": "read tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "write", "description": "write tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}, {"role": "user", "content": "u", "timestamp": 11}, {"role": "system", "content": "more", "timestamp": 12, "sections": {"b": null, "c": "C", "a": "A2"}, "toolsRemoved": [{"name": "read"}], "toolsAdded": [{"name": "grep", "description": "grep tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}], true] },
  { name: "case 0008", args: [[{"role": "system", "content": "base", "timestamp": 10, "sections": {"a": "A", "b": "B"}, "toolsAdded": [{"name": "read", "description": "read tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "write", "description": "write tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}, {"role": "user", "content": "u", "timestamp": 11}, {"role": "system", "content": "more", "timestamp": 12, "sections": {"b": null, "c": "C", "a": "A2"}, "toolsRemoved": [{"name": "read"}], "toolsAdded": [{"name": "grep", "description": "grep tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}], false] },
  { name: "case 0009", args: [[{"role": "system", "content": "base", "timestamp": 10, "toolsAdded": [{"name": "a", "description": "a tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "b", "description": "b tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}, {"role": "system", "content": [{"type": "text", "text": "x"}], "timestamp": 11, "toolsRemoved": [{"name": "a"}]}, {"role": "system", "content": "y", "timestamp": 12, "toolsAdded": [{"name": "a", "description": "again", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}], true] },
  { name: "case 0010", args: [[{"role": "system", "content": "base", "timestamp": 10, "toolsAdded": [{"name": "a", "description": "a tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "b", "description": "b tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}, {"role": "system", "content": [{"type": "text", "text": "x"}], "timestamp": 11, "toolsRemoved": [{"name": "a"}]}, {"role": "system", "content": "y", "timestamp": 12, "toolsAdded": [{"name": "a", "description": "again", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}], false] },
  { name: "case 0011", args: [[{"role": "system", "content": "base", "timestamp": 10, "toolsAdded": [{"name": "a", "description": "a tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}, {"role": "system", "content": "", "timestamp": 11, "toolsAdded": [{"name": "a", "description": "again", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}], "toolsRemoved": [{"name": "a"}]}], true] },
  { name: "case 0012", args: [[{"role": "system", "content": "base", "timestamp": 10, "toolsAdded": [{"name": "a", "description": "a tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}, {"role": "system", "content": "", "timestamp": 11, "toolsAdded": [{"name": "a", "description": "again", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}], "toolsRemoved": [{"name": "a"}]}], false] },
  { name: "case 0013", args: [[{"role": "custom", "customType": "x", "content": "c", "timestamp": 1}, {"role": "system", "content": "late", "timestamp": 9}, {"role": "bashExecution", "command": "ls", "timestamp": 2}], true] },
  { name: "case 0014", args: [[{"role": "custom", "customType": "x", "content": "c", "timestamp": 1}, {"role": "system", "content": "late", "timestamp": 9}, {"role": "bashExecution", "command": "ls", "timestamp": 2}], false] },
  { name: "case 0015", args: [[{"role": "system", "content": "", "timestamp": 0}], true] },
  { name: "case 0016", args: [[{"role": "system", "content": "", "timestamp": 0}], false] },
  { name: "case 0017", args: [[{"role": "system", "content": "", "timestamp": 7, "toolsAdded": []}], true] },
  { name: "case 0018", args: [[{"role": "system", "content": "", "timestamp": 7, "toolsAdded": []}], false] },
  { name: "case 0019", args: [[{"role": "user", "content": "hi", "timestamp": 5}, {"role": "system", "content": "late", "timestamp": 3, "sections": {"1": "one", "9": "nine", "10": "ten", "z": "Z"}}], true] },
  { name: "case 0020", args: [[{"role": "user", "content": "hi", "timestamp": 5}, {"role": "system", "content": "late", "timestamp": 3, "sections": {"1": "one", "9": "nine", "10": "ten", "z": "Z"}}], false] },
  { name: "case 0021", args: [[{"role": "system", "content": "p", "timestamp": 4, "sections": {"x": "1", "y": "2", "z": "3"}}, {"role": "system", "content": "", "timestamp": 5, "sections": {"x": null, "w": "W"}}, {"role": "system", "content": "", "timestamp": 6, "sections": {"x": "again"}}], true] },
  { name: "case 0022", args: [[{"role": "system", "content": "p", "timestamp": 4, "sections": {"x": "1", "y": "2", "z": "3"}}, {"role": "system", "content": "", "timestamp": 5, "sections": {"x": null, "w": "W"}}, {"role": "system", "content": "", "timestamp": 6, "sections": {"x": "again"}}], false] },
  { name: "case 0023", args: [[{"role": "system", "content": "only tools", "timestamp": 8, "toolsRemoved": [{"name": "none"}]}], true] },
  { name: "case 0024", args: [[{"role": "system", "content": "only tools", "timestamp": 8, "toolsRemoved": [{"name": "none"}]}], false] },
];

export const cases = supportFlagCases.map(({ name, args: [messages, supportsToolAdditions] }) => ({ name, args: [messages, supportsToolAdditions ? "additions" : "none"] }));
const next = () => `case ${String(cases.length + 1).padStart(4, "0")}`;
// Pi 1.4.0 (packages/ai/src/utils/transcript.ts): "changes" declares every tool in place, and "additions" folds the system messages up to the last removal or redeclaration into the top-level list.
for (const { args: [messages, supportsToolAdditions] } of supportFlagCases) if (supportsToolAdditions) cases.push({ name: next(), args: [messages, "changes"] });
const tool = (name, description = `${name} tool`) => ({ name, description, parameters: { type: "object", properties: {} } });
const system = (extra = {}) => ({ role: "system", content: "", timestamp: 1, ...extra });
const user = { role: "user", content: "hi", timestamp: 2 };
for (const messages of [
  // a removal in the middle, then later additions
  [system({ content: "base", toolsAdded: [tool("a"), tool("b")] }), user, system({ toolsRemoved: [{ name: "a" }] }), user, system({ toolsAdded: [tool("c")] }), system({ toolsAdded: [tool("d")] })],
  // removed, then declared again (Kimi K3 ignores a removed tool that a later tool message declares again)
  [system({ toolsAdded: [tool("a")] }), system({ toolsRemoved: [{ name: "a" }] }), system({ toolsAdded: [tool("a")] }), system({ toolsAdded: [tool("b")] })],
  // a redefinition under the same name
  [system({ toolsAdded: [tool("a")] }), system({ toolsAdded: [tool("a", "changed")] }), system({ toolsAdded: [tool("b")] })],
  // no leading system message
  [user, system({ toolsAdded: [tool("a")] }), system({ toolsRemoved: [{ name: "a" }] }), system({ toolsAdded: [tool("b")] })],
  [user, system({ toolsAdded: [tool("a")] }), system({ toolsAdded: [tool("b")] })],
  // additions only
  [system({ toolsAdded: [tool("a")] }), user, system({ toolsAdded: [tool("b")] }), system({ toolsAdded: [tool("c")] })],
  // a removal and an addition in the leading message
  [system({ toolsRemoved: [{ name: "a" }], toolsAdded: [tool("b")] }), system({ toolsAdded: [tool("c")] })],
  // custom roles between system messages
  [{ role: "custom", customType: "x", content: "c", timestamp: 1 }, system({ toolsAdded: [tool("a")] }), { role: "bashExecution", command: "ls", timestamp: 2 }, system({ toolsRemoved: [{ name: "a" }] })],
]) for (const support of ["additions", "none", "changes"]) cases.push({ name: next(), args: [messages, support] });
