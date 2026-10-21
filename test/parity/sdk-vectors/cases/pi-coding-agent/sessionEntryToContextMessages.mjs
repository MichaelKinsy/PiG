// What each entry type contributes: messages with missing content, custom, branch summary and compaction entries, and entries that are no message. Timestamps are ISO strings plus ones Date.parse reads differently.
export const cases = [
  { name: "entry 01", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "user", "content": "hi", "timestamp": 7}}] },
  { name: "entry 02", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "user", "timestamp": 7}}] },
  { name: "entry 03", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "user", "content": null, "timestamp": 7}}] },
  { name: "entry 04", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "assistant", "timestamp": 7}}] },
  { name: "entry 05", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "toolResult", "toolCallId": "t", "timestamp": 7}}] },
  { name: "entry 06", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "system", "timestamp": 7}}] },
  { name: "entry 07", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "system", "content": "sys", "timestamp": 7}}] },
  { name: "entry 08", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "bashExecution", "command": "ls", "timestamp": 7}}] },
  { name: "entry 09", args: [{"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "message": {"role": "custom", "timestamp": 7}}] },
  { name: "entry 10", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "customType": "note", "content": "text", "display": true}] },
  { name: "entry 11", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "customType": "note", "content": [{"type": "text", "text": "t"}], "display": false, "details": {"a": 1, "b": [2]}}] },
  { name: "entry 12", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "customType": "note", "display": true}] },
  { name: "entry 13", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "customType": "note", "content": null, "display": true, "details": null}] },
  { name: "entry 14", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "not a date", "customType": "note", "content": "x", "display": true}] },
  { name: "entry 15", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "2026-01-02", "customType": "note", "content": "x", "display": true}] },
  { name: "entry 16", args: [{"type": "custom_message", "id": "e1", "parentId": null, "timestamp": "Jan 2, 2026 10:00:00 GMT+0200", "customType": "note", "content": "x", "display": true}] },
  { name: "entry 17", args: [{"type": "branch_summary", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "fromId": "f1", "summary": "sum"}] },
  { name: "entry 18", args: [{"type": "branch_summary", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "fromId": "f1", "summary": ""}] },
  { name: "entry 19", args: [{"type": "branch_summary", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "fromId": "f1"}] },
  { name: "entry 20", args: [{"type": "compaction", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "summary": "cs", "firstKeptEntryId": "k", "tokensBefore": 1234}] },
  { name: "entry 21", args: [{"type": "compaction", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "summary": "cs", "firstKeptEntryId": "k", "tokensBefore": 0, "systemMessage": {"role": "system", "content": "s", "timestamp": 3}}] },
  { name: "entry 22", args: [{"type": "compaction", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "summary": "", "firstKeptEntryId": "k", "tokensBefore": 5, "systemMessage": null}] },
  { name: "entry 23", args: [{"type": "label", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "targetId": "t", "label": "l"}] },
  { name: "entry 24", args: [{"type": "model_change", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "provider": "p", "modelId": "m"}] },
  { name: "entry 25", args: [{"type": "thinking_level_change", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "thinkingLevel": "high"}] },
  { name: "entry 26", args: [{"type": "custom", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "customType": "c", "data": {"x": 1}}] },
  { name: "entry 27", args: [{"type": "session_info", "id": "e1", "parentId": null, "timestamp": "2026-01-02T03:04:05.678Z", "name": "n"}] },
];

// A property Pi leaves undefined is no member of the answer (JSON.stringify drops it), so the vector records the JSON the answer has.
const json = (value) => (value === undefined ? undefined : JSON.parse(JSON.stringify(value)));
export const call = async (fn, ...args) => json(await fn(...args));
