export default function register(pi) {
  pi.registerShortcut("ctrl+shift+x", {
    description: "Parity first shortcut",
    handler: (ctx) => ctx.ui.notify("first", "info"),
  });
  pi.registerShortcut("alt+q", {
    handler: (ctx) => ctx.ui.notify("second", "info"),
  });
  pi.registerShortcut("f5", {
    description: "Parity third shortcut",
    handler: (ctx) => ctx.ui.notify("third", "info"),
  });
}
