// This command exists only to test rejection when a command is submitted through a queue API.
export default function (pi) {
  pi.registerCommand("queue-target", { handler: async () => {} });
}
