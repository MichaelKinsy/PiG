// Runs a CLI in print mode against a local endpoint of one API kind. The endpoint answers the first request with reasoning and a call of the
// read tool and the second with text, so the second request replays an assistant turn. Prints the member names of each request body in wire order.
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, writeFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const [api, side] = process.argv.slice(2);
const root = await mkdtemp(join(tmpdir(), "provider-print-order-"));
const binary = side === "pig" ? (process.env.PIG_PARITY_PIG_BIN || resolve("bin/pig")) : process.execPath;
const prefix = side === "pig" ? [] : [join(process.env.PI_PACKAGE_ROOT, "dist/cli.js")];
// Strings are elided except the discriminators, so the output is the order of the members and the kind of each item.
const shape = (value, key) => {
  if (Array.isArray(value)) return "[" + value.map((item) => shape(item)).join(",") + "]";
  if (value && typeof value === "object") return "{" + Object.entries(value).map(([name, member]) => name + ":" + shape(member, name)).join(",") + "}";
  if (typeof value === "string") return key === "type" || key === "role" ? value : "s";
  return value === null ? "null" : typeof value;
};
const call = JSON.stringify({ path: "note.txt" });
// Each kind: the model entry, and the events of the turn that calls the tool and of the turn that ends the run.
const kinds = {
  "openai-completions": {
    model: { id: "m1", reasoning: true, input: ["text"], compat: { thinkingFormat: "zai" } },
    events: (first) => {
      const chunk = (delta, finish = null) => ({ data: { id: "c1", object: "chat.completion.chunk", created: 1, model: "m1", choices: [{ index: 0, delta, finish_reason: finish }] } });
      const usage = { data: { id: "c1", object: "chat.completion.chunk", created: 1, model: "m1", choices: [], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } } };
      return first
        ? [chunk({ role: "assistant", content: null, reasoning_content: "plan" }), chunk({ tool_calls: [{ index: 0, id: "call_1", type: "function", function: { name: "read", arguments: call } }] }), chunk({}, "tool_calls"), usage, { data: "[DONE]" }]
        : [chunk({ role: "assistant", content: "ok" }), chunk({}, "stop"), usage, { data: "[DONE]" }];
    },
  },
  "openai-responses": {
    model: { id: "m1", reasoning: true, input: ["text"] },
    events: (first) => {
      let sequence = 0;
      const event = (type, members) => ({ event: type, data: { type, sequence_number: sequence++, ...members } });
      const response = (status, output) => ({ id: "resp_1", object: "response", created_at: 1, status, model: "m1", output,
        usage: { input_tokens: 1, input_tokens_details: { cached_tokens: 0 }, output_tokens: 1, output_tokens_details: { reasoning_tokens: 0 }, total_tokens: 2 } });
      // The provider's own member order: id before type, the encrypted content before the summary.
      const reasoning = { id: "rs_1", type: "reasoning", encrypted_content: "enc", summary: [] };
      const fn = { id: "fc_1", type: "function_call", status: "completed", arguments: call, call_id: "call_1", name: "read" };
      const message = { id: "msg_1", type: "message", status: "completed", content: [{ type: "output_text", annotations: [], text: "ok" }], role: "assistant" };
      return first
        ? [event("response.created", { response: response("in_progress", []) }),
          event("response.output_item.added", { output_index: 0, item: { id: "rs_1", type: "reasoning", summary: [] } }),
          event("response.output_item.done", { output_index: 0, item: reasoning }),
          event("response.output_item.added", { output_index: 1, item: { ...fn, status: "in_progress", arguments: "" } }),
          event("response.function_call_arguments.delta", { item_id: "fc_1", output_index: 1, delta: call }),
          event("response.function_call_arguments.done", { item_id: "fc_1", output_index: 1, arguments: call }),
          event("response.output_item.done", { output_index: 1, item: fn }),
          event("response.completed", { response: response("completed", [reasoning, fn]) })]
        : [event("response.created", { response: response("in_progress", []) }),
          event("response.output_item.added", { output_index: 0, item: { ...message, status: "in_progress", content: [] } }),
          event("response.content_part.added", { item_id: "msg_1", output_index: 0, content_index: 0, part: { type: "output_text", annotations: [], text: "" } }),
          event("response.output_text.delta", { item_id: "msg_1", output_index: 0, content_index: 0, delta: "ok" }),
          event("response.output_text.done", { item_id: "msg_1", output_index: 0, content_index: 0, text: "ok" }),
          event("response.content_part.done", { item_id: "msg_1", output_index: 0, content_index: 0, part: message.content[0] }),
          event("response.output_item.done", { output_index: 0, item: message }),
          event("response.completed", { response: response("completed", [message]) })];
    },
  },
};
// Azure serves the Responses events under its own path; the run asks for long cache retention, which azure-openai-responses does not send.
kinds["azure-openai-responses"] = { model: { id: "m1", reasoning: true, input: ["text"], compat: { supportsLongCacheRetention: true } }, events: kinds["openai-responses"].events,
  env: { PI_CACHE_RETENTION: "long", AZURE_OPENAI_API_VERSION: "v1" } };
const kind = kinds[api];
if (!kind) throw new Error(`no endpoint for ${api}`);
try {
  const bodies = [];
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    bodies.push(JSON.parse(Buffer.concat(chunks)));
    res.writeHead(200, { "content-type": "text/event-stream" });
    for (const { event, data } of kind.events(bodies.length === 1)) {
      res.write((event ? `event: ${event}\n` : "") + `data: ${typeof data === "string" ? data : JSON.stringify(data)}\n\n`);
    }
    res.end();
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const agentDir = join(root, "agent");
    const cwd = join(root, "work");
    await mkdir(agentDir);
    await mkdir(cwd);
    await writeFile(join(cwd, "note.txt"), "a note\n");
    await writeFile(join(agentDir, "models.json"), JSON.stringify({ providers: { local: { api, baseUrl: `http://127.0.0.1:${server.address().port}/v1`, apiKey: "test-key", models: [kind.model] } } }));
    await new Promise((resolve, reject) => {
      const child = spawn(binary, [...prefix, "--no-extensions", "--offline", "--no-session", "--model", "local/m1", "--thinking", "high", "-p", "read note.txt"],
        { cwd, env: { ...process.env, PIG_CODING_AGENT_DIR: agentDir, PI_CODING_AGENT_DIR: agentDir, PI_SKIP_VERSION_CHECK: "1", ...kind.env } });
      let stdout = "", stderr = "";
      child.stdout.on("data", (data) => { stdout += data; });
      child.stderr.on("data", (data) => { stderr += data; });
      child.stdin.end();
      child.on("error", reject);
      child.on("exit", (code) => code === 0 ? resolve() : reject(new Error(`CLI exited ${code}: ${stderr}\n${stdout}`)));
    });
    if (bodies.length !== 2) throw new Error(`expected two requests, got ${bodies.length}`);
    for (const body of bodies) console.log(shape(body));
  } finally {
    server.closeAllConnections();
    await new Promise((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
  }
} finally {
  await rm(root, { recursive: true, force: true });
}
