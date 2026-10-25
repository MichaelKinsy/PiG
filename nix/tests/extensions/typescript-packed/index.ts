// The TypeScript factory (packed Node cell) of the Nix acceptance check.
export default function (pi: any) {
  pi.on("session_start", (_event: unknown, ctx: any) => {
    ctx.ui.setStatus("typescript-packed", "typescript-packed: ready");
  });
  pi.registerCommand("typescript-packed-probe", {
    description: "Append an entry and count it",
    handler: async (_args: string, ctx: any) => {
      pi.appendEntry("nix-probe", { by: "typescript-packed" });
      const entries = ctx.sessionManager.getEntries().filter((entry: any) => entry.type === "custom" && entry.customType === "nix-probe" && entry.data?.by === "typescript-packed");
      ctx.ui.notify(`typescript-packed: entries=${entries.length}`, "info");
    },
  });
}
