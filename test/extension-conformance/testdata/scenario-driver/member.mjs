// The member extension of a scenario: a second Node extension, loaded beside the scenario driver so that, when the scenario runs shared, both are members of one
// cell (one process). It registers one command and reads no Spec.
export default function (pi) {
  pi.registerCommand("member-ping", { description: "The cell member's command", handler: async () => {} });
}
