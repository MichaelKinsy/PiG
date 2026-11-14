// Runs a CLI in RPC mode on JSON from outside that holds a lone UTF-16 surrogate, against a local endpoint, and prints the marked strings of
// each place it then writes: the next request, the RPC lines, the session file, settings.json. JSON.parse keeps such a unit, and so does every
// read of this harness: a provider's stream (a character split over two deltas is one character again; a unit that stays alone is kept and
// removed from the next request's text), a session Pi wrote (the unit in a user message, an assistant text, a tool-call argument, a tool
// result, a custom entry and message), models.json (a model's id, name and compat) and settings.json (rewritten after a setter). A byte
// outside printable ASCII prints as <hex>: U+FFFD is <ef><bf><bd>, and the escape JSON.stringify writes for a lone surrogate is itself.
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const side = process.argv[2];
const here = join(dirname(fileURLToPath(import.meta.url)), "lone-surrogate-read");
const U = "\ud83d", LOW = "\ude00";
const command = (cli) => cli === "pig" ? [process.env.PIG_PARITY_PIG_BIN || resolve("bin/pig")] : [process.execPath, join(process.env.PI_PACKAGE_ROOT, "dist/cli.js")];
const show = (buffer) => Array.from(buffer, (b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : "<" + b.toString(16).padStart(2, "0") + ">")).join("");
const marks = (text) => (text.match(/\b[A-Z][A-Z0-9]\[[^\]]{0,40}\]/g) ?? []).join(" ") || "none";
const walk = async (dir) => (await Promise.all((await readdir(dir, { withFileTypes: true })).map((entry) => entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)]))).flat();

