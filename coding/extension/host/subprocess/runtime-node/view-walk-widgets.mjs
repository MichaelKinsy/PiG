// pig additive (D107): the view derivation of the widget kinds
// (docs/plan/extension-component-kit.md §2.2, §9). Each walker reads one real
// Pi component into its kit node, or returns undefined when the component
// holds something the wire cannot carry (a closure, a value out of range): the
// walk then draws it as a `lines` range. view-walk.mjs passes its helpers in,
// so this module imports nothing from it.

// The coding-agent classes the walk loads the coding-agent bundle for.
export const WIDGET_CODING_AGENT_CLASSES = ["BranchSummaryMessageComponent" , "CompactionSummaryMessageComponent" /* widget-classes */];

// The pi-tui widget classes by render function: none are drawn by their
// base class's render alone.
export function widgetRenderHandlers(_tui, _h) {
  return [];
}

// The coding-agent widget classes by prototype: each draws as a Box or a
// Container, so the class itself names the kind.
export function widgetClassHandlers(codingAgent, h) {
  const entries = [];
  const add = (cls, walker) => {
    if (cls) entries.push([cls.prototype, walker]);
  };

  // The summary, custom and skill messages draw with getMarkdownTheme() and
  // the active theme; a click's toggle is the component's own `expanded`.
  const markdownOk = (walk, c) => walk.probe.sameTheme(c.markdownTheme, "markdown");
  const padOk = (pad) => h.isInt(pad) && pad >= 0;
  const message = (walk, c, kind) => {
    if (typeof c.expanded !== "boolean" || !padOk(c.paddingX) || !markdownOk(walk, c)) return undefined;
    const node = { kind, id: h.conversationId(c, kind) };
    if (c.paddingX !== 1) node.outputPad = c.paddingX;
    if (c.expanded) node.expanded = true;
    return node;
  };

  add(codingAgent.BranchSummaryMessageComponent, (walk, c) => {
    if (typeof c.message?.summary !== "string") return undefined;
    const node = message(walk, c, "branch-summary");
    if (node && c.message.summary) node.summary = c.message.summary;
    return node;
  });
  add(codingAgent.CompactionSummaryMessageComponent, (walk, c) => {
    if (typeof c.message?.summary !== "string" || !h.isInt(c.message.tokensBefore) || c.message.tokensBefore < 0) return undefined;
    const node = message(walk, c, "compaction-summary");
    if (!node) return undefined;
    if (c.message.summary) node.summary = c.message.summary;
    node.tokensBefore = c.message.tokensBefore;
    return node;
  });
  // widget-walkers
  return entries;
}
