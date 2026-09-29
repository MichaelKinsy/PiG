// Theme construction and ASCII layout do not need the RGI Unicode set. Load Pi's native expression only on the guarded emoji-width branch.
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
export const rgiEmojiRegex = {
  test(value) { return require("./pi-tui-emoji.mjs").rgiEmojiRegex.test(value); },
};
