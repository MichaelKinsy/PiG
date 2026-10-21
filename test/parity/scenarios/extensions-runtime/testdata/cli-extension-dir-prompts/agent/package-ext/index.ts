// An extension entry point beside a prompts/ directory. Pi resolves the directory by how it is named, not by this file.
export default function (pi) {
  pi.registerCommand("package-dir-command", { description: "package directory extension command", handler: async () => {} });
}
