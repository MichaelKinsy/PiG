// Reports the terminal capabilities pi-tui resolved while this factory ran and whether the host's capability seed leaked into the extension's environment.
import { getCapabilities } from "@earendil-works/pi-tui";

export default function (pi) {
  const atFactory = getCapabilities();
  const leaked = process.env.PIG_TERMINAL_CAPABILITIES !== undefined;
  pi.registerCommand("report_capabilities", {
    description: "Notify the host with the factory-time terminal capabilities",
    handler: async (_args, ctx) => {
      ctx.ui.notify(JSON.stringify({ atFactory, leaked }), "info");
    },
  });
}
