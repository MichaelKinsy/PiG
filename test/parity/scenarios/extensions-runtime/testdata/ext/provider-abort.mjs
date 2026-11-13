// An extension provider whose stream shows "alpha beta " and then waits for the abort of its request. The model id names what its author does once the request's signal
// aborts: author-error yields an error event that holds its partial; author-ignores goes on, yields "gamma " 0.3 s later and completes.
const usage = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const aborted = (signal) => new Promise((resolve) => { if (signal.aborted) resolve(); else signal.addEventListener("abort", () => resolve(), { once: true }); });
const later = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function streamProbe(model, context, options) {
  const output = { role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id, usage: usage(), stopReason: "stop", timestamp: 1 };
  const block = { type: "text", text: "" };
  return {
    async *[Symbol.asyncIterator]() {
      yield { type: "start", partial: output };
      output.content.push(block);
      yield { type: "text_start", contentIndex: 0, partial: output };
      block.text = "alpha ";
      yield { type: "text_delta", contentIndex: 0, delta: "alpha ", partial: output };
      await later(5);
      block.text = "alpha beta ";
      yield { type: "text_delta", contentIndex: 0, delta: "beta ", partial: output };
      await aborted(options.signal);
      if (model.id === "author-error") {
        output.stopReason = "aborted";
        output.errorMessage = "the author saw the abort";
        yield { type: "error", reason: "aborted", error: output };
        return;
      }
      await later(300);
      block.text = "alpha beta gamma ";
      yield { type: "text_delta", contentIndex: 0, delta: "gamma ", partial: output };
      yield { type: "text_end", contentIndex: 0, content: block.text, partial: output };
      yield { type: "done", reason: "stop", message: output };
    },
    result: async () => output,
  };
}
export default function (pi) {
  pi.registerProvider("abort-probe", {
    baseUrl: "http://localhost:0", apiKey: "unused", api: "abort-probe-api",
    models: ["author-error", "author-ignores"].map((id) => ({ id, name: id, api: "abort-probe-api", provider: "abort-probe", baseUrl: "http://localhost:0", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 })),
    streamSimple: streamProbe,
  });
}
