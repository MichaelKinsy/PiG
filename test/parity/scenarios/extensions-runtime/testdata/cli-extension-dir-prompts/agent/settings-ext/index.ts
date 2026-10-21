// An extension entry point beside a prompts/ directory. Pi resolves the directory by how it is named, not by this file.
export default function (pi) {
  pi.registerCommand("settings-dir-command", { description: "settings directory extension command", handler: async () => {} });
}
