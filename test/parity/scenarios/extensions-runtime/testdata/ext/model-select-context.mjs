// Records what model_select and thinking_level_select handlers observe: the
// event, ctx.model and pi.getThinkingLevel(). Pi sets the Session's model and
// thinking level before it emits either event, and ctx.model reads the Session.
export default function (pi) {
  const lines = ["MODEL SELECT PROBE"];
  const show = (ctx) => ctx.hasUI && ctx.ui.setWidget("model-select-probe", [...lines]);
  pi.on("session_start", (_event, ctx) => show(ctx));
  pi.on("model_select", (event, ctx) => {
    lines.push(`model_select ${event.source} ${event.previousModel?.id}->${event.model.id} ctx=${ctx.model?.id} level=${pi.getThinkingLevel()}`);
    show(ctx);
  });
  pi.on("thinking_level_select", (event, ctx) => {
    lines.push(`thinking_level_select ${event.previousLevel}->${event.level} ctx=${ctx.model?.id} level=${pi.getThinkingLevel()}`);
    show(ctx);
  });
}
