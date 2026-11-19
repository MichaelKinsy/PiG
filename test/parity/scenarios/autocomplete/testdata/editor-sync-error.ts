import { CustomEditor } from "@earendil-works/pi-coding-agent";
import { existsSync, writeFileSync, rmSync, appendFileSync, readFileSync } from "node:fs";

// An extension whose installed editor refuses the autocomplete provider while the flag file exists: the sync of a rebuilt provider
// (interactive-mode.ts setupAutocompleteProvider -> editor.setAutocompleteProvider) throws from the extension own code.
// The first call of an editor (its install) passes unless the editor was built by /install-editor.
export default function (pi) {
  const flag = process.env.EDITOR_SYNC_FLAG;
  const armed = () => (flag && existsSync(flag)) || process.env.EDITOR_SYNC_ARMED === "1";
  const editorFactory = (refuseInstall) => (tui, theme, keybindings) => {
    const editor = new CustomEditor(tui, theme, keybindings);
    const set = editor.setAutocompleteProvider.bind(editor);
    let calls = 0;
    editor.setAutocompleteProvider = provider => {
      calls++;
      if (process.env.EDITOR_SYNC_LOG) appendFileSync(process.env.EDITOR_SYNC_LOG, "c");
      if (armed() && (calls > 1 || refuseInstall || process.env.EDITOR_SYNC_FIRST === "1")) throw new Error("EDITOR_SYNC_REFUSED");
      set(provider);
    };
    return editor;
  };
  pi.on("session_start", (_, ctx) => {
    ctx.ui.setEditorComponent(editorFactory(false));
  });
  pi.registerCommand("arm", { handler: async (_, ctx) => { writeFileSync(flag, "1"); ctx.ui.notify("armed", "info"); } });
  pi.registerCommand("disarm", { handler: async (_, ctx) => { rmSync(flag, { force: true }); ctx.ui.notify("disarmed", "info"); } });
  pi.registerCommand("mark", { handler: async (name) => { appendFileSync(process.env.EDITOR_SYNC_LOG, "|" + name + "|"); } });
  pi.registerCommand("show", { handler: async (_, ctx) => { ctx.ui.notify("calls=" + readFileSync(process.env.EDITOR_SYNC_LOG, "utf8"), "info"); } });
  pi.registerCommand("ping", { handler: async (_, ctx) => { ctx.ui.notify("pong", "info"); } });
  pi.registerCommand("add-provider", { handler: async (_, ctx) => { ctx.ui.addAutocompleteProvider(current => current); ctx.ui.notify("added", "info"); } });
  pi.registerCommand("install-editor", { handler: async (_, ctx) => { ctx.ui.setEditorComponent(editorFactory(true)); ctx.ui.notify("installed", "info"); } });
}
