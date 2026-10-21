import { builders, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a compaction summary: collapsed and expanded, at outputPad 1 and 0, with the token count
// Pi's toLocaleString groups, and a click on its body.
const summary = "Compacted the first **forty** turns:\n\n1. the build setup\n2. the failing test\n\nThe fix is pending.";
const root = (extra, tokens = 123456) => ({ kind: "container", children: [{ kind: "compaction-summary", id: "c", summary, tokensBefore: tokens, ...extra }] });
const click = { type: "click", button: "left", x: 3, y: 2 };

export const cases = [
  { name: "collapsed", args: [{ root: root({}) }] },
  { name: "expanded", args: [{ root: root({ expanded: true }) }] },
  { name: "outputPad 0 collapsed", args: [{ root: root({ outputPad: 0 }) }] },
  { name: "outputPad 0 expanded", args: [{ root: root({ outputPad: 0, expanded: true }) }] },
  { name: "tokens 999", args: [{ root: root({}, 999) }] },
  { name: "tokens 1234567 expanded", args: [{ root: root({ expanded: true }, 1234567) }] },
  { name: "click toggles", args: [{ root: root({}), steps: [{ mouse: click }, { mouse: click }] }] },
  { name: "right click ignored", args: [{ root: root({}), steps: [{ mouse: { ...click, button: "right" } }] }] },
  { name: "empty summary tokens 0 expanded", args: [{ root: { kind: "compaction-summary", tokensBefore: 0, expanded: true } }] },
];

const registry = {
  ...builders,
  "compaction-summary": (lib, n) => {
    const c = new lib.pi.CompactionSummaryMessageComponent({ summary: n.summary ?? "", tokensBefore: n.tokensBefore ?? 0 }, undefined, n.outputPad ?? 1);
    if (n.expanded) c.setExpanded(true);
    return c;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
