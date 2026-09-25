// Exercises the ReadonlyFooterDataProvider handed to a ui.setFooter factory.
// pi-atelier's footer calls onBranchChange(requestRender) at construction, so a
// plain data object makes it throw "onBranchChange is not a function".
export default function (pi) {
  pi.registerCommand("show_status_footer", {
    description: "Set a status and immediately read it from a custom footer",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("local", "fresh");
      ctx.ui.setFooter((_tui, _theme, footerData) => {
        ctx.ui.notify(`local=${footerData.getExtensionStatuses().get("local") ?? "missing"}`, "info");
        return { render: () => ["footer"] };
      });
    },
  });

  pi.registerCommand("show_footer", {
    description: "Install a footer that uses the full provider surface",
    handler: async (_args, ctx) => {
      ctx.ui.setFooter((_tui, _theme, footerData) => {
        const unsubscribe = footerData.onBranchChange(() => {});
        const statuses = footerData.getExtensionStatuses();
        ctx.ui.notify(
          `branch=${footerData.getGitBranch() ?? "none"} ` +
          `statuses=${statuses instanceof Map ? statuses.size : "notamap"} ` +
          `providers=${footerData.getAvailableProviderCount()} ` +
          `unsub=${typeof unsubscribe}`,
          "info",
        );
        return { render: () => ["footer"] };
      });
    },
  });
}
