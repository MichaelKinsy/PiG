import registerToolRenderers from "./tool-renderers.mjs";

// export-osc8-links: the four tools of tool-renderers.mjs (the faux prompt "Run: extension render cards" calls each once), run sequentially as
// export-tool-renderers.mjs runs them, with render_card's result drawn as terminal lines that carry OSC 8 hyperlinks and other escape
// sequences (Pi 0e1e3fc81, #12052: core/export-html/ansi-to-html.ts, tool-renderer.ts). The component is a plain object, so the lines reach the
// HTML converter exactly as written here; no pi-tui component wraps, pads or re-styles them.
const ESC = "\x1b";
const BEL = "\x07";
const ST = `${ESC}\\`;
const link = (url, text, end = BEL) => `${ESC}]8;;${url}${end}${text}${ESC}]8;;${end}`;

const lines = [
  // Only escape sequences: blank once they are stripped, so the result trim drops it at the start.
  `${ESC}[2K`,
  // A BEL-terminated link whose URL needs HTML escaping.
  `docs: ${link("https://example.com/docs?a=1&b=2", "the docs")} (BEL)`,
  // An ST-terminated link inside bold text, with its own color inside the link.
  `${ESC}[1mbold ${link("http://example.com/a", `${ESC}[31mred link${ESC}[39m`, ST)} tail${ESC}[0m (ST)`,
  // Only escape sequences, in the middle: the line stays and is blank (a CSI, an OSC that is not 8, an APC).
  `${ESC}[2K${ESC}]0;window title${BEL}${ESC}_Gapc payload${ST}`,
  // mailto, and a link with an id parameter and an upper-case scheme.
  `mail: ${link("mailto:dev@example.com", "dev@example.com")} upper: ${ESC}]8;id=7;HTTPS://EXAMPLE.COM/Up${BEL}upper${ESC}]8;;${BEL}`,
  // A scheme that is not http, https or mailto gets no anchor; markup in the text is escaped.
  `unsafe: ${link("javascript:alert(1)", "no anchor")} <b>&amp;</b>`,
  // Other CSI sequences and a stray ESC are dropped.
  `cursor${ESC}[5C moved${ESC}[?25l hidden ${ESC}7stray`,
  // A link that is never closed is closed at the end of its line.
  `open: ${ESC}]8;;https://example.com/open${BEL}never closed`,
  // Only escape sequences, at the end: dropped by the result trim.
  `${ESC}[0K${ESC}]8;;${BEL}`,
];

const renderResult = (_result, options) => ({
  render: () => (options.expanded ? lines : lines.slice(0, 3)),
  invalidate() {},
});

export default function (pi) {
  registerToolRenderers({
    registerTool(tool) {
      pi.registerTool({ ...(tool.name === "render_card" ? { ...tool, renderResult } : tool), executionMode: "sequential" });
    },
  });
}
