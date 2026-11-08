// What Paseo's Pi extension does with the messages of a run, plus an editor in front of it that appends a block to the user and assistant messages in place, as a redaction extension does (Pi hands every handler one message object, and the entry the session stores is that object).
// The report goes out as one notification at agent_end.
export default function (pi) {
  const held = {};
  const report = {};
  const indexOf = (ctx, message) => (message === undefined ? -2 : ctx.sessionManager.getEntries().findIndex((entry) => entry.message === message));
  const blocks = (message) => (Array.isArray(message?.content) ? message.content.length : -1);

  pi.on("message_end", (event) => {
    const message = event.message;
    if (message.role !== "user" && message.role !== "assistant") return;
    if (!Array.isArray(message.content)) message.content = [{ type: "text", text: message.content }];
    message.content.push({ type: "text", text: "(edited)" });
  });
  pi.on("message_end", (event) => {
    if (event.message.role === "user" || event.message.role === "assistant") held[event.message.role] = event.message;
  });
  pi.on("message_start", (event, ctx) => {
    if (event.message.role === "assistant") report.userIndexAtAssistantStart = indexOf(ctx, held.user);
  });
  pi.on("turn_end", (event) => {
    report.turnEndHoldsAssistant = event.message === held.assistant;
    report.turnEndAssistantBlocks = blocks(event.message);
  });
  pi.on("agent_end", (event, ctx) => {
    const user = event.messages.find((message) => message.role === "user");
    const assistant = event.messages.find((message) => message.role === "assistant");
    report.agentEndHoldsUser = user === held.user;
    report.agentEndHoldsAssistant = assistant === held.assistant;
    report.agentEndUserBlocks = blocks(user);
    report.agentEndAssistantBlocks = blocks(assistant);
    report.userIndexAtAgentEnd = indexOf(ctx, held.user);
    report.assistantIndexAtAgentEnd = indexOf(ctx, held.assistant);
    const entries = ctx.sessionManager.getEntries();
    report.entryUserBlocks = blocks(entries[report.userIndexAtAgentEnd]?.message);
    report.entryAssistantBlocks = blocks(entries[report.assistantIndexAtAgentEnd]?.message);
    ctx.ui.notify(JSON.stringify(report), "info");
  });
}
