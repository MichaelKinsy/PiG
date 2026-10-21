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
export function widgetRenderHandlers(tui, h) {
  return [
    // An Input draws what its value, cursor, focus, prompt and placeholder say. The Node component owns the keys, so the
    // node names the cursor and the focus it holds, and the host applies them (§2.2).
    [tui.Input.prototype.render, (walk, c) => {
      if (typeof c.value !== "string" || !h.isInt(c.cursor) || c.cursor < 0 || c.cursor > c.value.length) return undefined;
      if (typeof c.prompt !== "string" || typeof c.placeholder !== "string" || typeof c.focused !== "boolean") return undefined;
      const color = walk.probe.token(c.placeholderStyle, "fg");
      if (color === h.FAIL) return undefined;
      const node = { kind: "input", id: h.conversationId(c, "input") };
      if (c.value) node.value = c.value;
      if (c.prompt !== "> ") node.prompt = c.prompt;
      if (c.placeholder) node.placeholder = c.placeholder;
      if (color) node.placeholderColor = color;
      node.cursor = { line: 0, col: c.cursor };
      if (c.focused) node.focused = true;
      return node;
    }],
    // An Editor draws what its text, cursor, focus, padding, border and autocomplete list say. As an Input's, the node names the
    // cursor and the focus the Node component holds. State the node cannot name (a paste marker, history browsing, an open
    // autocomplete list, a pending character jump) leaves the editor to a lines range.
    [tui.Editor.prototype.render, (walk, c) => {
      const state = c.state;
      if (!Array.isArray(state?.lines) || state.lines.length === 0 || !state.lines.every((line) => typeof line === "string")) return undefined;
      if (!h.isInt(state.cursorLine) || !h.isInt(state.cursorCol) || state.cursorLine < 0 || state.cursorLine >= state.lines.length || state.cursorCol < 0) return undefined;
      if (c.autocompleteState || (c.pastes?.size ?? 0) > 0 || c.historyIndex !== -1 || c.jumpMode !== null || c.isInPaste) return undefined;
      if (!h.isInt(c.paddingX) || c.paddingX < 0 || !h.isInt(c.autocompleteMaxVisible) || typeof c.focused !== "boolean" || typeof c.disableSubmit !== "boolean") return undefined;
      if (!Array.isArray(c.history) || c.history.length > h.maxListItems || !c.history.every((entry) => typeof entry === "string")) return undefined;
      if (!walk.probe.sameTheme(c.theme?.selectList, "select")) return undefined;
      const border = walk.probe.token(c.borderColor, "fg");
      if (typeof border !== "string") return undefined;
      const node = { kind: "editor", id: h.conversationId(c, "editor") };
      const text = state.lines.join("\n");
      if (text) node.value = text;
      node.cursor = { line: state.cursorLine, col: state.cursorCol };
      if (c.paddingX !== 0) node.paddingX = c.paddingX;
      if (c.autocompleteMaxVisible !== 5) node.autocompleteMaxVisible = c.autocompleteMaxVisible;
      if (border !== "borderMuted") node.borderColor = border;
      if (c.disableSubmit) node.disableSubmit = true;
      // Pi keeps the history most recent first; the wire lists the addToHistory calls oldest first.
      if (c.history.length > 0) node.history = [...c.history].reverse();
      const provider = c.autocompleteProvider;
      if (provider !== undefined) {
        // Only the provider the wire names: slash commands and a base path, without the fd search.
        if (!(provider instanceof h.tui.CombinedAutocompleteProvider) || provider.fdPath !== null || typeof provider.basePath !== "string" || !Array.isArray(provider.commands)) return undefined;
        const commands = [];
        for (const command of provider.commands) {
          if (typeof command?.name !== "string" || typeof command.getArgumentCompletions === "function") return undefined;
          if ((command.description !== undefined && typeof command.description !== "string") || (command.argumentHint !== undefined && typeof command.argumentHint !== "string")) return undefined;
          const wire = { name: command.name };
          if (command.description) wire.description = command.description;
          if (command.argumentHint) wire.argumentHint = command.argumentHint;
          commands.push(wire);
        }
        node.autocomplete = {};
        if (commands.length > 0) node.autocomplete.commands = commands;
        if (provider.basePath) node.autocomplete.basePath = provider.basePath;
      }
      if (c.focused) node.focused = true;
      return node;
    }],
    // A ScrollView draws its child at the content width (and a column more for a scrollbar that is always shown). The node
    // carries the options; the scroll state is the host's, which lays out without a viewport height (layoutScroll).
    [tui.ScrollView.prototype.render, (walk, c, _rows, width, path, depth) => {
      if (width === undefined || !c.child || typeof c.followEnd !== "boolean" || typeof c.primary !== "boolean") return undefined;
      if (!["chain", "contain"].includes(c.overscroll) || !["hidden", "auto", "always"].includes(c.currentScrollbar)) return undefined;
      if (!h.isInt(c.scrollbarHideDelayMs) || c.scrollbarHideDelayMs < 0 || !h.isInt(c.currentScrollTop) || c.currentScrollTop < 0) return undefined;
      // A style that is Pi's default has no token; any other must be a theme token.
      const styleToken = (style, escape, close) => {
        if (typeof style !== "function") return h.FAIL;
        if (style("x") === `${escape}x${close}`) return null;
        const token = walk.probe.token(style, "fg");
        return typeof token === "string" ? token : h.FAIL;
      };
      const track = styleToken(c.scrollbarTrackStyle, "\x1b[90m", "\x1b[39m");
      const thumb = styleToken(c.scrollbarThumbStyle, "\x1b[37m", "\x1b[39m");
      if (track === h.FAIL || thumb === h.FAIL) return undefined;
      const children = walk.children([c.child], undefined, c.getContentWidth(width), path, depth);
      if (!children) return undefined;
      const node = { kind: "scroll-view", id: h.conversationId(c, "scroll-view"), children };
      if (c.followEnd) node.follow = "end";
      if (c.primary) node.primary = true;
      if (c.overscroll !== "chain") node.overscroll = c.overscroll;
      if (c.currentScrollbar !== "hidden") node.scrollbar = c.currentScrollbar;
      if (track) node.scrollbarTrackColor = track;
      if (thumb) node.scrollbarThumbColor = thumb;
      if (c.scrollbarHideDelayMs !== 1000) node.scrollbarHideDelayMs = c.scrollbarHideDelayMs;
      return node;
    }],
    // A MouseRegion draws its child unchanged; its handler is a closure, which the Node component runs itself, since a Node
    // view takes no mouse on the host.
    [tui.MouseRegion.prototype.render, (walk, c, rows, width, path, depth) => {
      if (!c.child || typeof c.onMouse !== "function") return undefined;
      return walk.node(c.child, rows, width, path, depth + 1);
    }],
    // widget-render-walkers
  ];
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
