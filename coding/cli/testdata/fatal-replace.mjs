// fatal-replace.mjs is the Node member of the pair that fatal_exit_extension_processes_linux_test.go runs. Every Session start appends
// "node <reason> <pid>" to PIG_TEST_REPLACE_LOG, so the test knows which process served which Session. Once /arm has created the flag file
// PIG_TEST_FATAL_FLAG, an editor this extension installed refuses the second provider it is given (the sync of a rebuilt provider throws
// from the extension's own code, as test/parity/scenarios/autocomplete/testdata/editor-sync-error.ts does), so the /new that follows
// ends in Pi's fatal "Failed to create session" report. The process also takes a long time to end once its input closes, as an
// extension with a slow exit handler does: a pig that leaves it behind leaves it running for that time.
import { CustomEditor } from "@earendil-works/pi-coding-agent";
import { appendFileSync, existsSync, writeFileSync } from "node:fs";

export default function fatalReplaceExtension(pi) {
  const flag = process.env.PIG_TEST_FATAL_FLAG;
  process.on("exit", () => {
    const end = Date.now() + 1500;
    while (Date.now() < end);
  });

  pi.on("session_start", (event, ctx) => {
    appendFileSync(process.env.PIG_TEST_REPLACE_LOG, `node ${event.reason} ${process.pid}\n`);
    ctx.ui.setEditorComponent((tui, theme, keybindings) => {
      const editor = new CustomEditor(tui, theme, keybindings);
      const set = editor.setAutocompleteProvider.bind(editor);
      let calls = 0;
      editor.setAutocompleteProvider = (provider) => {
        calls++;
        if (calls > 1 && existsSync(flag)) throw new Error("EDITOR_SYNC_REFUSED");
        set(provider);
      };
      return editor;
    });
  });

  pi.registerCommand("arm", {
    description: "Make the next editor sync fail",
    handler: async (_args, ctx) => {
      writeFileSync(flag, "1");
      ctx.ui.notify("armed", "info");
    },
  });
}
