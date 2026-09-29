import { CustomEditor } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  const observed = { registered: false, base: false, applied: false, retained: false, shared: true };
  let latestProvider;
  pi.on("session_start", (_, ctx) => {
    let constructed = false;
    ctx.ui.addAutocompleteProvider(current => {
      constructed = true;
      let seen = false;
      const provider = {
        async getSuggestions(lines, cursorLine, cursorCol, options) {
          const result = await current.getSuggestions(lines, cursorLine, cursorCol, options);
          const query = lines[cursorLine];
          if (query !== "/rigidity-probe" && query !== "/rigidity-probe!") return result;
          if (query === "/rigidity-probe") observed.base = result?.items.some(item => item.value === "rigidity-probe") ?? false;
          observed.retained = seen;
          seen = true;
          return { items: [{ value: "rigidity-probe", label: query.endsWith("!") ? "Rigidity completion next" : "Rigidity completion" }], prefix: query };
        },
        applyCompletion() {
          observed.applied = true;
          const text = "/rigidity-probe chosen";
          return { lines: [text], cursorLine: 0, cursorCol: text.length };
        },
      };
      latestProvider = provider;
      return provider;
    });
    observed.registered = constructed;
  });
  pi.registerCommand("rigidity-custom", { handler: async (_, ctx) => {
    ctx.ui.setEditorComponent((tui, theme, keybindings) => {
      const editor = new CustomEditor(tui, theme, keybindings);
      const set = editor.setAutocompleteProvider.bind(editor);
      editor.setAutocompleteProvider = provider => { observed.shared = provider === latestProvider; set(provider); };
      return editor;
    });
    ctx.ui.notify("Custom editor armed", "info");
  }});
  pi.registerCommand("rigidity-probe", { handler: async (args, ctx) => {
    await ctx.ui.select("Autocomplete results", [
      `registered=${observed.registered}`,
      `base=${observed.base}`,
      `applied=${observed.applied}`,
      `retained=${observed.retained}`,
      `shared=${observed.shared}`,
      `args=${args}`,
    ]);
  }});
}
