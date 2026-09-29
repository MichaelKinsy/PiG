import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import { semanticHash } from "./inventory.mjs";

const PACKAGE_COMMANDS = ["install", "remove", "update", "list"];
const FLAG = /^--[a-z][a-z0-9-]*$/;
const SHORT_FLAG = /^-[a-z][a-z0-9-]*$/;
const INTERNAL_COMMAND_FLAGS = new Map([
  ["runManagedNpmCi", new Set(["--ignore-scripts", "--min-release-age=0", "--omit=dev", "--include=optional", "--no-fund", "--no-audit", "--loglevel=error", "--progress=false"])],
  ["verifyManagedRelease", new Set(["--version"])],
]);

function parseFile(file) {
  const text = fs.readFileSync(file, "utf8");
  return { text, source: ts.createSourceFile(file, text, ts.ScriptTarget.ES2022, true, ts.ScriptKind.TS) };
}

function findFunction(source, name) {
  let found;
  function visit(node) {
    if ((ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node)) && node.name?.text === name) found = node;
    ts.forEachChild(node, visit);
  }
  visit(source);
  if (!found) throw new Error(`CLI adapter function ${name} not found in ${source.fileName}`);
  return found;
}

function comparedFlags(expression, variable) {
  const flags = [];
  function visit(node) {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === "includes") {
      const argument = node.arguments[0];
      if (argument && ts.isStringLiteral(argument) && (FLAG.test(argument.text) || SHORT_FLAG.test(argument.text))) {
        flags.push({ value: argument.text, node: argument });
      }
    }
    if (ts.isBinaryExpression(node) && [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.EqualsEqualsToken].includes(node.operatorToken.kind)) {
      const pairs = [[node.left, node.right], [node.right, node.left]];
      for (const [candidate, literal] of pairs) {
        if (ts.isIdentifier(candidate) && candidate.text === variable && ts.isStringLiteral(literal) && (FLAG.test(literal.text) || SHORT_FLAG.test(literal.text))) {
          flags.push({ value: literal.text, node: literal });
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(expression);
  return flags;
}

function commandRestrictions(statement) {
  const commands = new Set();
  function visit(node) {
    if (ts.isBinaryExpression(node) && [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken].includes(node.operatorToken.kind)) {
      for (const [candidate, literal] of [[node.left, node.right], [node.right, node.left]]) {
        if (ts.isIdentifier(candidate) && candidate.text === "command" && ts.isStringLiteral(literal) && PACKAGE_COMMANDS.includes(literal.text)) {
          commands.add(literal.text);
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(statement);
  return [...commands].sort();
}

function parserGroups(file, functionName, variable, scopes) {
  const { source } = parseFile(file);
  const fn = findFunction(source, functionName);
  const groups = [];
  function visit(node) {
    if (ts.isIfStatement(node)) {
      const flags = comparedFlags(node.expression, variable);
      if (flags.length) {
        const restrictions = commandRestrictions(node.thenStatement);
        const selectedScopes = restrictions.length ? restrictions.map((command) => `pi-${command}`) : scopes;
        groups.push({ flags, scopes: selectedScopes });
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(fn.body);
  return groups;
}

function lineNumber(source, node) {
  return source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
}

function recordsFromGroups(file, groups) {
  const { source } = parseFile(file);
  const records = [];
  for (const group of groups) {
    const long = group.flags.find((flag) => FLAG.test(flag.value));
    if (!long) throw new Error(`${file}:${lineNumber(source, group.flags[0].node)}: CLI alias group has no long flag`);
    const aliases = group.flags.filter((flag) => flag !== long).map((flag) => flag.value).sort();
    for (const scope of group.scopes) {
      records.push({
        id: `cli:${scope}/${long.value}`,
        command: scope,
        flag: long.value,
        aliases,
        parser: { path: file, line: lineNumber(source, long.node) },
      });
    }
  }
  return records;
}

function helpOptions(file, scopeRanges) {
  const text = fs.readFileSync(file, "utf8");
  const lines = text.split("\n");
  const options = [];
  for (const range of scopeRanges) {
    for (let index = range.start - 1; index < range.end && index < lines.length; index++) {
      const line = lines[index];
      const match = line.match(/^\s{2,}(-[a-z][a-z0-9-]*),\s+(--[a-z][a-z0-9-]*)(?:\s+(<[^>]+>|\[[^\]]+\]))?\s+/)
        ?? line.match(/^\s{2,}(--[a-z][a-z0-9-]*)(?:,\s+(-[a-z][a-z0-9-]*))?(?:\s+(<[^>]+>|\[[^\]]+\]))?\s+/);
      if (!match) continue;
      const shortFirst = SHORT_FLAG.test(match[1]);
      options.push({
        scope: range.scope,
        flag: shortFirst ? match[2] : match[1],
        alias: shortFirst ? match[1] : (match[2] ?? ""),
        value: match[3] ?? "",
        help: { path: file, line: index + 1 },
      });
    }
  }
  return options;
}

function functionRange(file, name) {
  const { source } = parseFile(file);
  const fn = findFunction(source, name);
  return {
    start: source.getLineAndCharacterOfPosition(fn.getStart(source)).line + 1,
    end: source.getLineAndCharacterOfPosition(fn.getEnd()).line + 1,
  };
}

function packageHelpRanges(file) {
  const { source } = parseFile(file);
  const fn = findFunction(source, "printPackageCommandHelp");
  const ranges = [];
  function visit(node) {
    if (ts.isCaseClause(node) && ts.isStringLiteral(node.expression) && PACKAGE_COMMANDS.includes(node.expression.text)) {
      ranges.push({
        scope: `pi-${node.expression.text}`,
        start: source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1,
        end: source.getLineAndCharacterOfPosition(node.end).line + 1,
      });
    }
    ts.forEachChild(node, visit);
  }
  visit(fn);
  return ranges;
}

function enclosingFunctionName(node) {
  for (let current = node.parent; current; current = current.parent) {
    if (ts.isFunctionDeclaration(current) || ts.isFunctionExpression(current) || ts.isMethodDeclaration(current)) {
      return current.name && ts.isIdentifier(current.name) ? current.name.text : "";
    }
  }
  return "";
}

function flagCandidates(file) {
	const { source } = parseFile(file);
	const candidates = [];
	function visit(node) {
		if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && (FLAG.test(node.text) || SHORT_FLAG.test(node.text))) {
			candidates.push({ value: node.text, line: lineNumber(source, node), file, functionName: enclosingFunctionName(node) });
		}
		ts.forEachChild(node, visit);
	}
	visit(source);
	return candidates;
}

function assertCandidatesClassified(files, records, help) {
  const classified = new Set();
  for (const record of records) {
    classified.add(`${record.parser.path}\0${record.flag}`);
    for (const alias of record.aliases) classified.add(`${record.parser.path}\0${alias}`);
  }
  for (const option of help) {
    classified.add(`${option.help.path}\0${option.flag}`);
    if (option.alias) classified.add(`${option.help.path}\0${option.alias}`);
  }
  for (const file of files) {
    for (const candidate of flagCandidates(file)) {
      if (classified.has(`${file}\0${candidate.value}`)) continue;
      if (INTERNAL_COMMAND_FLAGS.get(candidate.functionName)?.has(candidate.value)) continue;
      throw new Error(`${file}:${candidate.line}: unclassified CLI flag candidate ${candidate.value}`);
    }
  }
}

function mergeParserAndHelp(records, help, sourceRoot) {
  const helpByID = new Map();
  for (const option of help) helpByID.set(`cli:${option.scope}/${option.flag}`, option);
  const byID = new Map();
  for (const record of records) {
    if (byID.has(record.id)) throw new Error(`duplicate parsed CLI interface ${record.id}`);
    const documented = helpByID.get(record.id);
    if (!documented && record.flag !== "--help") throw new Error(`${record.id} is parsed but absent from command help`);
    const aliases = [...new Set([...record.aliases, ...(documented?.alias ? [documented.alias] : [])])].sort();
    byID.set(record.id, {
      id: record.id,
      kind: "cli-flag",
      shapeHash: semanticHash({
        kind: "cli-flag",
        command: record.command,
        flag: record.flag,
        aliases,
        value: documented?.value ?? "",
      }),
      command: record.command,
      flag: record.flag,
      aliases,
      value: documented?.value ?? "",
      parser: { path: path.relative(sourceRoot, record.parser.path).split(path.sep).join("/"), line: record.parser.line },
      help: documented ? { path: path.relative(sourceRoot, documented.help.path).split(path.sep).join("/"), line: documented.help.line } : null,
    });
  }
  for (const [id, documented] of helpByID) {
    if (!byID.has(id)) throw new Error(`${id} is documented but has no parser branch`);
    const record = byID.get(id);
    if (documented.alias && !record.aliases.includes(documented.alias)) throw new Error(`${id} help alias ${documented.alias} is not parsed`);
  }
  const aliasOwners = new Map();
  for (const record of byID.values()) {
    for (const alias of record.aliases) {
      const key = `${record.command}\0${alias}`;
      if (aliasOwners.has(key)) throw new Error(`${record.command} alias ${alias} belongs to both ${aliasOwners.get(key)} and ${record.flag}`);
      aliasOwners.set(key, record.flag);
    }
  }
  return [...byID.values()].sort((a, b) => a.id.localeCompare(b.id));
}

export function extractCLIInventory({ sourceRoot, upstreamVersion }) {
  const argsFile = path.join(sourceRoot, "packages/coding-agent/src/cli/args.ts");
  const packageFile = path.join(sourceRoot, "packages/coding-agent/src/package-manager-cli.ts");
  const mainGroups = parserGroups(argsFile, "parseArgs", "arg", ["pi"]);
  const packageGroups = parserGroups(packageFile, "parsePackageCommand", "arg", PACKAGE_COMMANDS.map((command) => `pi-${command}`));
  const configGroups = parserGroups(packageFile, "handleConfigCommand", "arg", ["pi-config"]);
  const records = [
    ...recordsFromGroups(argsFile, mainGroups),
    ...recordsFromGroups(packageFile, packageGroups),
    ...recordsFromGroups(packageFile, configGroups),
  ];
  const mainHelpRange = functionRange(argsFile, "printHelp");
  const configHelpRange = functionRange(packageFile, "printConfigCommandHelp");
  const help = [
    ...helpOptions(argsFile, [{ scope: "pi", ...mainHelpRange }]),
    ...helpOptions(packageFile, [{ scope: "pi-config", ...configHelpRange }, ...packageHelpRanges(packageFile)]),
  ];
  assertCandidatesClassified([argsFile, packageFile], records, help);
  return {
    upstreamVersion,
    kind: "cli",
    interfaces: mergeParserAndHelp(records, help, sourceRoot),
  };
}
