import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

class ParamPropGreeter {
  constructor(private readonly prefix: string) {}

  text(name: string): string {
    return `${this.prefix}:${name}`;
  }
}

export default function (pi: ExtensionAPI) {
  const greeter = new ParamPropGreeter("param-prop");

  pi.registerCommand("param_prop_ts", {
    description: "Exercise TS parameter-property transform support",
    async handler(_args, ctx) {
      ctx.ui.notify(greeter.text("loaded"), "info");
    },
  });
}
