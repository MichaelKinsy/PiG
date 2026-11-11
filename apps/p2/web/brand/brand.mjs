// The PiG brand layer over Pi's vendored web client (D471 keeps upstream/ byte-identical to Pi): a build-time overlay of
// exact-literal patches, appended CSS and replacement assets, in the manner of automation/gen/pi-identity-patches.mjs. A Pi
// line that changed fails the build instead of shipping unpatched. Only cosmetic places are touched: no protocol string, id,
// storage key or text a test reads (the manifest's `name`, the agent's mention name "Pi", `pi-theme`, `pi.*` documents).
//   node brand.mjs apply <tree>                        patch <tree>/apps/p2 (assemble.sh does it before building)
//   node brand.mjs finish <out> <upstream version>     add the public files and the brand block to a built bundle
//   node brand.mjs check-tests <Pi checkout> <commit>  fail when a Pi test reads a text the overlay changes
//   node brand.mjs css                                 print the brand stylesheet
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const tokens = JSON.parse(readFileSync(join(here, "tokens.json"), "utf8"));
const sha = (text) => createHash("sha256").update(text).digest("hex");

/** The brand stylesheet: p2's design tokens, Elanor's semantic colours and the highlighter's, per theme, and the mark. */
export function stylesheet() {
	const theme = (name) => {
		const p = tokens[name];
		return `:root[data-theme="${name}"] {
	--bg: ${p.bg};
	--text: ${p.text};
	--muted: ${p.muted};
	--faint: ${p.faint};
	--line: ${p.line};
	--hover: ${p.soft};
	--user: ${p.soft};
	--code: ${p.code};
	--scrollbar: ${p.scrollbar};
	--ok: ${p.accentText};
	--unread: ${p.accentText};
	--working: ${p.warning};
	--bad: ${p.bad};
	--add: ${p.add};
	--add-ink: ${p.addInk};
	--del: ${p.del};
	--del-ink: ${p.delInk};
	--accent: ${p.accent};
	--accent-text: ${p.accentText};
	accent-color: ${p.accent};
	--semantic-color-background-canvas: ${p.bg};
	--semantic-color-background-surface: ${p.surface};
	--semantic-color-text-primary: ${p.text};
	--semantic-color-text-secondary: ${p.muted};
	--semantic-color-text-link: ${p.accentText};
	--semantic-color-text-code: ${p.text};
	--semantic-color-text-negative: ${p.bad};
	--semantic-color-text-warning: ${p.warning};
	--semantic-color-icon-primary: ${p.text};
	--semantic-color-icon-secondary: ${p.muted};
	--semantic-color-icon-link: ${p.accentText};
	--semantic-color-border-primary: ${p.line};
	--semantic-color-border-secondary: ${p.line};
	--semantic-color-background-fill-secondary: ${p.code};
	--semantic-color-background-fill-secondary-hover: ${p.soft};
	--semantic-color-background-fill-secondary-active: color-mix(in srgb, ${p.soft}, ${p.text} 8%);
	--semantic-color-background-fill-ghost-neutral-hover: ${p.soft};
	--semantic-color-background-fill-ghost-neutral-active: color-mix(in srgb, ${p.soft}, ${p.text} 8%);
	--semantic-color-focus-ring: ${p.accent};
}
:root[data-theme="${name}"] .hljs-keyword,
:root[data-theme="${name}"] .hljs-selector-tag,
:root[data-theme="${name}"] .hljs-built_in,
:root[data-theme="${name}"] .hljs-doctag {
	color: ${p.keyword};
}
:root[data-theme="${name}"] .hljs-string,
:root[data-theme="${name}"] .hljs-regexp,
:root[data-theme="${name}"] .hljs-addition,
:root[data-theme="${name}"] .hljs-attribute {
	color: ${p.string};
}
:root[data-theme="${name}"] .hljs-number,
:root[data-theme="${name}"] .hljs-literal,
:root[data-theme="${name}"] .hljs-symbol,
:root[data-theme="${name}"] .hljs-bullet {
	color: ${p.number};
}
:root[data-theme="${name}"] .hljs-title,
:root[data-theme="${name}"] .hljs-section,
:root[data-theme="${name}"] .hljs-name {
	color: ${p.title};
}
:root[data-theme="${name}"] .hljs-type,
:root[data-theme="${name}"] .hljs-class .hljs-title,
:root[data-theme="${name}"] .hljs-title.class_ {
	color: ${p.type};
}
:root[data-theme="${name}"] .hljs-attr,
:root[data-theme="${name}"] .hljs-variable,
:root[data-theme="${name}"] .hljs-template-variable,
:root[data-theme="${name}"] .hljs-property {
	color: ${p.attr};
}
`;
	};
	return `
/* PiG brand layer (web/brand/brand.mjs): colours from web/brand/tokens.json, the pig mark at whole multiples of its grid. */
${theme("light")}${theme("dark")}.pi-logo {
	width: 56px;
	height: 52px;
}
.hero-title .pi-logo {
	width: 28px;
	height: 26px;
}
.pi-logo.compact {
	width: 28px;
	height: 26px;
}
/* A chosen sprite (16x14 grid) is an image at whole multiples of its grid, drawn with hard pixels. */
.pi-logo.sprite {
	width: 64px;
	height: 56px;
	image-rendering: pixelated;
}
.hero-title .pi-logo.sprite {
	width: 32px;
	height: 28px;
}
.pi-logo.sprite.compact {
	width: 32px;
	height: 28px;
}
/* Reading: running text at 1.65, a step looser than the interface's 1.5; code keeps its own. */
.messages .assistant .elanor-markdown :is(p, li) {
	line-height: 1.65;
}
`;
}

