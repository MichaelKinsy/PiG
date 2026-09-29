export default function (pi) {
  pi.registerMessageRenderer("parity-render", (message, options, widthOrTheme) => {
    const render = () => [`renderer:${message.content}:expanded=${Boolean(options?.expanded)}`];
    if (typeof widthOrTheme === "number") return render(widthOrTheme);
    return { render, invalidate() {} };
  });

  pi.registerCommand("renderer-probe", {
    description: "Send a custom message through the registered renderer.",
    handler: async () => {
      pi.sendMessage({
        customType: "parity-render",
        content: "hello",
        display: true,
      });
    },
  });
}
