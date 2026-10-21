import { builders, fg, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a scroll view: ScrollView's options as fields and its one child. A view surface lays its
// tree out without a viewport height, as Pi's layout engine does for a scroll node given none (layout.ts layoutScroll):
// the viewport is the content's height. `changes` of a frame are "<scrollTop>/<viewportHeight>/<followingEnd>/<scrollbarVisible>"
// after the first render and each step.
const text = (t, extra = {}) => ({ kind: "text", text: t, paddingX: 1, paddingY: 0, ...extra });
const view = (extra, child = text(LINES)) => ({ kind: "scroll-view", id: "s", children: [child], ...extra });
const root = (...children) => ({ kind: "container", children });
const LINES = "Alpha line of the log\nBeta line of the log\nGamma line of the log that is long enough to wrap at the narrow widths\nDelta";

export const cases = [
  { name: "default", args: [{ root: root(view({})) }] },
  { name: "scrollbar always", args: [{ root: root(view({ scrollbar: "always" })) }] },
  { name: "scrollbar auto", args: [{ root: root(view({ scrollbar: "auto" })) }] },
  { name: "scrollbar colors", args: [{ root: root(view({ scrollbar: "always", scrollbarTrackColor: "dim", scrollbarThumbColor: "accent" })) }] },
  { name: "follow end", args: [{ root: root(view({ follow: "end" })) }] },
  { name: "primary and contain", args: [{ root: root(view({ primary: true, overscroll: "contain" })) }] },
  { name: "hide delay", args: [{ root: root(view({ scrollbar: "auto", scrollbarHideDelayMs: 50 })) }] },
  { name: "scroll top clamps", args: [{ root: root(view({ scrollTop: 3 })) }] },
  { name: "markdown child", args: [{ root: root(view({ scrollbar: "always" }, { kind: "markdown", text: "# Title\n\n- one\n- two with a long line that wraps at narrow widths\n\n`code`", paddingX: 1, paddingY: 0 })) }] },
  { name: "after other rows", args: [{ root: root(text("Header"), view({ scrollbar: "always" }), text("Footer")) }] },
  { name: "empty child", args: [{ root: root(view({ scrollbar: "always" }, { kind: "container" })) }] },
  { name: "wheel and click pass", args: [{ root: root(view({}), text("after")), steps: [{ mouse: { type: "wheel", button: "none", x: 3, y: 1, wheelDelta: 1 } }, { mouse: { type: "click", button: "left", x: 3, y: 1 } }] }] },
];

const registry = {
  ...builders,
  "scroll-view": (lib, n, build) => {
    const options = {};
    if (n.follow) options.follow = n.follow;
    if (n.primary) options.primary = true;
    if (n.overscroll) options.overscroll = n.overscroll;
    if (n.scrollbar) options.scrollbar = n.scrollbar;
    if (n.scrollbarTrackColor) options.scrollbarTrackStyle = fg(n.scrollbarTrackColor);
    if (n.scrollbarThumbColor) options.scrollbarThumbStyle = fg(n.scrollbarThumbColor);
    if (n.scrollbarHideDelayMs !== undefined) options.scrollbarHideDelayMs = n.scrollbarHideDelayMs;
    const scroll = new lib.tui.ScrollView(build(n.children[0]), options);
    if (n.scrollTop !== undefined) scroll.scrollTo(n.scrollTop);
    // The layout of a surface that gives the scroll node no height: the viewport is the content.
    const render = scroll.render.bind(scroll);
    scroll.render = (width) => {
      const rows = render(width);
      scroll.updateLayout(rows.length, rows.length, () => {});
      return rows;
    };
    lib.ids[n.id] = scroll;
    lib.probeInitial = true;
    lib.probe = () => `${scroll.scrollTop}/${scroll.viewportHeight}/${scroll.isFollowingEnd}/${scroll.isScrollbarVisible}`;
    return scroll;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
