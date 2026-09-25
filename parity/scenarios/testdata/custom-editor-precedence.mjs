export default function register(pi) {
  pi.registerShortcut("ctrl+v", {
    description: "Prove extension shortcut precedence over paste-image",
    handler: (ctx) => ctx.ui.notify("CUSTOM-EDITOR-EXTENSION-BEFORE-PASTE", "info"),
  });
}
