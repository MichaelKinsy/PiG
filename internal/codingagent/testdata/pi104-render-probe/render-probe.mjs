// Differential probe for Pi 1.0.4's built-in read/write/edit renderers.
// Usage: node render-probe.mjs <pi-dist/pi-coding-agent dir> <shims dir> <cwd> <home> > golden.json
// The golden file holds each case's inputs and the rows Pi renders; {{CWD}} and {{HOME}} stand for the probe's paths.
import { setTimeout as sleep } from "node:timers/promises";
import fs from "node:fs";
const [D, SHIMS, CWD, HOME] = process.argv.slice(2);
const { initTheme, theme } = await import(`${D}/modes/interactive/theme/theme.js`);
const { readRenderers } = await import(`${D}/core/tools/renderers/read.js`);
const { writeRenderers } = await import(`${D}/core/tools/renderers/write.js`);
const { editRenderers } = await import(`${D}/core/tools/renderers/edit.js`);
const { KeybindingsManager } = await import(`${D}/core/keybindings.js`);
const tui = await import(`${SHIMS}/pi-tui.mjs`);
initTheme("dark");
tui.setKeybindings(new KeybindingsManager({}, "/nonexistent/keybindings.json"));
const W = 100;
const renderers = { read: readRenderers, write: writeRenderers, edit: editRenderers };
const sub = (v) => JSON.parse(JSON.stringify(v).replaceAll("{{CWD}}", CWD).replaceAll("{{HOME}}", HOME));
const unsub = (s) => s.replaceAll(CWD, "{{CWD}}").replaceAll(HOME, "{{HOME}}");
const txt = (t) => ({ content: [{ type: "text", text: t }] });
const cases = [];
// A step is {op:"call"|"result"|"wait"|"render", args, ctx, result}; "render" re-renders the call or result component.
const C = (name, ...steps) => cases.push({ name, steps });
const call = (tool, args, ctx = {}) => ({ op: "call", tool, args, ctx });
const res = (tool, args, result, ctx = {}) => ({ op: "result", tool, args, result, ctx });
const wait = () => ({ op: "wait" });
const again = (of, tool) => ({ op: "render", of, tool });

