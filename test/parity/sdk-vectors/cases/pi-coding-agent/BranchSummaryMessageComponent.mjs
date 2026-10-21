import { builders, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a branch summary, collapsed and expanded, at outputPad 1 and 0, with a click on its body.
const summary = "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`.";
const root = (extra) => ({ kind: "container", children: [{ kind: "branch-summary", id: "b", summary, ...extra }] });
const click = { type: "click", button: "left", x: 3, y: 2 };

export const cases = [
  { name: "collapsed", args: [{ root: root({}) }] },
  { name: "expanded", args: [{ root: root({ expanded: true }) }] },
  { name: "outputPad 0 collapsed", args: [{ root: root({ outputPad: 0 }) }] },
  { name: "outputPad 0 expanded", args: [{ root: root({ outputPad: 0, expanded: true }) }] },
  { name: "click toggles", args: [{ root: root({}), steps: [{ mouse: click }, { mouse: click }] }] },
  { name: "right click ignored", args: [{ root: root({}), steps: [{ mouse: { ...click, button: "right" } }] }] },
  { name: "empty summary expanded", args: [{ root: { kind: "branch-summary", expanded: true } }] },
];

const registry = {
  ...builders,
  "branch-summary": (lib, n) => {
    const c = new lib.pi.BranchSummaryMessageComponent({ summary: n.summary ?? "" }, undefined, n.outputPad ?? 1);
    if (n.expanded) c.setExpanded(true);
    return c;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
