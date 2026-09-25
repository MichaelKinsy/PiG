import { Text } from "@earendil-works/pi-tui";

export default function (pi) {
  let value = 0;
  let requestRender = () => {};

  pi.registerCommand("widget_factory", {
    description: "Exercise a TypeScript widget component factory",
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("dynamic", (tui, theme) => {
        requestRender = tui.requestRender;
        const text = new Text("", 0, 0);
        return {
          render(width) {
            text.setText([
              theme.bold(`widget value=${value} width=${width}`),
              "widget second line",
            ].join("\n"));
            return text.render(width);
          },
          invalidate() {},
        };
      });
      setTimeout(() => {
        value += 1;
        requestRender();
      }, 20);
    },
  });

  pi.registerCommand("widget_clear", {
    description: "Clear the TypeScript widget",
    handler: async (_args, ctx) => ctx.ui.setWidget("dynamic", undefined),
  });
}