const long = Array.from({ length: 25 }, (_, i) => `line ${i + 1}\tx`).join("\n");
const wlong = Array.from({ length: 25 }, (_, i) => `const v${i} = ${i}; // \tc`).join("\n");
const imgs = { content: [{ type: "text", text: "Read image file [image/png]" }, { type: "image", data: "iVBORw0KGgo=", mimeType: "image/png" }] };
for (const exp of [false, true]) {
  const e = exp ? "/x" : "/c";
  const x = { expanded: exp };
  for (const [n, a] of Object.entries({
    rel: { path: "a/b.go" }, filepath: { file_path: "f.go", path: "p.go" }, nullfp: { file_path: null, path: "p.go" },
    missing: {}, empty: { path: "" }, num: { path: 5 }, undef: null, range: { path: "a.go", offset: 3, limit: 4 },
    offonly: { path: "a.go", offset: 3 }, limonly: { path: "a.go", limit: 4 }, nullrange: { path: "a.go", offset: null, limit: null },
    zerooff: { path: "a.go", offset: 0, limit: 0 }, skill: { path: "/x/y/my-skill/SKILL.md" }, skillrange: { path: "my-skill/SKILL.md", offset: 2, limit: 5 },
    skillroot: { path: "/SKILL.md" }, agents: { path: "AGENTS.md" }, agentsabs: { path: "{{CWD}}/sub/CLAUDE.md" }, agentsoutside: { path: "/other/AGENTS.override.md" },
    agentsMD: { path: "AGENTS.MD", offset: 1 }, agentsfp: { file_path: "AGENTS.md", path: "x.go" }, home: { path: "{{HOME}}/z.go" }, tilde: { path: "~/z.go" },
    off0lim1: { path: "a.go", offset: 0, limit: 1 }, offfloat: { path: "a.go", offset: 1.5, limit: 2 }, offstr: { path: "a.go", offset: "3", limit: 4 },
    limstr: { path: "a.go", limit: "4" }, offbool: { path: "a.go", offset: true, limit: 2 }, offneg: { path: "a.go", offset: -2, limit: 1 }, offbig: { path: "a.go", offset: 1e21, limit: 1 },
    skillnum: { path: 5 }, skillnullpath: { path: null, file_path: null }, link: { path: "d i r/ü#x?.go" }, linkabs: { path: "/a b/c%d.go" }, linkfile: { path: "file:///tmp/f%20x.go" },
    readme: { path: "README.md" }, docs: { path: "docs/x.md" }, claudelower: { path: "claude.md" },
  })) C(`read.call:${n}${e}`, call("read", a, x));
  for (const [n, a, r, c] of [
    ["ok-go", { path: "a.go" }, txt("package a\n\nfunc F() {}\n")],
    ["ok-txt", { path: "a.txt" }, txt("hello\tworld\nsecond")],
    ["ok-nopath", {}, txt("hello")], ["ok-numpath", { path: 5 }, txt("hello")], ["ok-nullargs", null, txt("hello")],
    ["ok-filepath", { file_path: "z.go", path: "a.txt" }, txt("package z")],
    ["long-go", { path: "a.go" }, txt(long)], ["long-txt", { path: "a.txt" }, txt(long)],
    ["err", { path: "a.go" }, txt("ENOENT: no such file"), { isError: true }], ["errlong", { path: "a.go" }, txt(long), { isError: true }],
    ["empty", { path: "a.go" }, txt("")], ["trailing", { path: "a.txt" }, txt("a\n\n\n")],
    ["ansi", { path: "a.txt" }, txt("a\x1b[31mred\x1b[0m\r\nb\x07c")], ["wide", { path: "a.txt" }, txt("x".repeat(130))],
    ["image", { path: "a.png" }, imgs, { showImages: false }], ["image-show", { path: "a.png" }, imgs, { showImages: true }],
    ["partial", { path: "a.go" }, txt("package a"), { isPartial: true }],
    ...Object.entries({
      first: { truncated: true, firstLineExceedsLimit: true, maxBytes: 2048 }, firstdef: { truncated: true, firstLineExceedsLimit: true },
      lines: { truncated: true, truncatedBy: "lines", outputLines: 2000, totalLines: 5000, maxLines: 2000 },
      linesdef: { truncated: true, truncatedBy: "lines", outputLines: 3, totalLines: 5 },
      bytes: { truncated: true, truncatedBy: "bytes", outputLines: 40, maxBytes: 51200 }, bytesdef: { truncated: true, truncatedBy: "bytes", outputLines: 40 },
      no: { truncated: false },
    }).map(([k, tr]) => ["trunc-" + k, { path: "a.txt" }, { content: [{ type: "text", text: "a\nb\nc" }], details: { truncation: tr } }]),
    ["trunc-err", { path: "a.txt" }, { content: [{ type: "text", text: "boom" }], details: { truncation: { truncated: true, truncatedBy: "lines", outputLines: 1, totalLines: 2 } } }, { isError: true }],
  ]) C(`read.result:${n}${e}`, res("read", a, r, { ...x, ...(c ?? {}) }));
}
for (const exp of [false, true]) {
  const e = exp ? "/x" : "/c";
  const x = { expanded: exp };
  for (const [n, a, c] of [
    ["ts", { path: "a.ts", content: "const a = 1;\nlet b = 'x';\n" }], ["txt", { path: "a.txt", content: "hello\tworld\r\nsecond\n\n\n" }],
    ["nolang", { path: "data.zzz", content: "all:\n\ttrue" }], ["long-ts", { path: "a.ts", content: wlong }], ["long-txt", { path: "a.txt", content: wlong }],
    ["ten-ts", { path: "a.ts", content: Array.from({ length: 10 }, (_, i) => `let a${i};`).join("\n") }],
    ["eleven-txt", { path: "a.txt", content: Array.from({ length: 11 }, (_, i) => `l${i}`).join("\n") }],
    ["emptycontent", { path: "a.ts", content: "" }], ["nullcontent", { path: "a.ts", content: null }], ["nocontent", { path: "a.ts" }],
    ["numcontent", { path: "a.ts", content: 7 }], ["nopath", { content: "x" }], ["numpath", { path: 3, content: "x" }], ["nullpath", { path: null, file_path: null, content: "x" }],
    ["fp", { file_path: "z.py", content: "def f():\n  return 1\n" }], ["undef", null], ["wide", { path: "a.txt", content: "y".repeat(150) }],
    ["partial", { path: "a.ts", content: "const a = 1;" }, { argsComplete: false, isPartial: true }],
    ["onlynewlines", { path: "a.ts", content: "\n\n" }], ["crlf", { path: "a.ts", content: "let a;\r\nlet b;\r\n" }],
  ]) C(`write.call:${n}${e}`, call("write", a, { ...x, ...(c ?? {}) }));
}
{
  const steps = ["c", "co", "const a = 1;\nlet b", "const a = 1;\nlet b = 2;\n/* open", "const a = 1;\nlet b = 2;\n/* open comment\nstill */ let c = 3;"];
  const pc = { argsComplete: false, isPartial: true };
  C("write.stream", ...steps.map((content) => call("write", { path: "s.ts", content }, pc)), call("write", { path: "s.ts", content: steps.at(-1) }, { isPartial: true }));
  const many = (n) => Array.from({ length: n }, (_, i) => `/* c${i}`).join("\n") + "\n";
  const px = { ...pc, expanded: true };
  C("write.stream.many", ...[20, 55, 60].map((n) => call("write", { path: "m.ts", content: many(n) }, px)), call("write", { path: "m.ts", content: many(60) }, { isPartial: true, expanded: true }),
    call("write", { path: "m.ts", content: "const z = 1;" }, px), call("write", { path: "m.py", content: "const z = 1;" }, px),
    call("write", { path: "m.py", content: "const z = 1;\nx = 2" }, px), call("write", { path: "m", content: "const z = 1;\nx = 2" }, px), call("write", { path: "m.ts", content: null }, px),
    call("write", { path: "m.ts", content: "abc" }, px), call("write", { path: "m.ts", content: "abc" }, px));
  C("write.stream.tabs", call("write", { path: "t.go", content: "func a() {\n\t" }, pc), call("write", { path: "t.go", content: "func a() {\n\treturn\r\n}" }, pc), call("write", { path: "t.go", content: "func a() {\n\treturn\r\n}\n" }, { isPartial: true }));
  C("write.stream.skipcr", call("write", { path: "t.go", content: "a\r" }, pc), call("write", { path: "t.go", content: "a\r\nb" }, pc));
}
for (const [n, r, c] of [
  ["ok", txt("Successfully wrote 1 bytes to a.ts")], ["err", txt("EACCES: permission denied"), { isError: true }], ["errempty", txt(""), { isError: true }],
  ["errmulti", { content: [{ type: "text", text: "a" }, { type: "image", data: "x", mimeType: "image/png" }, { type: "text", text: "b" }] }, { isError: true }],
  ["errlong", txt("e".repeat(130)), { isError: true }], ["partial", txt("w"), { isPartial: true }],
]) C(`write.result:${n}`, res("write", { path: "a.ts", content: "x" }, r, c ?? {}));