const refuse = (res) => {
  res.writeHead(400, { "content-type": "application/json" });
  res.end('{"type":"error","error":{"type":"invalid_request_error","message":"captured","code":400,"status":"INVALID_ARGUMENT"}}');
};
// The deltas of the reply's text and of its tool arguments: a character split over two deltas, then a unit that stays alone.
const textDeltas = [`AT[${U}`, `${LOW}] AL[${U}`, "]"];
const argumentDeltas = [`{"p":"AR[${U}`, `${LOW}] AQ[${U}`, `]"}`];
const loops = {
  "anthropic-messages": (res, first) => {
    res.writeHead(200, { "content-type": "text/event-stream" });
    const emit = (value) => res.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`);
    emit({ type: "message_start", message: { id: "msg_1", type: "message", role: "assistant", model: "m1", content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 1, output_tokens: 1 } } });
    emit({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
    for (const text of first ? textDeltas : ["done"]) emit({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text } });
    emit({ type: "content_block_stop", index: 0 });
    if (first) {
      emit({ type: "content_block_start", index: 1, content_block: { type: "tool_use", id: "toolu_1", name: "echo", input: {} } });
      for (const partial_json of argumentDeltas) emit({ type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json } });
      emit({ type: "content_block_stop", index: 1 });
    }
    emit({ type: "message_delta", delta: { stop_reason: first ? "tool_use" : "end_turn", stop_sequence: null }, usage: { output_tokens: 1 } });
    emit({ type: "message_stop" });
    res.end();
  },
  "openai-completions": (res, first) => {
    res.writeHead(200, { "content-type": "text/event-stream" });
    const emit = (delta, finish) => res.write(`data: ${JSON.stringify({ id: "c1", object: "chat.completion.chunk", created: 1, model: "m1", choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`);
    for (const content of first ? textDeltas : ["done"]) emit({ role: "assistant", content }, null);
    if (first) argumentDeltas.forEach((part, i) => emit({ tool_calls: [{ index: 0, ...(i === 0 ? { id: "call_1", type: "function" } : {}), function: { ...(i === 0 ? { name: "echo" } : {}), arguments: part } }] }, null));
    emit({}, first ? "tool_calls" : "stop");
    res.write("data: [DONE]\n\n");
    res.end();
  },
};

// run starts a CLI against a local endpoint of api, sends the RPC lines one after another (each when the text that ends the one before
// appeared) and returns the request bodies, the RPC output, the session file and settings.json.
const run = async (cli, api, { extension, reply = refuse, seed, models = [], settings, commands, model = "local/m1" }) => {
  const root = await mkdtemp(join(tmpdir(), "lone-read-"));
  const bodies = [];
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    bodies.push(Buffer.concat(chunks));
    reply(res, bodies.length === 1);
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  try {
    const agentDir = join(root, "agent"), cwd = join(root, "work"), home = join(root, "home");
    for (const dir of [agentDir, cwd, home]) await mkdir(dir);
    const suffix = api === "anthropic-messages" ? "" : api === "google-generative-ai" ? "/v1beta" : "/v1";
    const base = `http://127.0.0.1:${server.address().port}${suffix}`;
    await writeFile(join(agentDir, "models.json"), JSON.stringify({ providers: { local: { api, baseUrl: base, apiKey: "test-key",
      models: [{ id: "m1", reasoning: false, input: ["text"] }, ...models] } } }));
    if (settings) await writeFile(join(agentDir, "settings.json"), JSON.stringify(settings, null, 2));
    const opened = [];
    if (seed) {
      await writeFile(join(root, "seed.jsonl"), seed.text.replaceAll(seed.root, root));
      opened.push("--session", join(root, "seed.jsonl"));
    }
    const [binary, ...prefix] = command(cli);
    let stdout = Buffer.alloc(0), stderr = "";
    await new Promise((done, fail) => {
      const child = spawn(binary, [...prefix, "--no-extensions", ...(extension ? ["-e", join(here, extension)] : []), "--offline", "--model", model, ...opened, "--mode", "rpc"],
        { cwd, env: { ...process.env, HOME: home, PIG_HOME: home, PI_HOME: home, PIG_CODING_AGENT_DIR: agentDir, PI_CODING_AGENT_DIR: agentDir, PI_SKIP_VERSION_CHECK: "1", PROBE_BASE: base } });
      const timer = setTimeout(() => child.kill("SIGKILL"), 60000);
      let next = 0, from = 0;
      const step = () => {
        if (next > commands.length) return;
        if (next > 0) {
          const at = stdout.indexOf(commands[next - 1].done, from);
          if (at < 0) return;
          from = at + 1;
        }
        if (next === commands.length) { next++; setTimeout(() => child.stdin.end(), 300); return; }
        child.stdin.write(commands[next++].line + "\n");
      };
      child.stdout.on("data", (data) => { stdout = Buffer.concat([stdout, data]); step(); });
      child.stderr.on("data", (data) => { stderr += data; });
      child.on("error", fail);
      child.on("exit", (code) => { clearTimeout(timer); code === 0 ? done() : fail(new Error(`${cli} ${api}: CLI exited ${code}: ${stderr.slice(-600)}\n${show(stdout).slice(-600)}`)); });
      step();
    });
    const sessions = (await walk(root)).filter((name) => name.endsWith(".jsonl") && !name.endsWith("seed.jsonl"));
    const file = seed ? await readFile(join(root, "seed.jsonl")) : sessions.length === 1 ? await readFile(sessions[0]) : Buffer.from(`sessions: ${sessions.length}`);
    const settingsFile = await readFile(join(agentDir, "settings.json")).catch(() => Buffer.from("absent"));
    return { bodies, lines: show(stdout).split("<0a>"), session: show(file).split("<0a>").filter(Boolean), written: { text: file.toString("utf8"), root }, settingsFile: show(settingsFile), stderr,
      seedLines: seed ? show(Buffer.from(seed.text.replaceAll(seed.root, root))).split("<0a>").filter(Boolean) : [] };
  } finally {
    server.closeAllConnections();
    await new Promise((done) => server.close(done));
    await rm(root, { recursive: true, force: true });
  }
};

const prompt = (id, text) => ({ line: `{"id":"${id}","type":"prompt","message":"${text}"}`, done: '"type":"agent_end"' });
const call = (id, rest) => ({ line: `{"id":"${id}",${rest}}`, done: `"id":"${id}"` });
const kindOf = (line) => { const m = line.match(/"type":"([a-z_]+)"/); const r = line.match(/"role":"([a-zA-Z]+)"/); return (m ? m[1] : "?") + (r ? "/" + r[1] : ""); };
const sessionMarks = (session) => session.map((line) => `${kindOf(line)}: ${marks(line)}`).filter((s) => !s.endsWith(": none")).join(" | ") || "none";
const rpcMarks = (lines, test) => marks(lines.filter(test).join(" "));

const stage = async (label, body) => {
  try { await body((name, value) => console.log(`${label}: ${name}: ${value}`)); }
  catch (error) { console.log(`${label}: ERROR: ${String(error).slice(0, 900)}`); process.exitCode = 1; }
};

// The stream of two API kinds, read by this side's CLI: the prompt holds a lone unit, the reply's text and tool arguments a split character and a lone unit.
for (const api of ["anthropic-messages", "openai-completions"]) {
  await stage(`stream ${api}`, async (put) => {
    const result = await run(side, api, { extension: "tool.ts", reply: loops[api], commands: [prompt("1", "UM[\\ud83d]"), call("2", '"type":"set_session_name","name":"RN[\\ud83d]"'), call("3", '"type":"get_messages"')] });
    put("requests", String(result.bodies.length));
    if (result.bodies.length > 1) put("request 2", marks(show(result.bodies[1])));
    put("rpc message_end", rpcMarks(result.lines, (line) => line.startsWith('{"type":"message_end"')));
    put("rpc get_messages", rpcMarks(result.lines, (line) => line.includes('"id":"3"')));
    put("session", sessionMarks(result.session));
  });
}
// A session Pi wrote: Pi runs the loop once with an extension whose tool result, entries, message and event results hold the unit, and this
// side's CLI opens that file, sends the next request of each API kind and of a provider an extension registers, and appends to the file.
const seed = (await run("pi", "anthropic-messages", { extension: "seed.ts", reply: loops["anthropic-messages"], commands: [prompt("1", "UM[\\ud83d]")] })).written;
for (const api of ["anthropic-messages", "openai-completions", "openai-responses", "google-generative-ai", "extension provider"]) {
  await stage(`session Pi wrote, ${api}`, async (put) => {
    const ext = api === "extension provider";
    const result = await run(side, ext ? "openai-completions" : api, { seed, extension: ext ? "provider.ts" : undefined, model: ext ? "extp/xm" : "local/m1", commands: [prompt("1", "NX[\\ud83d]")] });
    put("requests", String(result.bodies.length));
    if (result.bodies.length) put("request 1", marks(show(result.bodies[0])));
    const kept = result.seedLines.filter((line, i) => result.session[i] === line).length;
    put("lines Pi wrote, kept byte for byte", `${kept} of ${result.seedLines.length}`);
    put("appended", sessionMarks(result.session.slice(result.seedLines.length)));
  });
}
await stage("models.json", async (put) => {
  const result = await run(side, "openai-completions", { models: [{ id: `MX[${U}]`, name: `NM[${U}]`, reasoning: false, input: ["text"], compat: { openRouterRouting: { sort: `RS[${U}]` } } }],
    commands: [call("1", '"type":"set_model","provider":"local","modelId":"MX[\\ud83d]"'), prompt("2", "go"), call("3", '"type":"get_available_models"'), call("4", '"type":"get_state"')] });
  put("requests", String(result.bodies.length));
  if (result.bodies.length) put("request 1", marks(show(result.bodies[0])));
  for (const id of ["1", "3", "4"]) put(`rpc response ${id}`, rpcMarks(result.lines, (line) => line.includes(`"id":"${id}"`)));
  put("session", sessionMarks(result.session));
});
await stage("settings.json", async (put) => {
  const result = await run(side, "openai-completions", { settings: { shellCommandPrefix: `CP[${U}]`, zz: { k: `UK[${U}]`, [`KN[${U}]`]: 1 }, doubleEscapeAction: `DE[${U}]`, npmCommand: [`NC[${U}]`] },
    commands: [call("1", '"type":"set_auto_compaction","enabled":false'), call("2", '"type":"set_steering_mode","mode":"all"'), call("3", '"type":"get_state"')] });
  put("after two setters", result.settingsFile);
});