const pigLogo = (() => {
	const mark = readFileSync(join(here, "assets/pig-mark.svg"), "utf8");
	const inner = /<title>PiG<\/title>(.*)<\/svg>/s.exec(mark)[1].replace(/<rect ([^>]*?) fill="(#[0-9a-f]+)"\/>/g, '<rect $1 fill="$2" />');
	const rects = inner.match(/<rect [^>]*\/>/g).map((rect) => `\t\t\t${rect}`).join("\n");
	return `import { useSyncExternalStore } from "react";

// The mark is the PiG pig. With a sprite hook on the server (pig additive (D482): PiG Standard's \`/sprite\`) the page carries the chosen
// sprite's info as JSON (data-brand-sprite: key, name; "" for none) and streams changes; the mark and the favicon then draw
// that sprite's SVGs. Without the hook the attribute is absent and nothing below runs: the mark is
// the brand's pig.
type SpriteInfo = { key: string; name: string };
const root = document.documentElement;
const listeners = new Set<() => void>();
let streaming = false;
let parsed: [string, SpriteInfo | null] = ["", null];
function spriteInfo(): SpriteInfo | null {
	const raw = root.dataset.brandSprite ?? "";
	if (parsed[0] !== raw) {
		let info: SpriteInfo | null = null;
		try {
			info = raw ? (JSON.parse(raw) as SpriteInfo) : null;
		} catch {}
		parsed = [raw, info];
	}
	return parsed[1];
}
function setSpriteInfo(raw: string): void {
	if (raw === (root.dataset.brandSprite ?? "")) return;
	root.dataset.brandSprite = raw;
	const key = spriteInfo()?.key;
	const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
	if (icon) icon.href = key ? \`/__p2_brand/sprite.svg?v=\${key}\` : "/icons/favicon.svg";
	for (const listener of listeners) listener();
}
function subscribeSprite(listener: () => void): () => void {
	listeners.add(listener);
	if (!streaming && root.dataset.brandSprite !== undefined) {
		streaming = true;
		new EventSource("/__p2_brand/sprite/events").onmessage = (event) => setSpriteInfo(String(event.data));
	}
	return () => listeners.delete(listener);
}
const useSprite = (): SpriteInfo | null => {
	useSyncExternalStore(subscribeSprite, () => root.dataset.brandSprite ?? "");
	return spriteInfo();
};

export function PiLogo({ compact = false }: { compact?: boolean }) {
	const sprite = useSprite();
	if (sprite)
		return (
			<img className={\`pi-logo sprite \${compact ? "compact" : ""}\`} src={\`/__p2_brand/sprite.svg?v=\${sprite.key}\`} alt={sprite.name} />
		);
	return (
		<svg className={\`pi-logo \${compact ? "compact" : ""}\`} viewBox="0 0 14 13" role="img" aria-label="PiG">
			<title>PiG</title>
${rects}
		</svg>
	);
}
`;
})();

