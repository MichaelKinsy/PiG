// ctx.ui.setEditorComponent for extensions running in PiG.
//
// Pi's setCustomEditorComponent (modes/interactive/interactive-mode.ts) calls
// the factory with its TUI, getEditorTheme() and its keybindings manager, puts
// the returned editor in the editor container in place of its own, copies the
// default editor's text, callbacks, border color, padding, autocomplete size
// and provider to it, and focuses it. From then on the editor, typically a
// CustomEditor subclass, receives every key, renders the editor rows, and its
// CustomEditor.handleInput dispatches the app actions to the handlers Pi bound
// on the default editor.
//
// Here the editor stays in the extension process. The host sends it every key
// and every text operation Pi's host performs on this.editor, shows the frames
// it renders, and runs the host's handlers for its callbacks: onSubmit,
// onChange, onEscape, onCtrlD, onPasteImage, onExtensionShortcut and the app
// actionHandlers. Editor-local clear, follow-up and idle bash Escape effects
// run before handleInput returns. The host receives their original text and
// does not replay those edits. An inputDone barrier holds the input pump until
// the key's callbacks reach the owner loop; autocomplete is an awaited call.
import { createRequire } from "node:module";
import { matchesKey } from "./shims/pi-dist/pi-tui/keys.js";
import { getSelectListTheme } from "./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js";

const require = createRequire(import.meta.url);
const RENDER_INTERVAL_MS = 16;

// The app actions Pi's setupKeyHandlers binds with defaultEditor.onAction, in
// its order; setCustomEditorComponent copies them onto a CustomEditor.
const DEFAULT_EDITOR_ACTIONS = [
  "app.clear",
  "app.suspend",
  "app.thinking.cycle",
  "app.model.cycleForward",
  "app.model.cycleBackward",
  "app.model.select",
  "app.tools.expand",
  "app.thinking.toggle",
  "app.editor.external",
  "app.message.copy",
  "app.message.followUp",
  "app.message.dequeue",
  "app.session.new",
  "app.session.tree",
  "app.session.fork",
  "app.session.resume",
];

export class EditorComponentHost {
  constructor(runtime) {
    this.runtime = runtime;
    this.lastSigintTime = 0;
    this.session = undefined;
    this.seq = 0;
  }

  // Pi's getEditorTheme() over the host's active theme.
  editorTheme() {
    const theme = this.runtime.ui.theme;
    return {
      borderColor: (text) => theme.fg("borderMuted", text),
      selectList: getSelectListTheme(),
    };
  }

  // Pi's updateEditorBorderColor: the bash-mode color while the editor text
  // starts with "!", otherwise the thinking level's border color.
  borderColor(session) {
    const theme = this.runtime.ui.theme;
    if (session.isBashMode) return theme.getBashModeBorderColor();
    return theme.getThinkingBorderColor(session.thinkingLevel || "off");
  }

  updateBorderColor(session) {
    session.component.borderColor = this.borderColor(session);
    this.requestRender(session);
  }

  install(factory) {
    const runtime = this.runtime;
    const autocomplete = runtime.callSync("ui.autocomplete.current");
    if (!autocomplete) return;
    this.seq += 1;
    const session = {
      key: `editor-${this.seq}`,
      component: undefined,
      active: true,
      configured: false,
      frameSeq: 0,
      lastLines: undefined,
      lastWidth: 0,
      lastWantsKeyRelease: false,
      renderRequested: false,
      renderTimer: undefined,
      lastRenderAt: Number.NEGATIVE_INFINITY,
      submits: new Map(),
      nextSubmit: 1,
      isBashMode: false,
      thinkingLevel: runtime.state.thinkingLevel,
      showHardwareCursor: false,
      shortcuts: [],
    };
    const self = this;
    // Pi hands the factory its TUI. The editor reads the terminal size,
    // requests renders, and may drive the terminal cursor itself.
    const tui = {
      requestRender: () => self.requestRender(session),
      get width() { return Number(runtime.ready?.width || 80); },
      get height() { return Number(runtime.ready?.height || 24); },
      terminal: {
        get columns() { return Number(runtime.ready?.width || 80); },
        get rows() { return Number(runtime.ready?.height || 24); },
        write(data) {
          if (session.active) runtime.notify("ui.terminal.write", { key: session.key, data: String(data ?? "") });
        },
      },
      getShowHardwareCursor: () => session.showHardwareCursor,
      setShowHardwareCursor(enabled) {
        session.showHardwareCursor = enabled === true;
        if (session.active) runtime.notify("ui.setShowHardwareCursor", { key: session.key, enabled: enabled === true });
      },
    };
    const component = factory(tui, this.editorTheme(), runtime.keybindings());
    this.detach();
    if (!component) {
      runtime.notify("ui.editor.clear", { key: "" });
      return;
    }
    session.component = component;
    session.embedWorkingStatus = component.embedWorkingStatus === true && typeof component.setWorkingStatusIndicator === "function";
    component.onSubmit = (text) => this.submit(session, text);
    component.onChange = (text) => this.changed(session, text);
    if (component.borderColor !== undefined) component.borderColor = this.borderColor(session);
    if (typeof component.setAutocompleteProvider === "function") {
      component.setAutocompleteProvider(runtime.autocomplete.current(autocomplete));
    }
    // Pi copies the app-level handlers onto a CustomEditor (duck-typed on
    // actionHandlers, as instanceof fails across module boundaries).
    if ("actionHandlers" in component && component.actionHandlers instanceof Map) {
      if (!component.onEscape) component.onEscape = () => this.action(session, "app.interrupt");
      if (!component.onCtrlD) component.onCtrlD = () => this.action(session, "app.exit");
      if (!component.onPasteImage) component.onPasteImage = () => this.action(session, "app.clipboard.pasteImage");
      if (!component.onExtensionShortcut) component.onExtensionShortcut = (data) => this.shortcut(session, data);
      for (const action of DEFAULT_EDITOR_ACTIONS) {
        component.actionHandlers.set(action, () => this.action(session, action));
      }
    }
    // ui.setFocus(editor).
    if (require("./shims/pi-dist/pi-tui/tui.js").isFocusable(component)) component.focused = true;
    this.session = session;
    // The host answers with the editor text, padding, autocomplete size and
    // focus it copies, then the editor renders.
    runtime.notify("ui.editor.install", { key: session.key, embedWorkingStatus: session.embedWorkingStatus });
  }