const okRes = (diff) => ({ content: [{ type: "text", text: "Successfully replaced 1 block(s) in e.txt." }], details: { diff, firstChangedLine: 2 } });
const dd = "  1 alpha\n-2 beta\n+2 BETA\n  3 gamma";
const E = (name, args, ctx, result) => C(`edit:${name}`, call("edit", args, ctx), wait(), call("edit", args, ctx), ...(result ? [res("edit", args, result, { ...ctx, isError: !!result.isError }), again("call", "edit")] : []));
const eo = [{ oldText: "beta", newText: "BETA" }];
E("ok", { path: "e.txt", edits: eo }, {}, okRes(dd));
E("okdiffdiffers", { path: "e.txt", edits: eo }, {}, okRes("  1 alpha\n-2 beta\n+2 BETAX"));
E("okexpanded", { path: "e.txt", edits: eo }, { expanded: true }, okRes(dd));
E("legacy", { path: "e.txt", oldText: "beta", newText: "BETA" }, {});
E("fp", { file_path: "e.txt", edits: [{ oldText: "gamma", newText: "G" }] }, {});
E("notfound", { path: "e.txt", edits: [{ oldText: "zzz", newText: "G" }] }, {}, { isError: true, content: [{ type: "text", text: "Could not find the exact text in e.txt." }] });
E("errother", { path: "e.txt", edits: [{ oldText: "zzz", newText: "G" }] }, {}, { isError: true, content: [{ type: "text", text: "something else" }] });
E("errempty", { path: "e.txt", edits: eo }, {}, { isError: true, content: [{ type: "text", text: "" }] });
E("missingfile", { path: "nofile.txt", edits: [{ oldText: "a", newText: "b" }] }, {});
E("incomplete", { path: "e.txt", edits: eo }, { argsComplete: false });
E("nopath", { edits: eo }, {});
E("numpath", { path: 5, edits: eo }, {});
E("emptypath", { path: "", edits: eo }, {});
E("badedits", { path: "e.txt", edits: [{ oldText: "beta" }] }, {});
E("emptyedits", { path: "e.txt", edits: [] }, {});
E("undef", null, {});
E("multi", { path: "e.txt", edits: [{ oldText: "alpha", newText: "A" }, { oldText: "delta", newText: "D" }] }, { expanded: true }, okRes("x"));
E("errnopreview", { path: "e.txt", edits: eo }, { argsComplete: false }, { isError: true, content: [{ type: "text", text: "late failure" }] });
E("okresultonly", { path: "nofile.txt", edits: [{ oldText: "a", newText: "b" }] }, {}, okRes("  1 x\n-2 a\n+2 b"));
E("errlong", { path: "e.txt", edits: [{ oldText: "zzz", newText: "G" }] }, {}, { isError: true, content: [{ type: "text", text: "w ".repeat(60) }] });

