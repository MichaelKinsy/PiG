// load-failure: a TypeScript extension whose factory throws. Pi reports the
// thrown message as the load error.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  (pi as unknown as { notAThing(): void }).notAThing();
}
