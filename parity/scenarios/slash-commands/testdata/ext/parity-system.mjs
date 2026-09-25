export default function (pi) {
  pi.registerCommand("parity-system", {
    description: "Assert sample-skill is present in the effective system prompt.",
    async handler(_args, ctx) {
      const prompt = ctx.getSystemPrompt();
      const ok = prompt.includes("sample-skill") && prompt.includes("Use sample-skill guidance.");
      ctx.ui.notify(ok ? "system-skill-ok" : "system-skill-missing", ok ? "info" : "error");
    },
  });
}
