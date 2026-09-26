import { readFile, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { stripTypeScriptTypes } from "node:module";

// Specifiers Pi's extension loader serves from its own bundle
// (core/extensions/virtual-modules.ts). Pi resolves the pi-ai root to its
// compat entry point, so both names share one shim here. The pi-ai/oauth entry
// point is type-only; pi-ai/providers/all is Pi's own module. Pi's
// pi-agent-core has no shim yet; its imports still resolve through the
// extension's own node_modules.
const shims = new Map([
  ["@mariozechner/pi-coding-agent", new URL("./shims/pi-coding-agent.mjs", import.meta.url).href],
  ["@earendil-works/pi-coding-agent", new URL("./shims/pi-coding-agent.mjs", import.meta.url).href],
  ["@mariozechner/pi-tui", new URL("./shims/pi-tui.mjs", import.meta.url).href],
  ["@earendil-works/pi-tui", new URL("./shims/pi-tui.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@earendil-works/pi-ai", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai/compat", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@earendil-works/pi-ai/compat", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai/oauth", new URL("./shims/pi-ai-oauth.mjs", import.meta.url).href],
  ["@earendil-works/pi-ai/oauth", new URL("./shims/pi-ai-oauth.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai/providers/all", new URL("./shims/pi-dist/pi-ai/providers/all.js", import.meta.url).href],
  ["@earendil-works/pi-ai/providers/all", new URL("./shims/pi-dist/pi-ai/providers/all.js", import.meta.url).href],
  // Pi aliases these to the TypeBox it ships; shims/typebox*.mjs bundle the
  // same pinned release (automation/gen/vendor-typebox.sh).
  ["typebox", new URL("./shims/typebox.mjs", import.meta.url).href],
  ["typebox/value", new URL("./shims/typebox-value.mjs", import.meta.url).href],
  ["typebox/compile", new URL("./shims/typebox-compile.mjs", import.meta.url).href],
  ["@sinclair/typebox", new URL("./shims/typebox.mjs", import.meta.url).href],
  ["@sinclair/typebox/value", new URL("./shims/typebox-value.mjs", import.meta.url).href],
  ["@sinclair/typebox/compile", new URL("./shims/typebox-compile.mjs", import.meta.url).href],
]);

// TypeScript's NodeNext/Node16 module resolution requires relative imports of
// TypeScript sources to be written with the emitted JavaScript extension, so a
// `.ts` file is imported as `./thing.js`. Upstream pi loads extensions through
// jiti, which resolves those specifiers back to the TypeScript source; this
// mirrors that mapping for the subprocess runtime's own loader. Only `.ts` is
// mapped because `load` below only strips types from `.ts` sources.
const emittedToSourceExtension = new Map([
  [".js", [".ts"]],
  [".mjs", [".mts"]],
  [".cjs", [".cts"]],
]);
const extensionlessCandidates = [".ts", ".mts", ".cts", ".js", ".mjs", ".cjs"];

async function existingFileURL(url) {
  try {
    return (await stat(fileURLToPath(url))).isFile();
  } catch {
    return false;
  }
}

function fallbackURLs(specifier, parentURL) {
  if (!parentURL || (!specifier.startsWith("./") && !specifier.startsWith("../"))) return [];
  const resolved = new URL(specifier, parentURL);
  const extension = [...emittedToSourceExtension.keys()].find((suffix) => resolved.pathname.endsWith(suffix));
  const suffixes = extension ? emittedToSourceExtension.get(extension) : pathExtension(resolved.pathname) ? [] : extensionlessCandidates;
  const candidates = suffixes.map((suffix) => {
    const candidate = new URL(resolved);
    candidate.pathname = extension ? resolved.pathname.slice(0, -extension.length) + suffix : resolved.pathname + suffix;
    return candidate;
  });
  if (!pathExtension(resolved.pathname)) {
    for (const suffix of extensionlessCandidates) {
      const candidate = new URL(resolved);
      candidate.pathname = `${resolved.pathname.replace(/\/$/, "")}/index${suffix}`;
      candidates.push(candidate);
    }
  }
  return candidates;
}

function pathExtension(value) {
  const name = value.slice(value.lastIndexOf("/") + 1);
  const index = name.lastIndexOf(".");
  return index > 0 ? name.slice(index) : "";
}

function isRelativeSpecifier(specifier) {
  return specifier.startsWith("./") || specifier.startsWith("../") || specifier.startsWith("/");
}

export async function resolve(specifier, context, defaultResolve) {
  const shim = shims.get(specifier);
  if (shim) {
    return { url: shim, shortCircuit: true };
  }
  try {
    return await defaultResolve(specifier, context, defaultResolve);
  } catch (err) {
    if (err?.code !== "ERR_MODULE_NOT_FOUND" || !isRelativeSpecifier(specifier)) {
      throw err;
    }
    for (const candidate of fallbackURLs(specifier, context.parentURL)) {
      if (!await existingFileURL(candidate)) continue;
      return defaultResolve(candidate.href, context, defaultResolve);
    }
    throw err;
  }
}

// Matches an import declaration carrying named bindings, with an optional
// default binding ahead of the brace: `import D, { a, b as c } from "mod"`.
const namedImportPattern = /import\s+(?:([\w$]+)\s*,\s*)?\{([^}]*)\}\s*from\s*(["'][^"']+["'])\s*;?/g;

/**
 * Drop named imports whose local binding is never referenced.
 *
 * TypeScript allows a type to be imported without the `type` keyword, so
 * `import { Value, SomeInterface } from "./x.ts"` is legal source. Node's type
 * stripping erases the interface's declaration and every annotation that used
 * it, but leaves the import specifier in place, and linking then fails with
 * "does not provide an export named ...". Upstream pi does not hit this because
 * jiti transforms through babel, which elides an imported binding that is not
 * referenced in a value position.
 *
 * Running after stripping is what makes the usage test reliable: type positions
 * are already gone, so a binding that survived only as a type no longer appears
 * anywhere else in the module. A binding is kept whenever its name still occurs
 * outside the import declarations, so the failure mode is to keep an import
 * rather than to remove a live one.
 */
function elideUnreferencedNamedImports(source) {
  const declarations = [...source.matchAll(namedImportPattern)];
  if (declarations.length === 0) return source;

  // Usage is measured against the module minus its import declarations so a
  // binding is not considered "used" by the import that introduces it.
  let body = source;
  for (const declaration of declarations) {
    body = body.replace(declaration[0], " ".repeat(declaration[0].length));
  }

  let result = source;
  for (const declaration of declarations) {
    const [text, defaultBinding, specifierList, moduleSpecifier] = declaration;
    const specifiers = specifierList
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
    if (specifiers.length === 0) continue;

    const kept = specifiers.filter((specifier) => {
      const local = specifier.split(/\s+as\s+/).pop().trim();
      return new RegExp(`\\b${local.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\b`).test(body);
    });
    if (kept.length === specifiers.length) continue;

    // An import with no remaining bindings still runs the module for its side
    // effects, so it degrades to a bare import rather than being deleted.
    const replacement =
      kept.length > 0
        ? `import ${defaultBinding ? `${defaultBinding}, ` : ""}{ ${kept.join(", ")} } from ${moduleSpecifier};`
        : defaultBinding
          ? `import ${defaultBinding} from ${moduleSpecifier};`
          : `import ${moduleSpecifier};`;
    result = result.replace(text, replacement);
  }
  return result;
}

// A module that already binds `require` itself must not be shadowed.
const declaresRequirePattern = /\b(?:const|let|var|function|class)\s+require\b|\bimport\s+[^;]*\brequire\b/;
const usesRequirePattern = /\brequire\s*\(/;

/**
 * Give a TypeScript extension a working `require`.
 *
 * Extensions are loaded as ES modules, where `require` is not defined. Upstream
 * pi loads them through jiti, which supplies CommonJS interop, so source that
 * calls `require("child_process")` runs there. The binding is declared on a
 * single line so stack-trace line numbers shift by at most one.
 */
function provideCommonJSRequire(source) {
  if (!usesRequirePattern.test(source) || declaresRequirePattern.test(source)) return source;
  return (
    `import { createRequire as __pigCreateRequire } from "node:module"; const require = __pigCreateRequire(import.meta.url);\n` +
    source
  );
}

export async function load(url, context, defaultLoad) {
  if (url.startsWith("file://") && /\.(?:ts|mts|cts)$/.test(url)) {
    const sourcePath = fileURLToPath(url);
    const raw = await readFile(sourcePath, "utf8");
    const stripped = stripTypeScriptTypes(raw, {
      mode: "transform",
      sourceMap: false,
      sourceUrl: sourcePath,
    });
    return {
      format: url.endsWith(".cts") ? "commonjs" : "module",
      source: provideCommonJSRequire(elideUnreferencedNamedImports(stripped)),
      shortCircuit: true,
    };
  }
  return defaultLoad(url, context, defaultLoad);
}
