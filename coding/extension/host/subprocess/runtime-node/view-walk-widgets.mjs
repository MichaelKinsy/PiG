// pig additive (D107): the view derivation of the widget kinds
// (docs/plan/extension-component-kit.md §2.2, §9). Each walker reads one real
// Pi component into its kit node, or returns undefined when the component
// holds something the wire cannot carry (a closure, a value out of range): the
// walk then draws it as a `lines` range. view-walk.mjs passes its helpers in,
// so this module imports nothing from it.

// The coding-agent classes the walk loads the coding-agent bundle for.
export const WIDGET_CODING_AGENT_CLASSES = ["BranchSummaryMessageComponent" , "CompactionSummaryMessageComponent" , "SkillInvocationMessageComponent" , "CustomMessageComponent" , "CancellableLoader" /* widget-classes */];

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
  add(codingAgent.SkillInvocationMessageComponent, (walk, c) => {
    if (typeof c.skillBlock?.name !== "string" || typeof c.skillBlock.content !== "string") return undefined;
    const node = message(walk, c, "skill-invocation");
    if (!node) return undefined;
    const skill = {};
    if (c.skillBlock.name) skill.name = c.skillBlock.name;
    if (c.skillBlock.content) skill.content = c.skillBlock.content;
    node.skill = skill;
    return node;
  });
  add(codingAgent.CustomMessageComponent, (walk, c) => {
    // The default rendering only: a custom renderer is a closure.
    if (c.customRenderer !== undefined || typeof c.message?.customType !== "string" || typeof c._expanded !== "boolean" || !padOk(c.outputPad) || !markdownOk(walk, c)) return undefined;
    let text;
    if (typeof c.message.content === "string") text = c.message.content;
    else if (Array.isArray(c.message.content) && c.message.content.length <= h.maxListItems && c.message.content.every((b) => b?.type !== "text" || typeof b.text === "string")) {
      text = c.message.content.filter((b) => b?.type === "text").map((b) => b.text).join("\n");
    } else return undefined;
    const node = { kind: "custom-message", id: h.conversationId(c, "custom-message") };
    if (c.message.customType) node.customType = c.message.customType;
    if (text) node.text = text;
    if (c.outputPad !== 1) node.outputPad = c.outputPad;
    if (c._expanded) node.expanded = true;
    return node;
  });
  // The loaders draw as their Loader does; the class names the kind.
  add(h.tui.CancellableLoader, (walk, c) => {
    const node = h.walkLoader(walk, c);
    if (!node) return undefined;
    node.kind = "cancellable-loader";
    node.id = h.conversationId(c, "cancellable-loader");
    return node;
  });
  add(codingAgent.BorderedLoader, (walk, c) => {
    if (typeof c.cancellable !== "boolean" || !c.loader || !Array.isArray(c.children)) return undefined;
    const border = walk.probe.token(c.children[0]?.color, "fg");
    const spinner = walk.probe.token(c.loader.spinnerColorFn, "fg");
    const messageColor = walk.probe.token(c.loader.messageColorFn, "fg");
    if (border !== "border" || spinner !== "accent" || messageColor !== "muted") return undefined;
    // The loader is the constructor's: default spinner, and the text its updateDisplay drew.
    if (c.loader.renderIndicatorVerbatim || !h.isInt(c.loader.currentFrame) || typeof c.loader.message !== "string" || c.loader.intervalMs !== 80) return undefined;
    const node = { kind: "bordered-loader", id: h.conversationId(c, "bordered-loader") };
    if (c.loader.message !== "Loading...") node.message = c.loader.message;
    if (!c.cancellable) node.cancellable = false;
    if (c.loader.currentFrame !== 0) node.frame = c.loader.currentFrame;
    return node;
  });
  // widget-walkers
  return entries;
}
