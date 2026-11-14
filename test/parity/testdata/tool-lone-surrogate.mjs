// Runs a CLI in RPC mode with an extension whose tools hold lone UTF-16 surrogates, against a local endpoint of each API kind, and prints the
// marked strings as they are in the request body, in the RPC lines and in the session file. Then the whole loop for two API kinds: two tools
// whose names differ only in the lone unit, the endpoint answers with a call of one of them, and the output says which tool ran and how the
// second request, the RPC lines and the session file name it. Last, a session Pi wrote in that loop is opened and the next request of each
// API kind is built from it. A byte outside printable ASCII prints as <hex>, so U+FFFD is <ef><bf><bd> and the escape JSON.stringify writes
// for a lone surrogate is itself.
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const side = process.argv[2];
const here = join(dirname(fileURLToPath(import.meta.url)), "tool-lone-surrogate");
const command = (cli) => cli === "pig" ? [process.env.PIG_PARITY_PIG_BIN || resolve("bin/pig")] : [process.execPath, join(process.env.PI_PACKAGE_ROOT, "dist/cli.js")];
const show = (buffer) => Array.from(buffer, (b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : "<" + b.toString(16).padStart(2, "0") + ">")).join("");
// The marked strings of a text in order, each once when unique is set.
const marks = (text, unique) => {
  const found = text.match(/\b(?:N_[a-z]+|N|PD|DF|D|E|K)\[[^\]]*\]|ran (?:high|low)|Tool [^"\\]{0,80}not found/g) ?? [];
  return (unique ? [...new Set(found)] : found).join(" ") || "none";
};
const walk = async (dir) => (await Promise.all((await readdir(dir, { withFileTypes: true })).map((entry) => entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)]))).flat();
const called = "N[\ude00]";

const refuse = (res) => {
  res.writeHead(400, { "content-type": "application/json" });
  res.end('{"type":"error","error":{"type":"invalid_request_error","message":"captured","code":400,"status":"INVALID_ARGUMENT"}}');
};
// The replies of the loop: the first request is answered with a call of the tool, the second with text.
const loops = {
  "anthropic-messages": (res, first) => {
    res.writeHead(200, { "content-type": "text/event-stream" });
    const emit = (value) => res.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`);
    emit({ type: "message_start", message: { id: "msg_1", type: "message", role: "assistant", model: "m1", content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 1, output_tokens: 1 } } });
    if (first) {
      emit({ type: "content_block_start", index: 0, content_block: { type: "tool_use", id: "toolu_1", name: called, input: {} } });
      emit({ type: "content_block_delta", index: 0, delta: { type: "input_json_delta", partial_json: "{}" } });
    } else {
      emit({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
      emit({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "done" } });
    }
    emit({ type: "content_block_stop", index: 0 });
    emit({ type: "message_delta", delta: { stop_reason: first ? "tool_use" : "end_turn", stop_sequence: null }, usage: { output_tokens: 1 } });
    emit({ type: "message_stop" });
    res.end();
  },
  "openai-completions": (res, first) => {
    res.writeHead(200, { "content-type": "text/event-stream" });
    const emit = (delta, finish) => res.write(`data: ${JSON.stringify({ id: "c1", object: "chat.completion.chunk", created: 1, model: "m1", choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`);
    if (first) emit({ role: "assistant", tool_calls: [{ index: 0, id: "call_1", type: "function", function: { name: called, arguments: "{}" } }] }, null);
    else emit({ role: "assistant", content: "done" }, null);
    emit({}, first ? "tool_calls" : "stop");
    res.write("data: [DONE]\n\n");
    res.end();
  },
};

// run starts a CLI (this side's unless cli names one) with one extension against a local endpoint of api, sends one prompt and returns the
// request bodies, the RPC output and the session file. With seed, the CLI opens that session, its paths moved to this run's directory.
const run = async (api, extension, reply, { cli = side, seed } = {}) => {
  const root = await mkdtemp(join(tmpdir(), "tool-lone-surrogate-"));
  const bodies = [];
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    bodies.push(Buffer.concat(chunks));
    reply(res, bodies.length === 1);
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  try {
    const agentDir = join(root, "agent");
    const cwd = join(root, "work");
    await mkdir(agentDir);
    await mkdir(cwd);
    const suffix = api === "anthropic-messages" ? "" : api === "google-generative-ai" ? "/v1beta" : "/v1";
    await writeFile(join(agentDir, "models.json"), JSON.stringify({ providers: { local: { api, baseUrl: `http://127.0.0.1:${server.address().port}${suffix}`, apiKey: "test-key",
      models: [{ id: "m1", reasoning: false, input: ["text"] }] } } }));
    const opened = [];
    if (seed) {
      await writeFile(join(root, "seed.jsonl"), seed.text.replaceAll(seed.root, root));
      opened.push("--session", join(root, "seed.jsonl"));
    }
    const [binary, ...prefix] = command(cli);
    let stdout = Buffer.alloc(0), stderr = "";
    await new Promise((done, fail) => {
      const child = spawn(binary, [...prefix, "--no-extensions", "-e", join(here, extension), "--offline", "--model", "local/m1", ...opened, "--mode", "rpc"],
        { cwd, env: { ...process.env, PIG_CODING_AGENT_DIR: agentDir, PI_CODING_AGENT_DIR: agentDir, PI_SKIP_VERSION_CHECK: "1" } });
      const timer = setTimeout(() => child.kill("SIGKILL"), 60000);
      let closed = false;
      child.stdout.on("data", (data) => {
        stdout = Buffer.concat([stdout, data]);
        if (!closed && stdout.includes('"type":"agent_end"')) { closed = true; setTimeout(() => child.stdin.end(), 300); }
      });
      child.stderr.on("data", (data) => { stderr += data; });
      child.on("error", fail);
      child.on("exit", (code) => { clearTimeout(timer); code === 0 ? done() : fail(new Error(`${api} ${extension}: CLI exited ${code}: ${stderr}\n${stdout}`)); });
      child.stdin.write('{"id":"1","type":"prompt","message":"go"}\n');
    });
    const sessions = (await walk(root)).filter((name) => name.endsWith(".jsonl"));
    if (sessions.length !== 1) throw new Error(`${api} ${extension}: expected one session file, got ${sessions.length}`);
    const file = await readFile(sessions[0]);
    return { bodies, lines: show(stdout).split("<0a>"), session: show(file).split("<0a>"), written: { text: file.toString("utf8"), root } };
  } finally {
    server.closeAllConnections();
    await new Promise((done) => server.close(done));
    await rm(root, { recursive: true, force: true });
  }
};

