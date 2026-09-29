export default function (pi) {
  pi.on("session_start", (_, ctx) => ctx.ui.addAutocompleteProvider(current => ({
    async getSuggestions(lines, line, col, options) {
      if (lines[line] === "/boom") throw new Error("BR25_AUTOCOMPLETE_ERROR");
      return current.getSuggestions(lines, line, col, options);
    },
    applyCompletion: (...args) => current.applyCompletion(...args),
  })));
}
