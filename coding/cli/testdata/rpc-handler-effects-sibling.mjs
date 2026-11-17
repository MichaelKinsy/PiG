// A second Node extension: two of them load into one packed runtime.
export default function (pi) {
  pi.registerCommand("handler_effects_sibling", { description: "Does nothing", handler: async () => {} });
}
