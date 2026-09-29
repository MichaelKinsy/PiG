import { createRequire } from "node:module";
const require = createRequire(import.meta.url);

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts: setExtensionWidget's string-list component.
export function stringWidget(content, theme) {
  const { Container } = require("./shims/pi-dist/pi-tui/tui.js");
  const { Text } = require("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
  const container = new Container();
  const maxWidgetLines = 10;
  for (const line of content.slice(0, maxWidgetLines)) {
    container.addChild(new Text(line, 1, 0));
  }
  if (content.length > maxWidgetLines) {
    container.addChild(new Text(theme.fg("muted", "... (widget truncated)"), 1, 0));
  }
  return container;
}
