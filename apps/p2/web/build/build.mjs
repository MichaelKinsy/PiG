// The web bundle build of Pi's apps/p2/src/hosts/bundles.ts (`buildWebBundle`), the same esbuild options and entry text, run
// by Node over the vendored tree (D471). The bundle is a build input of the Go server, not something it builds at run time.
//   node build.mjs <tree> <out directory> [plugin names, comma-separated (default: every plugin with a web facet)]
// <tree> holds apps/p2 (upstream/), packages/ (upstream-root/packages) and node_modules (npm ci of this directory's lock).
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { basename, dirname, extname, join, relative, resolve, sep } from "node:path";
import { build } from "esbuild";

const [tree, out, selected] = process.argv.slice(2);
if (!tree || !out) throw new Error("usage: node build.mjs <tree> <out directory> [plugins]");
const root = resolve(tree, "apps/p2");
const pluginsDirectory = join(root, "plugins");

const listing = readdirSync(pluginsDirectory, { withFileTypes: true })
	.filter((entry) => entry.isDirectory())
	.flatMap((entry) => {
		const file = [".ts", ".tsx"].map((extension) => join(pluginsDirectory, entry.name, `web${extension}`)).find(existsSync);
		return file ? [[entry.name, file]] : [];
	})
	.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
const plugins = selected ? selected.split(",") : listing.map(([name]) => name);
for (const name of plugins) if (!listing.some(([listed]) => listed === name)) throw new Error(`No web facet: ${name}`);
const entries = listing.filter(([name]) => plugins.includes(name));

const WEB_BUNDLE_PATH = "/__p2_web/";
const MAX_WEB_BUNDLE = 8 * 1024 * 1024;
const TYPES = {
	".js": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".map": "application/json",
	".wasm": "application/wasm",
	".woff2": "font/woff2",
};
const sourceLoaders = {
	".cjs": "js",
	".css": "css",
	".cts": "ts",
	".js": "js",
	".json": "json",
	".jsx": "jsx",
	".mjs": "js",
	".mts": "ts",
	".ts": "ts",
	".tsx": "tsx",
	".wasm": "file",
};
const inputs = new Map();
const capture = {
	name: "p2-capture-bundle-inputs",
	setup(builder) {
		builder.onLoad({ filter: /.*/ }, async (args) => {
			if (args.namespace !== "file" || args.path.includes(`${sep}node_modules${sep}`)) return;
			const loader = sourceLoaders[extname(args.path)];
			if (loader === undefined) return;
			const file = resolve(args.path);
			const contents = readFileSync(file);
			inputs.set(file, createHash("sha256").update(contents).digest("hex"));
			return { contents, loader, resolveDir: dirname(file) };
		});
	},
};
const assetUrls = {
	name: "p2-asset-urls",
	setup(builder) {
		builder.onResolve({ filter: /\?url$/ }, (args) => ({ path: resolve(args.resolveDir, args.path.slice(0, -"?url".length)) }));
	},
};
const common = {
	bundle: true,
	write: false,
	format: "esm",
	minify: true,
	legalComments: "none",
	absWorkingDir: root,
	tsconfig: join(root, "tsconfig.json"),
	logLevel: "silent",
};
const browser = {
	...common,
	platform: "browser",
	target: "es2022",
	loader: { ".wasm": "file", ".woff2": "file" },
	define: { "process.env.NODE_ENV": '"production"' },
};
const specifier = (file) => JSON.stringify(`./${relative(root, file).split(sep).join("/")}`);
const entry = [
	`import { startWeb } from ${specifier(join(root, "src/hosts/web/main.tsx"))};`,
	...entries.map(([, file], index) => `import facet${index} from ${specifier(file)};`),
	`startWeb([${entries.map(([name], index) => `[${JSON.stringify(name)}, facet${index}]`).join(",")}]);`,
].join("\n");
const result = await build({
	...browser,
	entryPoints: { app: "p2-web-entry" },
	outdir: join(root, ".p2-web"),
	entryNames: "[name]-[hash]",
	assetNames: "[name]-[hash]",
	publicPath: WEB_BUNDLE_PATH,
	plugins: [
		{
			name: "p2-web-entry",
			setup(builder) {
				builder.onResolve({ filter: /^p2-web-entry$/ }, () => ({ path: "p2-web-entry", namespace: "p2-web" }));
				builder.onLoad({ filter: /.*/, namespace: "p2-web" }, () => ({ contents: entry, resolveDir: root, loader: "ts" }));
			},
		},
		assetUrls,
		capture,
	],
	sourcemap: "linked",
});
const files = new Map();
for (const output of result.outputFiles) {
	const name = basename(output.path);
	files.set(name, { body: output.contents, type: TYPES[extname(name)] ?? "application/octet-stream" });
}
const script = [...files.keys()].find((name) => name.startsWith("app-") && name.endsWith(".js"));
if (!script) throw new Error("The web bundle has no entry script");
let size = 0;
for (const [name, file] of files) if (!name.endsWith(".map")) size += file.body.byteLength;
if (size > MAX_WEB_BUNDLE) throw new Error("Web bundle exceeds 8 MiB");
const style = [...files.keys()].find((name) => name.startsWith("app-") && name.endsWith(".css"));
const version = createHash("sha256")
	.update([...files.keys()].sort().join("\n"))
	.digest("hex");

// The view kit (plugins/views/kit/main.ts, apps/p2/docs/rooms.md): the one script a custom view's frame loads beside its bridge. Pi's
// server builds it on first request with `buildBrowserScript` (an IIFE compiled exactly like the web bundle); the Go server serves
// the bytes built here at /view-kit/<sha256>.js. It is no file of the web bundle (not in bundle.json: the bundle's version is Pi's).
let kit;
if (plugins.includes("views")) {
	const built = await build({
		...browser,
		entryPoints: [join(root, "plugins/views/kit/main.ts")],
		outdir: join(root, ".p2-script"),
		format: "iife",
		plugins: [assetUrls],
	});
	kit = built.outputFiles.find((file) => file.path.endsWith(".js"));
	if (!kit) throw new Error("The view kit compiled to no script");
}
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
const names = [...files.keys()].sort();
for (const name of names) writeFileSync(join(out, name), files.get(name).body);
if (kit) writeFileSync(join(out, "view-kit.js"), kit.contents);
// bundle.json is what the Go server embeds beside the files: the manifest's inputs (version, entry script, stylesheet), each
// file's type, and the plugins whose web facets the bundle holds.
writeFileSync(
	join(out, "bundle.json"),
	`${JSON.stringify({ version, script, style: style ?? null, plugins, files: names.map((name) => ({ name, type: files.get(name).type, size: files.get(name).body.byteLength, sha256: createHash("sha256").update(files.get(name).body).digest("hex") })) }, null, "\t")}\n`,
);
console.log(`web bundle ${version} (${names.length} files, ${size} bytes without maps), script ${script}, style ${style}`);
