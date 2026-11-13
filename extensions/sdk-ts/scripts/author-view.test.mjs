// What an extension author's compiler sees once the project imports this package: Pi's packages as the pinned build declares them, in
// place of the installed release, with the PiG-only declarations of index.d.ts on top.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { blockNames, compare, PRIVATE, specifiers } from "./declarations.mjs";
import { ambient, codes, compile, CONFIGURATIONS, declared, defaults, discard, hoisted, LAYOUTS, project, text, TYPES, typesPackage } from "./author-view.mjs";
import { bundle } from "./pin-bundle.mjs";
import { pinnedBuild, publishedTree, sdk } from "./trees.mjs";

const NO_PROPERTY = 2339;
const NO_EXPORT = 2305;
const PRIVATE_MEMBER = 2341;

// A release and a pin that declares more: `onlyInPin` and a private member on Session, and no Manager.
function release(pin) {
  const session = pin ? "    inBoth: number;\n    onlyInPin: number;\n    private _restored;\n" : "    inBoth: number;\n";
  const manager = pin ? "" : "export declare class Manager {\n    releaseOnly: number;\n}\n";
  return new Map([
    ["@earendil-works/pi-coding-agent/package.json", JSON.stringify({ name: "@earendil-works/pi-coding-agent", version: "9.9.9", type: "module", exports: { ".": { types: "./dist/index.d.ts", import: "./dist/index.js" } } })],
    ["@earendil-works/pi-coding-agent/dist/index.d.ts", `export { Session } from "./session.js";\n${manager}`],
    ["@earendil-works/pi-coding-agent/dist/session.d.ts", `export declare class Session {\n${session}}\n`],
  ]);
}

const AUGMENTING = [
  '/// <reference path="./pi-pin.d.ts" />',
  'import "@earendil-works/pi-coding-agent";',
  'export type * from "@earendil-works/pi-coding-agent";',
  "export interface PiGOnly {\n    x: number;\n}",
  'declare module "@earendil-works/pi-coding-agent" {\n    interface Session {\n        augmented: number;\n    }\n}',
  "",
].join("\n");

const USE = 'import type { PiGOnly } from "@michaelkinsy/pig-extension-types";\nimport type { Session } from "@earendil-works/pi-coding-agent";\n';

test("an author who imports the types package compiles against the pin's declarations, in every resolution mode", (t) => {
  const packages = new Map([
    ...release(false),
    [`${TYPES}/package.json`, JSON.stringify({ name: TYPES, version: "0.0.0", type: "module", types: "./index.d.ts", exports: { ".": { types: "./index.d.ts" } } })],
    [`${TYPES}/index.d.ts`, AUGMENTING],
    [`${TYPES}/pi-pin.d.ts`, bundle(release(true), "// a pin").text],
  ]);
  const sources = new Map([
    ["pin.ts", `${USE}export function f(s: Session, p: PiGOnly): number[] {\n  return [s.inBoth, s.onlyInPin, p.x];\n}\n`],
    ["augmented.ts", `${USE}export function f(s: Session, p: PiGOnly): number[] {\n  return [s.augmented, p.x];\n}\n`],
    ["neither.ts", `${USE}export function f(s: Session, p: PiGOnly): number[] {\n  return [s.inNeither, p.x];\n}\n`],
    ["private.ts", `${USE}export function f(s: Session, p: PiGOnly): unknown[] {\n  return [s._restored, p.x];\n}\n`],
    ["release.ts", 'import type { PiGOnly } from "@michaelkinsy/pig-extension-types";\nimport type { Manager } from "@earendil-works/pi-coding-agent";\nexport type Both = PiGOnly | Manager;\n'],
    ["alone.ts", 'import type { Session } from "@earendil-works/pi-coding-agent";\nexport function f(s: Session): number {\n  return s.onlyInPin;\n}\n'],
  ]);
  const directory = project({ files: new Map([...[...packages].map(([path, text]) => [`node_modules/${path}`, text]), ...sources]) });
  t.after(() => discard(directory));
  for (const { name, options } of CONFIGURATIONS) {
    const { diagnostics } = compile(directory, ["pin.ts", "augmented.ts", "neither.ts", "private.ts", "release.ts"], options);
    const found = Object.fromEntries(["pin.ts", "augmented.ts", "neither.ts", "private.ts", "release.ts"].map((file) => [file, codes(diagnostics, file)]));
    assert.deepEqual(found, { "pin.ts": [], "augmented.ts": [], "neither.ts": [NO_PROPERTY], "private.ts": [PRIVATE_MEMBER], "release.ts": [NO_EXPORT] }, name);
    assert.deepEqual(diagnostics.filter((diagnostic) => diagnostic.file.includes("node_modules")), [], `${name}: the packages`);
    // A project that does not import the types package sees the installed release.
    assert.deepEqual(codes(compile(directory, ["alone.ts"], options).diagnostics, "alone.ts"), [NO_PROPERTY], `${name}: without the types package`);
  }
});

