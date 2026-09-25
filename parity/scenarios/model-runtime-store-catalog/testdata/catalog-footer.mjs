export default function (pi) {
  pi.registerCommand("catalog-footer", {
    description: "Report the footer provider count after an offline model refresh.",
    handler: async (_args, ctx) => {
      ctx.ui.setFooter((_tui, _theme, footerData) => {
        ctx.ui.notify(`catalog-providers:${footerData.getAvailableProviderCount()}`, "info");
        return { render: () => ["Catalog footer"] };
      });
    },
  });
}
