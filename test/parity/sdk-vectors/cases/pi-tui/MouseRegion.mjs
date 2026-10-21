import { builders, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a mouse region: its one child and the region's handler as a description (types, handled,
// capture, focus, render). A case's `events` are "mouse:<type>/<button>/<x>/<y>" for each event the handler ran for,
// the coordinates local to the region; `results` are what the renderer is answered per mouse step ("h" handled, "c" capture,
// "f" focus, "-" none).
const text = (t) => ({ kind: "text", text: t, paddingX: 1, paddingY: 0 });
const region = (r, child = text("Region content\nsecond row")) => ({ kind: "mouse-region", id: "m", region: r, children: [child] });
const root = (...children) => ({ kind: "container", children });
const mouse = (type, x, y, extra = {}) => ({ mouse: { type, button: "left", x, y, ...extra } });

export const cases = [
  { name: "renders its child", args: [{ root: root(region({})) }] },
  { name: "every type by default", args: [{ root: root(region({})), steps: [mouse("press", 2, 0), mouse("click", 2, 0), mouse("wheel", 2, 1, { button: "none", wheelDelta: -1 })] }] },
  { name: "types filter", args: [{ root: root(region({ types: ["click"] })), steps: [mouse("press", 2, 0), mouse("click", 3, 1), mouse("release", 3, 1)] }] },
  { name: "not handled", args: [{ root: root(region({ handled: false })), steps: [mouse("click", 2, 0)] }] },
  { name: "capture", args: [{ root: root(region({ capture: true })), steps: [mouse("press", 2, 0), mouse("drag", 4, 1), mouse("release", 4, 1)] }] },
  { name: "focus and render", args: [{ root: root(region({ focus: true, render: false })), steps: [mouse("click", 2, 0)] }] },
  { name: "local coordinates", args: [{ root: root(text("Above the region"), region({}), text("Below")), steps: [mouse("click", 2, 1), mouse("click", 2, 2), mouse("click", 2, 4)] }] },
  { name: "the child takes the event first", args: [{ root: root(region({}, { kind: "branch-summary", id: "b", summary: "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`." })), steps: [mouse("press", 3, 2), mouse("click", 3, 2), mouse("click", 3, 2)] }] },
  { name: "right button", args: [{ root: root(region({})), steps: [mouse("click", 2, 0, { button: "right" })] }] },
];

const registry = {
  ...builders,
  "mouse-region": (lib, n, build) => {
    const r = n.region ?? {};
    return new lib.tui.MouseRegion(build(n.children[0]), (event) => {
      if (r.types?.length && !r.types.includes(event.type)) return undefined;
      lib.events.push(`mouse:${event.type}/${event.button}/${event.x}/${event.y}`);
      const result = { handled: r.handled ?? true };
      if (r.capture) result.capture = true;
      if (r.focus) result.focus = true;
      if (r.render !== undefined) result.render = r.render;
      return result;
    });
  },
  // The child a region lets handle the event first: a click toggles it.
  "branch-summary": (lib, n) => new lib.pi.BranchSummaryMessageComponent({ summary: n.summary ?? "" }, undefined, 1),
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
