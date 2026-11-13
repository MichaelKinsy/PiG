// Pi's terminal-image.js loads with every start: Theme reads its capability cache for the colour mode. Only its image-line extent measures text, so Pi's width module loads at the first measurement.
import { runtimeRequire } from "../runtime-modules.mjs";
let width;
export function visibleWidth(str) {
  return (width ??= runtimeRequire("./shims/pi-dist/pi-tui/utils.js").visibleWidth)(str);
}
