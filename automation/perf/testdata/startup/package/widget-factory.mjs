import { Text } from "@earendil-works/pi-tui";

export default function (pi) {
  pi.registerCommand("widget-factory-probe", {
    description: "Render a factory-backed widget",
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("factory-probe", (_tui, theme) => {
        const text = new Text("", 0, 0);
        return {
          render(width) {
            text.setText([
              theme.fg("accent", theme.bold(`factory-widget:width=${width}`)),
              theme.fg("muted", "factory-widget:second-line"),
            ].join("\n"));
            return text.render(width);
          },
          invalidate() {},
        };
      });
    },
  });
}
