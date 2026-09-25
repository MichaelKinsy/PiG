export default function (pi) {
  pi.registerMessageRenderer("padding-probe", (_message, options) => ({
    render(width) {
      return [`${" ".repeat(options.outputPad ?? 0)}padding-render:pad=${options.outputPad}:expanded=${options.expanded}:width=${width}`];
    },
    invalidate() {},
  }));
  pi.registerCommand("padding-probe", {
    description: "Show the configured custom-message renderer padding",
    handler: async () => {
      pi.sendMessage({ customType: "padding-probe", content: "custom", display: true });
    },
  });
}
