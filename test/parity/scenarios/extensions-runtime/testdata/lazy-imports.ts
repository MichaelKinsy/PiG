// Pi 0.87.1 loader.ts:490-501 supplies shared virtual modules to dynamic imports too.
export default function (pi: any) {
  const order = ["factory"];
  pi.registerCommand("lazy-imports", {
    handler: async () => {
      order.push("command");
      const tui = await import("@earendil-works/pi-tui");
      order.push("tui");
      const legacy = await import("@mariozechner/pi-tui");
      const sdk = await import("@earendil-works/pi-coding-agent");
      order.push("theme");
      const exports = Object.keys(tui).sort();
      const original = tui.getKeybindings();
      const bindings = new tui.KeybindingsManager({ probe: { defaultKeys: ["ctrl+x"] } });
      tui.setKeybindings(bindings);
      try {
        console.log(JSON.stringify({
          order,
          exports,
          shared: exports.every(key => tui[key] === legacy[key]),
          state: legacy.getKeybindings() === bindings && legacy.getKeybindings().matches("\u0018", "probe"),
          text: new tui.Text("lazy words wrap", 1, 0).render(10),
          code: sdk.highlightCode("const answer = 42;", "javascript"),
          markdownCode: sdk.getMarkdownTheme().highlightCode("const answer = 42;", "javascript"),
          unknownLanguage: sdk.highlightCode("not code", "not-a-language"),
        }));
      } finally {
        tui.setKeybindings(original);
      }
    },
  });
}