fs.mkdirSync(CWD, { recursive: true });
fs.writeFileSync(`${CWD}/e.txt`, "alpha\nbeta\ngamma\ndelta\n");
for (const c of cases) {
  const state = {};
  const last = {};
  let invalidated = 0;
  const lines = [];
  for (const raw of c.steps) {
    const s = sub(raw);
    const mk = (tool) => ({ args: s.args ?? undefined, toolCallId: "c", invalidate() { invalidated++; }, state, cwd: CWD, executionStarted: true, argsComplete: true, isPartial: false, expanded: false, showImages: true, isError: false, ...s.ctx });
    if (s.op === "wait") { await sleep(150); lines.push(null); continue; }
    if (s.op === "call") { last[`call:${s.tool}`] = renderers[s.tool].renderCall(s.args ?? undefined, theme, { ...mk(), lastComponent: last[`call:${s.tool}`] }); }
    if (s.op === "result") {
      const ctx = { ...mk(), lastComponent: last[`result:${s.tool}`] };
      last[`result:${s.tool}`] = renderers[s.tool].renderResult(s.result, { expanded: !!s.ctx.expanded, isPartial: !!s.ctx.isPartial }, theme, ctx);
      lines.push(last[`result:${s.tool}`].render(W)); continue;
    }
    const comp = s.op === "render" ? last[`${s.of}:${s.tool}`] : last[`call:${s.tool}`];
    lines.push(comp.render(W));
  }
  c.lines = lines;
}
const json = JSON.stringify({ width: W, cases: cases.map((c) => ({ name: c.name, steps: c.steps, lines: c.lines })) }, null, 1);
fs.writeSync(1, unsub(json) + "\n");
process.exit(0);
