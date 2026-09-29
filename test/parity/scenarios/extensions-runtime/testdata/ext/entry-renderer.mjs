export default function (pi) {
  pi.registerEntryRenderer("parity-entry", (entry, options, widthOrTheme) => {
    const render = () => [`entryrenderer:${entry.data}:expanded=${Boolean(options?.expanded)}`];
    if (typeof widthOrTheme === "number") return render(widthOrTheme);
    return { render, invalidate() {} };
  });

  pi.registerCommand("entry-probe", {
    description: "Append a custom session entry through the registered entry renderer.",
    handler: async () => {
      pi.appendEntry("parity-entry", "hello");
    },
  });
}
