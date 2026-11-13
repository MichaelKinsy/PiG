// Sets, from a command, what Pi's resetExtensionUI resets when a /reload begins: a keyed status, a widget, a header and a footer.
// Neither the factory nor a session_start handler sets any of them, so nothing sets them again after the reload.
export default function (pi) {
  pi.registerCommand("parity-decorate", {
    description: "Set a status, a widget, a header and a footer.",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("reload-ui-reset", "status-set-before-reload");
      ctx.ui.setWidget("reload-ui-reset", ["widget-set-before-reload"]);
      ctx.ui.setHeader(() => ({
        render: () => ["header-set-before-reload"],
        invalidate() {},
      }));
      ctx.ui.setFooter((_tui, _theme, footerData) => ({
        render() {
          const statuses = [...footerData.getExtensionStatuses().entries()]
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([, value]) => String(value));
          return ["footer-set-before-reload", ...statuses];
        },
        invalidate() {},
      }));
    },
  });
}
