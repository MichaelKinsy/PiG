// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
import { CustomEditor } from "@earendil-works/pi-coding-agent";
import { matchesKey } from "@earendil-works/pi-tui";

// Pi's CustomEditor.handleInput invokes app.clear synchronously. The host's
// handleCtrlC -> clearEditor -> setText chain completes before this read.
// An acknowledgement before the next key cannot repair this observation.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setEditorComponent((tui, theme, kb) => {
      return new class extends CustomEditor {
        render(width) {
          return ["editor-action-ready", ...super.render(width)];
        }
        handleInput(data) {
          super.handleInput(data);
          if (matchesKey(data, "ctrl+c")) {
            ctx.ui.notify(`editor-action-result:${JSON.stringify(this.getText())}:end`, "info");
          }
        }
      }(tui, theme, kb);
    });
  });
}
