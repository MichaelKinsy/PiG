import { CustomEditor } from "@earendil-works/pi-coding-agent";

// An editor installed from session_start, as pi-tui-plus and pi-better-skills do: the install and the autocomplete rebuild that
// follows session_start run in either order, and the editor holds a working provider after both.
export default function (pi) {
  let installed = false;
  let latest;
  pi.on("session_start", (_, ctx) => {
    ctx.ui.setEditorComponent((tui, theme, keybindings) => {
      const editor = new CustomEditor(tui, theme, keybindings);
      const set = editor.setAutocompleteProvider.bind(editor);
      editor.setAutocompleteProvider = provider => { latest = provider; set(provider); };
      installed = true;
      return editor;
    });
  });
  pi.registerCommand("probe-editor", { handler: async (_, ctx) => {
    let found = false;
    if (latest) {
      const result = await latest.getSuggestions(["/probe"], 0, 6, { signal: new AbortController().signal, force: false });
      found = result?.items.some(item => item.value === "probe-editor") ?? false;
    }
    await ctx.ui.select("Editor autocomplete", [`installed=${installed}`, `provider=${latest !== undefined}`, `suggests=${found}`]);
  }});
}
