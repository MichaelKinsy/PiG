// Drive the installed Pi createToolHtmlRenderer (core/export-html/tool-renderer.ts) directly, never a translated oracle.
// Each scenario defines tools whose renderers return components of fixed lines plus a line that records the render context.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createToolHtmlRenderer } = await import(pathToFileURL(root + "dist/core/export-html/tool-renderer.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((scenario) => {
  let componentSeq = 0;
  const contextLine = (kind, ctx, extra) => {
    ctx.state.n = (ctx.state.n ?? 0) + 1;
    return "ctx:" + JSON.stringify({
      kind, n: ctx.state.n, last: ctx.lastComponent ? ctx.lastComponent.id : null, args: ctx.args === undefined ? "undefined" : JSON.stringify(ctx.args), id: ctx.toolCallId, cwd: ctx.cwd,
      started: ctx.executionStarted, complete: ctx.argsComplete, partial: ctx.isPartial, expanded: ctx.expanded, images: ctx.showImages, error: ctx.isError, duration: ctx.durationMs === undefined ? "undefined" : ctx.durationMs, pad: ctx.outputPad, ...extra,
    });
  };
  const component = (lines, throwRender, infoFirst) => ({
    id: ++componentSeq,
    invalidate() {},
    render(w) { if (throwRender) throw new Error("render"); return infoFirst ? ["w=" + w, ...lines] : [...lines, "w=" + w]; },
  });
  const getToolRenderers = (name) => {
    const tool = scenario.tools[name];
    if (tool === undefined) return undefined;
    if (tool.getThrows) throw new Error("lookup");
    const renderers = {};
    if (tool.call) {
      renderers.renderCall = (args, theme, ctx) => {
        if (tool.throwCall) throw new Error("call");
        const c = component(tool.infoFirst ? [contextLine("call", ctx, {}), ...tool.call] : [...tool.call, contextLine("call", ctx, {})], tool.throwCallRender, tool.infoFirst);
        return c;
      };
    }
    if (tool.result) {
      renderers.renderResult = (result, options, theme, ctx) => {
        if (options.expanded ? tool.throwResult === 2 : tool.throwResult === 1) throw new Error("result");
        const texts = result.content.map((b) => (b.type === "text" ? b.text : "[" + b.type + ":" + b.mimeType + "]"));
        const extra = { texts, details: result.details === undefined || result.details === null ? "none" : JSON.stringify(result.details), isErrorResult: result.isError, optionExpanded: options.expanded, optionPartial: options.isPartial, themeIsGiven: theme !== undefined };
        const throwRender = options.expanded ? tool.throwResult === 4 : tool.throwResult === 3;
        const body = options.expanded ? tool.result.expanded : tool.result.collapsed;
        if (tool.plain) return { id: ++componentSeq, invalidate() {}, render() { return [...body]; } }; // no context line, so the two passes can render the same lines
        return component(tool.infoFirst ? [contextLine("result", ctx, extra), ...body] : [...body, contextLine("result", ctx, extra)], throwRender, tool.infoFirst);
      };
    }
    return renderers;
  };
  const renderer = createToolHtmlRenderer({ getToolRenderers, theme: {}, cwd: scenario.cwd, width: scenario.width === 0 ? undefined : scenario.width });
  return scenario.ops.map((op) => {
    if (op.op === "call") {
      const html = renderer.renderCall(op.id, op.name, JSON.parse(op.args));
      return { html: html === undefined ? "" : html };
    }
    const content = (op.content ?? []).map((b) => (b.type === "text" ? { type: "text", text: b.text } : { type: "image", data: b.data, mimeType: b.mimeType }));
    const out = renderer.renderResult(op.id, op.name, content, op.details === null ? undefined : JSON.parse(op.details), op.isError === true);
    return out === undefined ? { collapsed: "", expanded: "" } : { collapsed: out.collapsed ?? "", expanded: out.expanded ?? "" };
  });
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
