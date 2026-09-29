import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, writeFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const side = process.argv[2];
const root = await mkdtemp(join(tmpdir(), "custom-thinking-"));
const binary = side === "pig" ? (process.env.PIG_PARITY_PIG_BIN || resolve("bin/pig")) : process.execPath;
const prefix = side === "pig" ? [] : [join(process.env.PI_PACKAGE_ROOT, "dist/cli.js")];
const observations = [];
try {
  for (const api of ["openai-completions", "openai-responses"]) {
    let request;
    const server = createServer(async (req, res) => {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      request = JSON.parse(Buffer.concat(chunks));
      res.writeHead(200, { "content-type": "text/event-stream" });
      const emit = (value) => res.write(`data: ${JSON.stringify(value)}\n\n`);
      if (api === "openai-completions") {
        emit({ id: "completion", object: "chat.completion.chunk", choices: [{ index: 0, delta: { role: "assistant", content: "ok" }, finish_reason: null }] });
        emit({ id: "completion", object: "chat.completion.chunk", choices: [{ index: 0, delta: {}, finish_reason: "stop" }] });
        res.end("data: [DONE]\n\n");
      } else {
        const item = { id: "message", type: "message", role: "assistant", status: "completed", content: [{ type: "output_text", text: "ok", annotations: [] }] };
        emit({ type: "response.created", response: { id: "response", status: "in_progress", output: [] } });
        emit({ type: "response.output_item.added", output_index: 0, item: { ...item, status: "in_progress", content: [] } });
        emit({ type: "response.content_part.added", output_index: 0, content_index: 0, part: { type: "output_text", text: "", annotations: [] } });
        emit({ type: "response.output_text.delta", output_index: 0, content_index: 0, delta: "ok" });
        emit({ type: "response.output_text.done", output_index: 0, content_index: 0, text: "ok" });
        emit({ type: "response.output_item.done", output_index: 0, item });
        emit({ type: "response.completed", response: { id: "response", status: "completed", output: [item], usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 } } });
        res.end();
      }
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    try {
      const agentDir = join(root, api);
      await mkdir(agentDir);
      await writeFile(join(agentDir, "models.json"), JSON.stringify({ providers: { neuralwatt: { api, baseUrl: `http://127.0.0.1:${server.address().port}/v1`, apiKey: "test-key", compat: { supportsReasoningEffort: true }, models: [{ id: "some-base-model", reasoning: false }] } } }));
      const output = await new Promise((resolve, reject) => {
        const child = spawn(binary, [...prefix, "--no-extensions", "--offline", "--no-session", "--no-tools", "--mode", "json", "--model", "neuralwatt/zai-org/GLM-5.1-FP8:high", "hello"], { cwd: root, env: { ...process.env, PIG_CODING_AGENT_DIR: agentDir, PI_CODING_AGENT_DIR: agentDir, PI_SKIP_VERSION_CHECK: "1" } });
        let stdout = "", stderr = "";
        child.stdout.on("data", (data) => { stdout += data; });
        child.stderr.on("data", (data) => { stderr += data; });
        child.stdin.end();
        child.on("error", reject);
        child.on("exit", (code) => code === 0 ? resolve(stdout) : reject(new Error(`${api}: CLI exited ${code}: ${stderr}\n${stdout}`)));
      });
      const messages = output.trim().split("\n").map((line) => JSON.parse(line)).filter((event) => event.type === "message_end").map((event) => event.message);
      assert.ok(messages.some((message) => message?.role === "assistant" && message.stopReason === "stop" && message.content.some((part) => part.type === "text" && part.text === "ok")), "CLI must finish the provider response with the exact assistant text");
      const observation = { api, model: request?.model, reasoning: api === "openai-completions" ? request?.reasoning_effort : request?.reasoning?.effort };
      assert.deepEqual(observation, { api, model: "zai-org/GLM-5.1-FP8", reasoning: "high" });
      observations.push(observation);
    } finally {
      server.closeAllConnections();
      await new Promise((resolve, reject) => server.close((err) => err ? reject(err) : resolve()));
    }
  }
  console.log(JSON.stringify(observations));
} finally {
  await rm(root, { recursive: true, force: true });
}
