import { CustomEditor } from "@earendil-works/pi-coding-agent";

class StatusEditor extends CustomEditor {
  constructor(tui, theme, keys) {
    super(tui, theme, keys, { embedWorkingStatus: true });
  }

  render(width) {
    const lines = super.render(width);
    lines[lines.length - 1] = this.borderColor("─".repeat(Math.max(0, width - 14)) + " STATUS-EDITOR");
    return lines;
  }
}

export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setEditorComponent((tui, theme, keys) => new StatusEditor(tui, theme, keys));
    ctx.ui.setWorkingIndicator({ frames: ["*"] });
  });
}
