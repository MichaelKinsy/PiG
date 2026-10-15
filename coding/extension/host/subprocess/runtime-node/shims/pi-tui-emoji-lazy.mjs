// Theme construction and ASCII layout do not need the RGI Unicode set. Load Pi's native expression only on the guarded emoji-width branch.
import { runtimeRequire } from "../runtime-modules.mjs";
export const rgiEmojiRegex = {
  test(value) { return runtimeRequire("./shims/pi-tui-emoji.mjs").rgiEmojiRegex.test(value); },
};
