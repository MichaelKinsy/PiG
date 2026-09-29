// Pi 0.87.1 Markdown.render: packages/tui/src/components/markdown.ts:303-329.
// Import the installed pinned Pi dependency, not a replacement Markdown parser.
import { Markdown } from "../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist/components/markdown.js";
const identity = (text) => text;
const theme = Object.fromEntries([
	"heading", "link", "linkUrl", "code", "codeBlock", "codeBlockBorder",
	"quote", "quoteBorder", "hr", "listBullet", "bold", "italic",
	"strikethrough", "underline",
].map((key) => [key, identity]));
console.log(JSON.stringify(new Markdown("x".repeat(64 << 10), 0, 0, theme).render(64)));
