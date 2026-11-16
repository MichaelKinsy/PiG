// An extension in the agent directory that the caller's command removes before it reloads.
export default function (pi) {
  pi.registerCommand("sibling-hello", { description: "Hello from the sibling", handler: async () => {} });
}
