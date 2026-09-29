// A modal editor shaped like pi-vim: a CustomEditor subclass installed with
// ctx.ui.setEditorComponent that reads its label from settings through
// SettingsManager, switches INSERT/NORMAL on Escape and i, maps h/l/0/$/x to
// Pi's editor keys in NORMAL mode, passes Escape in NORMAL mode on to Pi, and
// draws its mode on the bottom border.
import { CustomEditor, SettingsManager } from "@earendil-works/pi-coding-agent";
import { matchesKey, truncateToWidth, visibleWidth } from "@earendil-works/pi-tui";

const NORMAL_KEYS = { h: "\x1b[D", l: "\x1b[C", 0: "\x01", $: "\x05", x: "\x1b[3~" };

class ProbeModalEditor extends CustomEditor {
  constructor(tui, theme, keybindings, label, colors) {
    super(tui, theme, keybindings);
    this.mode = "insert";
    this.label = label;
    this.colors = colors;
  }

  handleInput(data) {
    if (this.mode === "insert") {
      if (matchesKey(data, "escape")) {
        this.mode = "normal";
        this.tui.requestRender();
        return;
      }
      super.handleInput(data);
      return;
    }
    if (data === "i") {
      this.mode = "insert";
      this.tui.requestRender();
      return;
    }
    if (NORMAL_KEYS[data]) {
      super.handleInput(NORMAL_KEYS[data]);
      return;
    }
    if (matchesKey(data, "escape") || matchesKey(data, "enter") || data.length !== 1) super.handleInput(data);
  }

  render(width) {
    const lines = super.render(width);
    if (lines.length === 0) return lines;
    const text = ` ${this.label}:${this.mode === "insert" ? "INSERT" : "NORMAL"} `;
    const label = this.colors[this.mode](`\x1b[7m${text}\x1b[27m`);
    const last = lines.length - 1;
    lines[last] = truncateToWidth(lines[last], width - visibleWidth(text), "") + label;
    return lines;
  }
}

export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    const settings = SettingsManager.create(ctx.cwd);
    const label = settings.getGlobalSettings().editorProbe?.label ?? "none";
    const theme = ctx.ui.theme;
    const colors = { insert: (s) => theme.fg("success", s), normal: (s) => theme.fg("accent", s) };
    ctx.ui.setEditorComponent((tui, editorTheme, keybindings) => new ProbeModalEditor(tui, editorTheme, keybindings, label, colors));
  });
}
