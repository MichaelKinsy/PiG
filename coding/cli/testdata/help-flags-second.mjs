// A second extension's flags follow the first extension's flags in --help.
export default function (pi) {
  pi.registerFlag("zeta", { description: "Second extension flag", type: "string" });
}
