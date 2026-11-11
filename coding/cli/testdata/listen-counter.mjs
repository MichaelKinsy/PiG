import { appendFileSync } from "node:fs";
import { randomBytes } from "node:crypto";

// The Node twin of listen-counter-go: every tool answers what the Go one answers. Tool names carry the extension name so both register in one session. pig additive (D232).
export default function (pi) {
  const name = "node";
  const instance = randomBytes(8).toString("hex");
  let calls = 0;
  const heard = [];
  const text = (value) => ({ content: [{ type: "text", text: value }], details: {} });
  const tool = (suffix, description, execute) => pi.registerTool({ name: `${name}_${suffix}`, label: suffix, description, parameters: { type: "object", properties: {} }, execute });
  pi.events.on("listen-counter", (data) => { heard.push(data.from); });
  tool("counter", "this instance's call count", async () => text(String(++calls)));
  tool("ping", "emit on the event bus", async () => { pi.events.emit("listen-counter", { from: name }); return text("pinged"); });
  tool("received", "what this instance heard", async () => text("received=" + heard.join(",")));
  tool("slow", "wait until the host cancels the call", (_id, _params, signal) => new Promise((resolve) => {
    const timer = setInterval(() => {}, 1000);
    const done = () => { clearInterval(timer); resolve(text("cancelled")); };
    if (signal?.aborted) done(); else signal?.addEventListener("abort", done, { once: true });
  }));
  tool("confirm", "ask the user and log the answer", async (_id, _params, _signal, _update, ctx) => {
    const answer = await ctx.ui.confirm("listen", "proceed?");
    appendFileSync(process.env.LISTEN_FIXTURE_LOG + ".confirm", `${name}:${answer}\n`);
    return text(String(answer));
  });
  pi.on("session_shutdown", async () => {
    appendFileSync(process.env.LISTEN_FIXTURE_LOG, `${name}:${instance}\n`);
  });
}
