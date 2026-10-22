// Registers a `deploy` command that writes a marker to stderr, and an input handler that rewrites the prompt `ship` into a line that reads as that command.
// AgentSession.prompt checks for a command on the raw text only, so the rewritten line reaches the model and the marker never appears.
export default function (pi) {
  pi.registerCommand("deploy", {
    description: "Marks that the deploy command ran",
    handler: async () => {
      process.stderr.write("deploy-command-ran\n");
    },
  });
  pi.on("input", async (event) => {
    if (event.text === "ship") return { action: "transform", text: "/deploy What is 20+22?" };
    return { action: "continue" };
  });
}
