// parity-exec: exercises the extension-level API.exec() (core/exec.ts).
// Registers a slash command /exec-probe that calls pi.exec("echo", ["exec-ok"])
// and surfaces the result stdout via ctx.ui.notify().
export default function (pi) {
  pi.registerCommand("exec-probe", {
    description: "Run echo via pi.exec to test extension exec API.",
    args: "",
    handler: async (args, ctx) => {
      const result = await pi.exec("echo", ["exec-ok"]);
      ctx.ui.notify("exec-stdout:" + result.stdout.trim(), "info");
    },
  });
}