const EXTENSION = `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { Component } from "@earendil-works/pi-tui";
import type { PiGLoginDefinition } from "@michaelkinsy/pig-extension-types";

export default function extension(pi: ExtensionAPI): void {
  pi.on("session_start", async (_event, ctx) => {
    const login: PiGLoginDefinition = { brand: [], hero: [], mascot: [], palette: {}, name: "n", description: "d", tagline: "t" };
    await ctx.ui.setLogin(login);
  });
}

export function themed(root: Component): void {
  root.viewTheme = { accent: "#d75f00" };
}
`;

const MISTAKEN = `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { PiGLoginDefinition } from "@michaelkinsy/pig-extension-types";

export function mistaken(pi: ExtensionAPI, login: PiGLoginDefinition): void {
  pi.noSuchMember(login);
}
`;

// McpServerConfig's \`url\` is declared by @earendil-works/pi-mcp, a package the bundle imports and does not carry: the second
// assignment is an error only while that package resolves from the bundle.
const THROUGH_ANOTHER_PACKAGE = `import type { McpServerConfig } from "@earendil-works/pi-coding-agent";
import type { PiGLoginDefinition } from "@michaelkinsy/pig-extension-types";

export const login: PiGLoginDefinition | undefined = undefined;
export const typed: McpServerConfig = { url: "https://mcp.example.com/jira" };
export const wrong: McpServerConfig = { url: 1 };
`;

// The PiG-only members whose types name a Pi declaration: an unknown property is an error only while that declaration is not \`any\`.
const PIG_ONLY = `import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { PiGLoginDefinition } from "@michaelkinsy/pig-extension-types";

export function typed(pi: ExtensionAPI, login: PiGLoginDefinition): void {
  pi.registerMarkdownTransformer((markdown, context) => markdown + String(context.noSuchProperty));
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.onThemeChange((theme) => void theme.noSuchProperty);
    await ctx.ui.setLogin(login);
  });
}
`;

const RELEASE_ONLY = 'import "@earendil-works/pi-coding-agent";\nimport "@earendil-works/pi-ai";\nimport "@earendil-works/pi-tui";\nimport "@earendil-works/pi-agent-core";\n';

const SOURCES = new Map([
  ["extension.ts", EXTENSION],
  ["mistaken.ts", MISTAKEN],
  ["through.ts", THROUGH_ANOTHER_PACKAGE],
  ["pig-only.ts", PIG_ONLY],
  ["typecheck.ts", readFileSync(join(sdk, "typecheck.ts"), "utf8")],
  ["release.ts", RELEASE_ONLY],
]);

// installed writes the project of an author who installed this package and the published Pi release, in one layout: the release's
// declarations from the snapshot, this package as it is packed, and the release's other dependencies linked from the installed Pi.
function installed(layout, sources = SOURCES) {
  const build = pinnedBuild();
  const published = publishedTree();
  const mine = (name) => published.tree.has(`${name}/package.json`) || name === TYPES;
  const others = hoisted([join(build.modules, "@earendil-works", "pi-coding-agent", "node_modules"), build.modules], mine);
  const { files, links, options = {} } = LAYOUTS[layout]({ release: published.tree, types: typesPackage(), others, sources });
  return { build, published, directory: project({ files, links }), extra: options };
}

const inTypes = (file) => (diagnostic) => diagnostic.file.endsWith(`node_modules/${TYPES}/${file}`);

