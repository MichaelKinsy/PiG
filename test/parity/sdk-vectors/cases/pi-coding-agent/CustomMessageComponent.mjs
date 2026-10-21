import { builders, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for an extension's custom message drawn by default: its customType label over the text's markdown,
// at outputPad 1 and 0, with the expanded state Pi's default rendering ignores.
const text = "Build **passed** in 12s\n\n- 214 tests\n- 0 failures";
const root = (extra, t = text) => ({ kind: "container", children: [{ kind: "custom-message", id: "m", customType: "ci-status", text: t, ...extra }] });

export const cases = [
  { name: "default", args: [{ root: root({}) }] },
  { name: "outputPad 0", args: [{ root: root({ outputPad: 0 }) }] },
  { name: "expanded changes nothing", args: [{ root: root({ expanded: true }) }] },
  { name: "empty text", args: [{ root: root({}, "") }] },
  { name: "no id", args: [{ root: { kind: "custom-message", customType: "note", text: "plain" } }] },
];

const registry = {
  ...builders,
  "custom-message": (lib, n) => {
    const c = new lib.pi.CustomMessageComponent({ customType: n.customType ?? "", content: n.text ?? "" }, undefined, undefined, n.outputPad ?? 1);
    if (n.expanded) c.setExpanded(true);
    return c;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
