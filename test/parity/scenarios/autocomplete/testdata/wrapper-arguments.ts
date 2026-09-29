export default function (pi) {
  let first;
  let calls = 0;
  pi.on("session_start", (_, ctx) => ctx.ui.addAutocompleteProvider(current => ({
    async getSuggestions(lines, line, col, options) {
      const result = await current.getSuggestions(lines, line, col, options);
      if (lines[line] === "/argument-proof ") {
        first ??= result?.items?.[0]?.value === "chosen";
        return {items:[{value:"chosen",label:"Awaited argument"}],prefix:""};
      }
      return result;
    },
    applyCompletion: (...args) => current.applyCompletion(...args),
  })));
  pi.registerCommand("argument-proof", {
    async getArgumentCompletions() { calls++; await Promise.resolve(); return [{value:"chosen",label:"chosen"}]; },
    handler: async (args, ctx) => { await ctx.ui.select("Argument results", [`first=${first}`, `called=${calls>0}`, `args=${args}`]); },
  });
}
