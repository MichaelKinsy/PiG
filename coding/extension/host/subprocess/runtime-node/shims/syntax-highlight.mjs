import { createRequire } from "node:module";

// Theme construction does not need language grammars. Load Pi's shared highlighter synchronously on the first highlighting call; interactive startup still schedules its complete language set through Pi's original function.
const require = createRequire(import.meta.url);
const syntaxHighlight = () => require("./pi-dist/pi-coding-agent/utils/syntax-highlight.js");

export function highlight(code, options) {
  return syntaxHighlight().highlight(code, options);
}

export function supportsLanguage(name) {
  return syntaxHighlight().supportsLanguage(name);
}

export function loadAllHighlightLanguages() {
  return syntaxHighlight().loadAllHighlightLanguages();
}