/** By file: the vendored file's sha256 when it is replaced or extended (a Pi change fails the build), the whole replacement, the appended CSS, and the exact literals replaced once each. */
const files = {
	"src/web/PiLogo.tsx": { sha256: "2b29bc2437e8cc0285678c5e8ad2bb6153fe45943ce9fcc28d37e7c804edb0de", replaceWhole: pigLogo },
	"src/web/style.css": { sha256: "f8ef1492135de961a82a2e433e3a875aaf7fb562ddd12aec6be869ff4750d162", append: stylesheet },
	"plugins/chat/web.tsx": {
		literals: [
			['"Message Pi…"', '"Message PiG…"', "Message Pi…"],
			["<h2>What should Pi do?</h2>", "<h2>What should PiG do?</h2>", "What should Pi do?"],
		],
	},
	"plugins/devices/web.tsx": { literals: [["Open Pi's domain on the other device", "Open PiG's domain on the other device", "Open Pi's domain on the other device"]] },
	"plugins/terminals/Terminal.tsx": { literals: [['<span className="terminal-agent">Pi</span>', '<span className="terminal-agent">PiG</span>', 'terminal-agent">Pi<']] },
	"plugins/discord/LinkSheet.tsx": {
		literals: [
			['<span className="label">Who may wake Pi</span>', '<span className="label">Who may wake PiG</span>', "Who may wake Pi</span>"],
			// "Pick who may wake Pi." stays: test/p2/ui-discord.test.ts reads it.
		],
	},
	"src/web/transcript.ts": { literals: [[`not shared with Pi"}.\``, `not shared with PiG"}.\``, "not shared with Pi"]] },
};
function apply(tree) {
	const root = join(tree, "apps/p2");
	for (const [file, change] of Object.entries(files)) {
		const path = join(root, file);
		let text = readFileSync(path, "utf8");
		if (change.sha256 !== undefined && sha(text) !== change.sha256) throw new Error(`Pi file changed: ${file}`);
		if (change.replaceWhole !== undefined) text = change.replaceWhole;
		if (change.append) text += change.append();
		for (const [before, after] of change.literals ?? []) {
			if (text.split(before).length !== 2) throw new Error(`Pi literal changed in ${file}: ${before}`);
			text = text.replace(before, () => after);
		}
		writeFileSync(path, text);
	}
}

const publicFiles = {
	"icons/favicon.svg": "favicon.svg",
	"icons/apple-touch-icon.png": "apple-touch-icon.png",
	"icons/icon-192.png": "icon-192.png",
	"icons/icon-512.png": "icon-512.png",
};

function finish(out, upstreamVersion) {
	for (const [target, source] of Object.entries(publicFiles)) {
		mkdirSync(dirname(join(out, "public", target)), { recursive: true });
		cpSync(join(here, "assets", source), join(out, "public", target));
	}
	// The manifest keeps `name` ("Pi": test/p2/health.test.ts reads it); the short name, colours and description are PiG's.
	const manifest = JSON.parse(readFileSync(join(here, "../upstream/public/manifest.webmanifest"), "utf8"));
	manifest.short_name = tokens.name;
	manifest.description = "Self-hosted mobile home for PiG coding agents";
	manifest.background_color = tokens.themeColor.dark;
	manifest.theme_color = tokens.themeColor.dark;
	writeFileSync(join(out, "public/manifest.webmanifest"), `${JSON.stringify(manifest, null, "\t")}\n`);
	const bundlePath = join(out, "bundle.json");
	const bundle = JSON.parse(readFileSync(bundlePath, "utf8"));
	bundle.brand = {
		name: tokens.name,
		themeColor: tokens.themeColor,
		upstreamVersion,
		public: [...Object.keys(publicFiles), "manifest.webmanifest"].sort(),
	};
	writeFileSync(bundlePath, `${JSON.stringify(bundle, null, "\t")}\n`);
}

/**
 * Every overlaid Pi text must not be read by a Pi test (the overlay is for the branded build; Pi's UI tests run on the unbranded
 * one, but the overlay promises to touch nothing a test reads). Searches apps/p2/test of a Pi checkout at a commit.
 *   node brand.mjs check-tests <Pi checkout> <commit>
 */
function checkTests(pi, commit) {
	const texts = Object.entries(files).flatMap(([file, change]) =>
		(change.literals ?? []).map(([, , text]) => {
			if (text === undefined) throw new Error(`${file}: a literal names no Pi text (give the text a test might read, or null for code)`);
			return text;
		}),
	);
	const failures = [];
	for (const text of texts.filter((value) => value !== null)) {
		const found = spawnSync("git", ["-C", pi, "grep", "-n", "-F", "--", text, commit, "--", "apps/p2/test"], { encoding: "utf8" });
		if (found.status === 0) failures.push(`${JSON.stringify(text)} is read by Pi's tests:\n${found.stdout}`);
	}
	if (failures.length) throw new Error(failures.join("\n"));
	console.log(`${texts.length} overlaid texts, none read by Pi's tests at ${commit}`);
}

const [command, ...args] = process.argv.slice(2);
if (command === "apply") apply(args[0]);
else if (command === "finish") finish(args[0], args[1]);
else if (command === "check-tests") checkTests(args[0], args[1]);
else if (command === "css") process.stdout.write(stylesheet());
else if (process.argv[1] === fileURLToPath(import.meta.url)) throw new Error("usage: node brand.mjs apply <tree> | finish <out> <upstream version> | css");
