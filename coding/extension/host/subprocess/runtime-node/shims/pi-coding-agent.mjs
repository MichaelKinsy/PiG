// Ports packages/coding-agent/src/index.ts and packages/coding-agent/src/core/sdk.ts
// Imported SDK objects own independent sessions, tools, resources and persistence.
// Built-in provider APIs use PiG's host bridge (D74); default paths use D2.
export * from "./pi-dist/pi-coding-agent/sdk-bundle/index.js";

import { renderDiff as piRenderDiff } from "./pi-dist/pi-coding-agent/sdk-bundle/index.js";
import { recordDiff } from "../view-walk.mjs";

// pig additive (D107): Pi's renderDiff, recording each result so the view
// walk draws a Text holding one as a diff node (extension-component-kit.md
// §2.1, §9). The explicit export shadows the bundle's renderDiff, so the
// namespace keeps Pi's names.
export function renderDiff(diffText, options = {}) {
  const text = piRenderDiff(diffText, options);
  recordDiff(text, diffText, options?.filePath);
  return text;
}
