// An extension entry point beside a prompts/ directory. Pi resolves the directory by how it is named, not by this file.
export default function (pi) {
  pi.registerCommand("cli-dir-command", { description: "cli directory extension command", handler: async () => {} });
}
