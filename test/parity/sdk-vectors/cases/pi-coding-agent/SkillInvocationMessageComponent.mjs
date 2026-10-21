import { builders, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a skill invocation: one line collapsed, the skill's content expanded, at outputPad 1 and 0,
// and a click on it.
const skill = { name: "code-review", content: "Review the diff for **bugs** first.\n\n- correctness\n- naming\n\nReport findings as a list." };
const root = (extra) => ({ kind: "container", children: [{ kind: "skill-invocation", id: "s", skill, ...extra }] });
const click = { type: "click", button: "left", x: 3, y: 1 };

export const cases = [
  { name: "collapsed", args: [{ root: root({}) }] },
  { name: "expanded", args: [{ root: root({ expanded: true }) }] },
  { name: "outputPad 0 collapsed", args: [{ root: root({ outputPad: 0 }) }] },
  { name: "outputPad 0 expanded", args: [{ root: root({ outputPad: 0, expanded: true }) }] },
  { name: "click toggles", args: [{ root: root({}), steps: [{ mouse: click }, { mouse: click }] }] },
  { name: "right click ignored", args: [{ root: root({}), steps: [{ mouse: { ...click, button: "right" } }] }] },
  { name: "empty skill expanded", args: [{ root: { kind: "skill-invocation", skill: { name: "", content: "" }, expanded: true } }] },
];

const registry = {
  ...builders,
  "skill-invocation": (lib, n) => {
    const c = new lib.pi.SkillInvocationMessageComponent({ name: n.skill?.name ?? "", content: n.skill?.content ?? "" }, undefined, n.outputPad ?? 1);
    if (n.expanded) c.setExpanded(true);
    return c;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
