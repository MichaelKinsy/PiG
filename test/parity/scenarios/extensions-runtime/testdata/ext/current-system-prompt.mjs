// Pi 1.2.0 runner.ts:1426-1477 (emitBeforeAgentStart): renderCurrentSystemPrompt builds the prompt from the options every
// handler shares, so event.systemPrompt and ctx.getSystemPrompt() show an earlier handler's edit to systemPromptOptions and
// its systemPrompt result (forceSystemPrompt). The observations replace the assistant's reply so print mode shows them.
export default function (pi) {
  const seen = [];
  const mark = (prompt) => (prompt.includes("APPENDED-BY-FIRST") ? "appended" : "plain") + (prompt.startsWith("BASE PROMPT") ? "+base" : "");
  pi.on("before_agent_start", (event, ctx) => {
    seen.push(`first event=${mark(event.systemPrompt)} ctx=${mark(ctx.getSystemPrompt())}`);
    event.systemPromptOptions.appendSystemPrompt = "APPENDED-BY-FIRST";
    seen.push(`first after-edit event=${mark(event.systemPrompt)} ctx=${mark(ctx.getSystemPrompt())}`);
  });
  pi.on("before_agent_start", (event, ctx) => {
    seen.push(`second event=${mark(event.systemPrompt)} ctx=${mark(ctx.getSystemPrompt())}`);
    return { systemPrompt: "FORCED-BY-SECOND" };
  });
  pi.on("before_agent_start", (event, ctx) => {
    seen.push(`third event=${JSON.stringify(event.systemPrompt)} ctx=${JSON.stringify(ctx.getSystemPrompt())}`);
    seen.push(`third force=${JSON.stringify(event.systemPromptOptions.forceSystemPrompt)}`);
  });
  pi.on("message_end", (event) => {
    if (event.message.role !== "assistant") return;
    return { message: { ...event.message, content: [{ type: "text", text: seen.join("\n") }] } };
  });
}
