import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { PiGLoginDefinition } from "@michaelkinsy/pig-extension-types";

const login: PiGLoginDefinition = {
  brand: ["."],
  hero: ["."],
  mascot: ["."],
  palette: {},
  name: "Example",
  description: "Type declaration check",
  tagline: "Pi-compatible extension",
};

export default function extension(pi: ExtensionAPI): void {
  pi.on("session_start", async (_event, ctx) => {
    await ctx.ui.setLogin(login);
    ctx.ui.notify("ready", "info");
  });
  // PiG emits Pi's UI prompt lifecycle for every SDK; the kinds and the
  // optional title come from the pinned Pi declarations.
  pi.on("ui_prompt_start", (event) => {
    const kind: "select" | "confirm" | "input" | "editor" | "custom" = event.kind;
    const title: string | undefined = event.title;
    void [kind, title, event.reason satisfies "ui_prompt"];
  });
  pi.on("context_with_system", (event) => ({ messages: event.messages }));
  pi.on("agent_before_settle", (event) => {
    const outcome: "completed" | "aborted" | "error" = event.outcome;
    const canContinue: boolean = event.context.canContinue;
    void [outcome, canContinue, event.context.contextEntries, event.context.llmMessages];
    return { entries: event.entries, continue: event.continue };
  });
  pi.on("turn_end", (event) => {
    const messageEntryId: string = event.messageEntryId;
    const toolResultEntryIds: string[] = event.toolResultEntryIds;
    void [messageEntryId, toolResultEntryIds];
  });
  pi.on("ui_prompt_end", (event) => {
    void [event.kind, event.title];
  });
}