for (const layout of Object.keys(LAYOUTS)) {
  test(`an extension compiles against the bundle and the PiG-only declarations, with types that are not any: ${layout}`, (t) => {
    const { build, published, directory, extra } = installed(layout);
    t.after(() => discard(directory));
    const names = blockNames(build.tree);
    const added = [];
    for (const row of compare(published.tree, build.tree).public) {
      for (const change of row.members) {
        const name = change.match(/^member (\S+) \(.*\) added$/)?.[1];
        if (name) added.push({ block: names.get(row.key.split("#")[0]), declaration: row.key.split("#")[1], name });
      }
    }
    for (const { name, options: base, skipLibCheck } of CONFIGURATIONS) {
      const options = { ...base, ...extra };
      const { program, diagnostics } = compile(directory, ["extension.ts", "mistaken.ts", "through.ts", "pig-only.ts", "typecheck.ts"], options);
      const of = (file) => diagnostics.filter((diagnostic) => diagnostic.file === file).map((diagnostic) => `TS${diagnostic.code} ${diagnostic.text}`);
      assert.deepEqual(of("extension.ts"), [], name);
      assert.deepEqual(of("typecheck.ts"), [], name);
      assert.deepEqual(codes(diagnostics, "mistaken.ts"), [NO_PROPERTY], name);
      assert.deepEqual(of("through.ts"), ["TS2322 Type 'number' is not assignable to type 'string'."], `${name}: a type that comes through another package`);
      assert.deepEqual(codes(diagnostics, "pig-only.ts"), [NO_PROPERTY, NO_PROPERTY], `${name}: the PiG-only members' types`);

      // Every specifier the pinned build exposes is declared by the bundle, so none falls back to the installed release.
      const modules = ambient(program, directory);
      const elsewhere = [...specifiers(build.tree).keys()].filter((specifier) => !modules.get(specifier)?.some((file) => file.endsWith(`node_modules/${TYPES}/pi-pin.d.ts`)));
      assert.deepEqual(elsewhere, [], `${name}: specifiers not declared by pi-pin.d.ts`);

      // What the pin declares beyond the release is in the author's view.
      for (const { block, declaration, name: property } of added) {
        const type = declared(program, block, declaration);
        if (!type) t.diagnostic(`${declaration}.${property}: ${declaration} is no export of ${block}, not looked up`);
        else assert.ok(type.getProperty(property), `${name}: ${block} declares no ${declaration}.${property}`);
      }

      // With skipLibCheck off the compiler also checks the package: index.d.ts is clean, and pi-pin.d.ts reports nothing the
      // release's own files do not report.
      if (!skipLibCheck) {
        assert.deepEqual(diagnostics.filter(inTypes("index.d.ts")), [], `${name}: index.d.ts`);
        const messages = (found, wanted) => new Set(found.filter(wanted).map((diagnostic) => `TS${diagnostic.code} ${diagnostic.text}`));
        const ofRelease = messages(compile(directory, ["release.ts"], options).diagnostics, (diagnostic) => diagnostic.file.includes("node_modules/@earendil-works/"));
        const ofBundle = messages(diagnostics, inTypes("pi-pin.d.ts"));
        assert.deepEqual([...ofBundle].filter((message) => !ofRelease.has(message)), [], `${name}: diagnostics of pi-pin.d.ts the release's files do not have`);
      }
    }
    t.diagnostic(added.length > 0 ? `members the pin adds to Pi ${published.version}, seen by the author: ${added.map((entry) => `${entry.declaration}.${entry.name}`).join(", ")}` : `the pin adds no member to Pi ${published.version}`);
  });
}

test("a link to the source folder without preserveSymlinks loses the types that come through another package", (t) => {
  const { directory } = installed("linked source folder (npm install <folder>, preserveSymlinks)", new Map([["through.ts", THROUGH_ANOTHER_PACKAGE]]));
  t.after(() => discard(directory));
  const { diagnostics } = compile(directory, ["through.ts"], CONFIGURATIONS[0].options);
  assert.deepEqual([...new Set(codes(diagnostics, "through.ts"))], [2353], "the compiler resolves the bundle's imports from the folder, where no package is installed");
});

test("a model catalog of the bundle has the type the compiler gives the pinned build's JSON file", (t) => {
  const { build, directory } = installed("hoisted (npm)", new Map([["extension.ts", EXTENSION]]));
  t.after(() => discard(directory));
  const { options } = CONFIGURATIONS.find((configuration) => configuration.name.includes("Bundler") && configuration.skipLibCheck);
  const { program } = compile(directory, ["extension.ts"], options);
  const catalogs = [...ambient(program, directory).keys()].filter((specifier) => specifier.startsWith(PRIVATE) && specifier.endsWith(".json")).sort();
  assert.notEqual(catalogs.length, 0, "the bundle declares no JSON file");
  const real = catalogs.map((specifier) => {
    const path = specifier.slice(PRIVATE.length).split("/");
    return join(build.packages.get(path.slice(0, 2).join("/")).directory, ...path.slice(2));
  });
  const inferred = defaults(directory, real, { ...options, resolveJsonModule: true });
  const different = [];
  catalogs.forEach((specifier, at) => {
    const bundled = text(program, declared(program, specifier, "default"));
    let first = 0;
    while (first < bundled.length && bundled[first] === inferred[at][first]) first++;
    if (bundled !== inferred[at]) different.push(`${specifier}: the bundle declares ...${bundled.slice(Math.max(0, first - 30), first + 50)}, the JSON file is ...${inferred[at].slice(Math.max(0, first - 30), first + 50)}`);
  });
  assert.deepEqual(different, []);
});
