import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

// The upstream-test inventory is the compiler-derived denominator of upstream
// test obligations: every `*.test.ts` file in the pinned upstream tree and every
// `describe`/`it`/`test` case it declares. It is the test-level analogue of the
// public-interface inventory (`extract.mjs`) and the behaviour-input inventory
// (`extract-behavior-inputs.mjs`): a mechanically regenerated fact set that a
// reviewed mapping dispositions (ported / scenario-covered / designed-out /
// divergence / pending). Each file carries a content hash so an upstream leap
// that changes a test re-opens its disposition for review.

function normalizePath(value) {
  return value.split(path.sep).join("/");
}

function sha256(value) {
  return `sha256:${crypto.createHash("sha256").update(value).digest("hex")}`;
}

// testFiles returns every *.test.ts anywhere under packages/, at any nesting
// depth. Walking the whole tree (rather than an assumed packages/<pkg>/test
// layout) is deliberate: nested workspace packages such as
// session-backends/sqlite-node keep their tests under a deeper test/ directory,
// and the denominator must never silently drop a test file. Skips node_modules
// and build output. Results are sorted for deterministic output.
function testFiles(sourceRoot) {
  const packagesDir = path.join(sourceRoot, "packages");
  if (!fs.existsSync(packagesDir)) return [];
  const files = [];
  const visit = (current) => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      if (entry.isDirectory()) {
        if (entry.name === "node_modules" || entry.name === "dist" || entry.name === "build") continue;
        visit(path.join(current, entry.name));
      } else if (entry.isFile() && entry.name.endsWith(".test.ts")) {
        files.push(path.join(current, entry.name));
      }
    }
  };
  visit(packagesDir);
  return files;
}

// calleeChain resolves the base identifier and modifier chain of a call so that
// `it`, `it.skip`, `it.each(table)(...)`, and `describe.each` all reduce to their
// base name plus ordered modifiers.
function calleeChain(call) {
  const modifiers = [];
  let expr = call.expression;
  for (;;) {
    if (ts.isPropertyAccessExpression(expr)) {
      modifiers.unshift(expr.name.text);
      expr = expr.expression;
      continue;
    }
    if (ts.isCallExpression(expr)) {
      // Unwrap the table application in it.each(table)("title", fn).
      expr = expr.expression;
      continue;
    }
    break;
  }
  return ts.isIdentifier(expr) ? { base: expr.text, modifiers } : null;
}

// titleOf extracts a stable, deterministic title from a test/describe first
// argument: literal and no-substitution template text verbatim, and the raw
// source text (minus surrounding quotes) for computed titles.
function titleOf(arg, source) {
  if (!arg) return null;
  if (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) return arg.text;
  const raw = arg.getText(source);
  return raw.replace(/^[`'"]|[`'"]$/g, "");
}

function isTitleArgument(arg) {
  if (!arg) return false;
  return ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg) || ts.isTemplateExpression(arg);
}

function isCallbackArgument(arg) {
  return Boolean(arg) && (ts.isArrowFunction(arg) || ts.isFunctionExpression(arg));
}

// extractCases walks one source file and returns its statically-declared cases
// (describe path folded into each id) plus a count of dynamic it/test sites:
// calls whose title is computed (e.g. `for (const c of table) it(c.name, ...)`)
// so the AST cannot resolve the case names without executing. Reporting them as
// a distinct count keeps the denominator from silently claiming a data-driven
// file has zero obligations.
function extractCases(relativePath, text) {
  const source = ts.createSourceFile(relativePath, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const cases = [];
  let dynamicSites = 0;

  const visit = (node, stack) => {
    if (ts.isCallExpression(node)) {
      const chain = calleeChain(node);
      if (chain && (chain.base === "describe" || chain.base === "it" || chain.base === "test")) {
        if (isTitleArgument(node.arguments[0])) {
          const title = titleOf(node.arguments[0], source);
          if (chain.base === "describe") {
            ts.forEachChild(node, (child) => visit(child, [...stack, title]));
            return;
          }
          cases.push({
            id: [...stack, title].join(" \u203a "),
            kind: chain.base,
            modifiers: chain.modifiers,
            line: source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1,
          });
        } else if (chain.base !== "describe" && isCallbackArgument(node.arguments[1])) {
          // it(computedName, () => ...): a runtime-named case. Requiring a
          // callback second argument excludes the it.each(table) table
          // application, whose sole argument is the data table.
          dynamicSites += 1;
        }
      }
    }
    ts.forEachChild(node, (child) => visit(child, stack));
  };

  visit(source, []);
  cases.sort((a, b) => a.line - b.line || a.id.localeCompare(b.id));
  return { cases, dynamicSites };
}

export function extractTestInventory({ sourceRoot, upstreamVersion }) {
  const files = [];
  let caseTotal = 0;
  let dynamicTotal = 0;
  for (const absolute of testFiles(sourceRoot)) {
    const text = fs.readFileSync(absolute, "utf8");
    const relativePath = normalizePath(path.relative(sourceRoot, absolute));
    const pkg = relativePath.split("/")[1] ?? "";
    const { cases, dynamicSites } = extractCases(relativePath, text);
    caseTotal += cases.length;
    dynamicTotal += dynamicSites;
    const entry = {
      path: relativePath,
      package: pkg,
      sha256: sha256(text),
      caseCount: cases.length,
      cases,
    };
    if (dynamicSites > 0) entry.dynamicCaseSites = dynamicSites;
    files.push(entry);
  }
  files.sort((a, b) => a.path.localeCompare(b.path));
  return {
    upstreamVersion,
    generator: "extract-test-inventory.mjs",
    fileCount: files.length,
    caseCount: caseTotal,
    dynamicCaseSiteCount: dynamicTotal,
    files,
  };
}
