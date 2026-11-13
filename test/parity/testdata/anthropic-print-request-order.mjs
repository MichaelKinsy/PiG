// Runs a CLI in print mode against a local Anthropic Messages endpoint, once with a text prompt and once with an attached image, and
// prints the member names of each request body in wire order.
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, writeFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const side = process.argv[2];
const root = await mkdtemp(join(tmpdir(), "anthropic-print-order-"));
const binary = side === "pig" ? (process.env.PIG_PARITY_PIG_BIN || resolve("bin/pig")) : process.execPath;
const prefix = side === "pig" ? [] : [join(process.env.PI_PACKAGE_ROOT, "dist/cli.js")];
// Strings are elided except the discriminators, so the output is the order of the members and the kind of each block.
const shape = (value, key) => {
  if (Array.isArray(value)) return "[" + value.map((item) => shape(item)).join(",") + "]";
  if (value && typeof value === "object") return "{" + Object.entries(value).map(([name, member]) => name + ":" + shape(member, name)).join(",") + "}";
  if (typeof value === "string") return key === "type" || key === "role" ? value : "s";
  return typeof value;
};
const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
try {
  const bodies = [];
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    bodies.push(JSON.parse(Buffer.concat(chunks)));
    res.writeHead(200, { "content-type": "text/event-stream" });
    const emit = (value) => res.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`);
    emit({ type: "message_start", message: { id: "msg_1", type: "message", role: "assistant", model: "m1", content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 1, output_tokens: 1 } } });
    emit({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
    emit({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "ok" } });
    emit({ type: "content_block_stop", index: 0 });
    emit({ type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 1 } });
    emit({ type: "message_stop" });
    res.end();
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  try {
    const agentDir = join(root, "agent");
    const cwd = join(root, "work");
    await mkdir(agentDir);
    await mkdir(cwd);
    await writeFile(join(cwd, "pixel.png"), Buffer.from(pixel, "base64"));
    await writeFile(join(agentDir, "models.json"), JSON.stringify({ providers: { local: { api: "anthropic-messages", baseUrl: `http://127.0.0.1:${server.address().port}`, apiKey: "test-key",
      models: [{ id: "m1", reasoning: false, input: ["text", "image"] }] } } }));
    for (const prompt of [["hello"], ["@pixel.png", "look"]]) {
      await new Promise((resolve, reject) => {
        const child = spawn(binary, [...prefix, "--no-extensions", "--offline", "--no-session", "--model", "local/m1", "-p", ...prompt],
          { cwd, env: { ...process.env, PIG_CODING_AGENT_DIR: agentDir, PI_CODING_AGENT_DIR: agentDir, PI_SKIP_VERSION_CHECK: "1" } });
        let stdout = "", stderr = "";
        child.stdout.on("data", (data) => { stdout += data; });
        child.stderr.on("data", (data) => { stderr += data; });
        child.stdin.end();
        child.on("error", reject);
        child.on("exit", (code) => code === 0 ? resolve() : reject(new Error(`${prompt.join(" ")}: CLI exited ${code}: ${stderr}\n${stdout}`)));
      });
    }
    if (bodies.length !== 2) throw new Error(`expected one request per prompt, got ${bodies.length}`);
    for (const body of bodies) console.log(shape(body));
  } finally {
    server.closeAllConnections();
    await new Promise((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
  }
} finally {
  await rm(root, { recursive: true, force: true });
}