  // Pi's setEditorComponent(undefined) restores the default editor.
  clear() {
    const session = this.session;
    this.detach();
    this.runtime.notify("ui.editor.clear", { key: session?.key ?? "" });
  }

  detach() {
    const session = this.session;
    if (!session) return;
    session.active = false;
    if (session.renderTimer !== undefined) clearTimeout(session.renderTimer);
    for (const resolve of session.submits.values()) resolve();
    session.submits.clear();
    this.session = undefined;
  }

  current(key) {
    const session = this.session;
    return session && session.active && session.key === key ? session : undefined;
  }

  submit(session, text) {
    if (!session.active) return Promise.resolve();
    const id = session.nextSubmit++;
    return new Promise((resolve) => {
      session.submits.set(id, resolve);
      this.runtime.notify("ui.editor.submit", { key: session.key, id, text: String(text ?? "") });
    });
  }

  changed(session, text) {
    if (!session.active) return;
    text = String(text ?? "");
    const expanded = typeof session.component.getExpandedText === "function" ? String(session.component.getExpandedText()) : text;
    this.runtime.state.editorText = expanded;
    this.runtime.notify("ui.editor.change", { key: session.key, text, expanded });
    // Pi's defaultEditor.onChange: entering or leaving bash mode recolors the
    // border.
    const wasBashMode = session.isBashMode;
    session.isBashMode = text.trimStart().startsWith("!");
    if (wasBashMode !== session.isBashMode) this.updateBorderColor(session);
  }

  action(session, action) {
    if (!session.active) return;
    const component = session.component;
    const text = component.getText();
    const expanded = component.getExpandedText?.() ?? text;
    let local = false;
    if (action === "app.clear") {
      // Pi's handleCtrlC/clearEditor mutates the selected editor in this turn.
      const now = Date.now();
      if (now - this.lastSigintTime < 500) {
        action = "app.exit";
      } else {
        component.setText("");
        this.lastSigintTime = now;
        local = true;
      }
    } else if (action === "app.message.followUp") {
      const value = expanded.trim();
      if (!value) return;
      if (!session.config?.streaming && !session.config?.compacting) {
        component.setText("");
        component.onSubmit?.(value);
        return;
      }
      component.addToHistory?.(value);
      component.setText("");
      local = true;
    } else if (action === "app.interrupt" && session.isBashMode &&
               !session.config?.streaming && !session.config?.bashRunning && !session.config?.interruptHandled) {
      component.setText("");
      local = true;
    }
    this.runtime.notify("ui.editor.action", { key: session.key, action, text, expanded, local });
  }

  // Pi's defaultEditor.onExtensionShortcut: a key bound to an extension
  // shortcut runs it and is consumed.
  shortcut(session, data) {
    if (!session.active) return false;
    for (const key of session.shortcuts) {
      if (matchesKey(data, key)) {
        this.runtime.notify("ui.editor.shortcut", { key: session.key, data: String(data) });
        return true;
      }
    }
    return false;
  }

  // Provider replacement preserves the same chain used by the host's default editor.
  setAutocompleteProvider(descriptor) {
    const session = this.session;
    if (!session?.active) return;
    session.component.setAutocompleteProvider?.(this.runtime.autocomplete.current(descriptor));
    this.requestRender(session);
  }

  requestRender(session = this.session) {
    if (!session?.active || !session.configured) return;
    session.renderRequested = true;
    if (session.renderTimer !== undefined) return;
    const delay = Math.max(0, RENDER_INTERVAL_MS - (performance.now() - session.lastRenderAt));
    session.renderTimer = setTimeout(() => {
      session.renderTimer = undefined;
      if (session.renderRequested) this.renderNow(session);
    }, delay);
  }

