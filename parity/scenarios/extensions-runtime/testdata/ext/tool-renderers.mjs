// tool-renderers: tools whose renderCall/renderResult exercise upstream's
// tool-card lifecycle: renderer state shared by both renderers, the last
// component, context.invalidate, partial results, errors, throwing renderers
// and renderShell "self".
import { Text } from "@earendil-works/pi-tui";

const parameters = {
  type: "object",
  properties: { topic: { type: "string", description: "Card topic." } },
  required: ["topic"],
};

const flags = (context) =>
  `started=${context.executionStarted} complete=${context.argsComplete} partial=${context.isPartial} expanded=${context.expanded} error=${context.isError}`;

const execute = async (_toolCallId, params, _signal, onUpdate) => {
  onUpdate?.({ content: [{ type: "text", text: `working on ${params.topic}` }], details: { stage: "partial" } });
  await new Promise((resolve) => setTimeout(resolve, 150));
  return { content: [{ type: "text", text: `done ${params.topic}\nsecond line` }], details: { stage: "final" } };
};

export default function (pi) {
  pi.registerTool({
    name: "render_card",
    label: "Render card",
    description: "Render a card with custom call and result renderers.",
    parameters,
    execute,
    renderCall(args, theme, context) {
      context.state.topic = args.topic;
      if (!context.state.scheduled) {
        context.state.scheduled = true;
        setTimeout(() => {
          context.state.invalidated = true;
          context.invalidate();
        }, 30);
      }
      const text = `${theme.fg("toolTitle", theme.bold("card"))} ${theme.fg("accent", args.topic ?? "")} invalidated=${context.state.invalidated === true}`;
      if (context.lastComponent) {
        context.lastComponent.setText(text);
        return context.lastComponent;
      }
      return new Text(text, 0, 0);
    },
    renderResult(result, options, theme, context) {
      const body = result.content.map((block) => (block.type === "text" ? block.text : `[${block.type}]`)).join("\n");
      const lines = options.expanded ? body.split("\n") : body.split("\n").slice(0, 1);
      return new Text(
        [
          "",
          ...lines.map((line) => theme.fg("toolOutput", line)),
          theme.fg("muted", `topic=${context.state.topic} stage=${result.details?.stage} ${flags(context)}`),
        ].join("\n"),
        0,
        0,
      );
    },
  });

  pi.registerTool({
    name: "render_self",
    label: "Render self",
    description: "Render a card that draws its own framing.",
    parameters,
    execute,
    renderShell: "self",
    renderCall(args, theme, context) {
      if (!context.isPartial && !context.expanded) return new Text("", 0, 0);
      return new Text(theme.fg("accent", `self call ${args.topic}`), 0, 0);
    },
    renderResult(result, _options, theme, context) {
      const first = result.content.find((block) => block.type === "text")?.text.split("\n")[0] ?? "";
      return new Text(theme.fg("muted", `self result ${first} partial=${context.isPartial}`), 0, 0);
    },
  });

  pi.registerTool({
    name: "render_throw",
    label: "Render throw",
    description: "Render a card whose renderers throw.",
    parameters,
    execute,
    renderCall() {
      throw new Error("renderCall failed");
    },
    renderResult() {
      throw new Error("renderResult failed");
    },
  });

  pi.registerTool({
    name: "render_fail",
    label: "Render fail",
    description: "Render a card for a tool that fails.",
    parameters,
    async execute(_toolCallId, params) {
      throw new Error(`cannot render ${params.topic}`);
    },
    renderCall(args, theme) {
      return new Text(`${theme.fg("toolTitle", theme.bold("fail"))} ${args.topic}`, 0, 0);
    },
    renderResult(result, options, theme, context) {
      const body = result.content.map((block) => block.text ?? "").join("\n");
      return new Text(`\n${theme.fg("error", body)} ${flags(context)} expandedOption=${options.expanded}`, 0, 0);
    },
  });
}