const apis = ["anthropic-messages", "openai-completions", "openai-responses", "google-generative-ai"];
for (const api of apis) {
  const { bodies, lines, session } = await run(api, "declarations.ts", refuse);
  if (bodies.length !== 1) throw new Error(`${api}: expected one request, got ${bodies.length}`);
  const system = (line) => line.includes('"role":"system"');
  console.log(`${api} declarations`);
  console.log(`  wire, ${bodies[0].filter((b) => b >= 0x7f).length} bytes outside ASCII: ${marks(show(bodies[0]), true)}`);
  console.log(`  rpc system message: ${marks(lines.filter(system).join(" "), true)}`);
  console.log(`  session system message: ${marks(session.filter(system).join(" "), true)}`);
}
for (const api of ["anthropic-messages", "openai-completions"]) {
  const { bodies, lines, session } = await run(api, "twins.ts", loops[api]);
  if (bodies.length !== 2) throw new Error(`${api}: expected two requests, got ${bodies.length}`);
  const first = show(bodies[0]), second = show(bodies[1]);
  const kind = (name) => lines.filter((line) => line.startsWith(`{"type":"${name}"`)).join(" ");
  console.log(`${api} loop`);
  console.log(`  request 1 tools: ${marks(first.slice(first.indexOf('"tools"')))}`);
  console.log(`  request 2 messages: ${marks(second.slice(second.indexOf('"messages"'), second.indexOf('"tools"') > second.indexOf('"messages"') ? second.indexOf('"tools"') : undefined))}`);
  console.log(`  rpc tool_execution_start: ${marks(kind("tool_execution_start"))}`);
  console.log(`  rpc tool_execution_end: ${marks(kind("tool_execution_end"))}`);
  console.log(`  session assistant and toolResult: ${marks(session.filter((line) => line.includes('"role":"assistant"') || line.includes('"role":"toolResult"')).join(" "))}`);
}
// A session Pi wrote: Pi runs the loop once, and this side's CLI opens that file for the next request of each API kind.
const { written } = await run("anthropic-messages", "twins.ts", loops["anthropic-messages"], { cli: "pi" });
for (const api of apis) {
  const { bodies } = await run(api, "twins.ts", refuse, { seed: written });
  if (bodies.length !== 1) throw new Error(`${api} replay: expected one request, got ${bodies.length}`);
  console.log(`${api} replay of a session Pi wrote, ${bodies[0].filter((b) => b >= 0x7f).length} bytes outside ASCII: ${marks(show(bodies[0]))}`);
}