  renderNow(session = this.session) {
    if (!session?.active || !session.configured) return;
    if (session.renderTimer !== undefined) {
      clearTimeout(session.renderTimer);
      session.renderTimer = undefined;
    }
    session.renderRequested = false;
    session.lastRenderAt = performance.now();
    const runtime = this.runtime;
    const width = Number(runtime.ready?.width || 80);
    let lines;
    try {
      lines = session.component.render(width);
    } catch (err) {
      runtime.fireAndForget("ui.notify", { message: `editor render failed: ${err?.message || String(err)}`, level: "error" });
      return;
    }
    const out = Array.isArray(lines) ? lines.map((line) => String(line)) : [];
    const wantsKeyRelease = session.component.wantsKeyRelease === true;
    if (width === session.lastWidth && wantsKeyRelease === session.lastWantsKeyRelease && session.lastLines &&
        out.length === session.lastLines.length && out.every((line, i) => line === session.lastLines[i])) {
      return;
    }
    session.lastLines = out;
    session.lastWidth = width;
    session.lastWantsKeyRelease = wantsKeyRelease;
    session.frameSeq += 1;
    runtime.notify("ui.editor.render", { key: session.key, lines: out, width, seq: session.frameSeq, wantsKeyRelease });
  }

  // run calls one editor method for the host and renders the result, as Pi's
  // TUI renders after input. An exception is the extension's error.
  run(session, fn) {
    try {
      fn(session.component);
    } catch (err) {
      this.runtime.fireAndForget("ui.notify", { message: `editor failed: ${err?.message || String(err)}`, level: "error" });
    }
    this.renderNow(session);
  }

  // handleNotify routes the host's editor notifies; it reports whether
  // method was one.
  handleNotify(method, args) {
    switch (method) {
      case "ui.editor.input": {
        const session = this.current(args?.key);
        if (session) {
          this.run(session, (editor) => editor.handleInput(String(args.data ?? "")));
          this.runtime.notify("ui.editor.inputDone", { key: session.key });
        }
        return true;
      }
      case "ui.editor.mouse": {
        const session = this.current(args?.key);
        if (session) this.run(session, (editor) => editor.handleMouse?.(args.event ?? {}));
        return true;
      }
      case "ui.editor.setText": {
        const session = this.current(args?.key);
        if (session) this.run(session, (editor) => editor.setText(String(args.text ?? "")));
        return true;
      }
      case "ui.editor.insertText": {
        const session = this.current(args?.key);
        if (session) this.run(session, (editor) => editor.insertTextAtCursor?.(String(args.text ?? "")));
        return true;
      }
      case "ui.editor.addToHistory": {
        const session = this.current(args?.key);
        if (session) this.run(session, (editor) => editor.addToHistory?.(String(args.text ?? "")));
        return true;
      }
      case "ui.editor.configure": {
        const session = this.current(args?.key);
        if (!session) return true;
        session.config = args;
        session.shortcuts = Array.isArray(args.shortcuts) ? args.shortcuts.map(String) : [];
        const thinkingLevel = String(args.thinkingLevel ?? "");
        const recolor = thinkingLevel !== session.thinkingLevel;
        session.thinkingLevel = thinkingLevel;
        session.showHardwareCursor = args.showHardwareCursor === true;
        session.configured = true;
        this.run(session, (editor) => {
          if (editor.setPaddingX !== undefined) editor.setPaddingX(Number(args.paddingX || 0));
          if (editor.setAutocompleteMaxVisible !== undefined) editor.setAutocompleteMaxVisible(Number(args.autocompleteMaxVisible || 5));
          if (require("./shims/pi-dist/pi-tui/tui.js").isFocusable(editor)) editor.focused = args.focused !== false;
          if (session.embedWorkingStatus) {
            const status = args.workingStatus;
            let indicator;
            if (status) {
              const { StatusIndicator } = require("./shims/pi-dist/pi-coding-agent/modes/interactive/components/status-indicator.js");
              // Empty initial frames prevent a second animation timer.
              indicator = new StatusIndicator(status.kind, undefined,
                (text) => status.spinnerColor ? status.spinnerColor + text + "\x1b[0m" : text,
                (text) => status.messageColor ? status.messageColor + text + "\x1b[0m" : text,
                status.message, { frames: [] });
              indicator.frames = status.frames;
              indicator.currentFrame = status.frames.length ? status.frame % status.frames.length : 0;
              indicator.renderIndicatorVerbatim = status.indicatorVerbatim;
              indicator.invalidate();
            }
            editor.setWorkingStatusIndicator(indicator);
          }
          // Pi's updateEditorBorderColor on a thinking level change.
          if (recolor && !session.isBashMode) editor.borderColor = this.borderColor(session);
        });
        return true;
      }
      case "ui.editor.submitted": {
        const session = this.current(args?.key);
        const resolve = session?.submits.get(Number(args.id));
        if (resolve) {
          session.submits.delete(Number(args.id));
          resolve();
        }
        return true;
      }
      case "ui.editor.closed": {
        if (this.current(args?.key)) this.detach();
        return true;
      }
      default:
        return false;
    }
  }

  // The terminal was resized or the theme changed: render again.
  refresh() {
    if (this.session?.active) this.requestRender(this.session);
  }
}
