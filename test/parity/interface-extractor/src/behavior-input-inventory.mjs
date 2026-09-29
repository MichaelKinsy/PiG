import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

const TRACKED_PACKAGES = ["agent", "ai", "coding-agent", "tui"];
const RENDER_PRIMITIVES = new Set([
  "visibleWidth",
  "truncateToWidth",
  "sliceByWidth",
  "wrapTextWithAnsi",
  "wrapSingleLine",
  "splitIntoTokensWithAnsi",
  "breakLongWord",
  "doRender",
]);
const PRODUCTION_EFFECT_METHODS = new Set([
  "showExtensionSelector",
  "showExtensionInput",
  "showExtensionEditor",
  "flushCompactionQueue",
  "handleEvent",
  "handleKeyboardProtocolNegotiationSequence",
  "detectCapabilities",
  "setExtensionFooter",
  "_emitExtensionEvent",
]);

function isProductionEffectMethod(method, relativePath) {
  return PRODUCTION_EFFECT_METHODS.has(method) ||
    method === "refreshModels" && relativePath === "packages/coding-agent/src/modes/interactive/components/model-selector.ts" ||
    method === "dispose" && relativePath === "packages/tui/src/components/cancellable-loader.ts";
}

function normalizePath(value) {
  return value.split(path.sep).join("/");
}

function sha256(value) {
  return `sha256:${crypto.createHash("sha256").update(value).digest("hex")}`;
}

function sourceFiles(root) {
  const files = [];
  for (const packageName of TRACKED_PACKAGES) {
    const directory = path.join(root, "packages", packageName, "src");
    if (!fs.existsSync(directory)) continue;
    const visit = (current) => {
      for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
        const candidate = path.join(current, entry.name);
        if (entry.isDirectory()) visit(candidate);
        else if (entry.isFile() && candidate.endsWith(".ts") && !candidate.endsWith(".d.ts")) files.push(candidate);
      }
    };
    visit(directory);
  }
  return files.sort();
}

function propertyName(node, sourceFile) {
  if (!node.name) {
    if (ts.isVariableDeclaration(node.parent) && ts.isIdentifier(node.parent.name)) return node.parent.name.text;
    if (ts.isPropertyAssignment(node.parent) && (ts.isIdentifier(node.parent.name) || ts.isStringLiteralLike(node.parent.name))) {
      const member = node.parent.name.text;
      const object = node.parent.parent;
      const container = object.parent;
      if (ts.isObjectLiteralExpression(object) && ts.isBinaryExpression(container) && container.right === object && ts.isPropertyAccessExpression(container.left)) {
        return `${container.left.name.text}.${member}`;
      }
      if (ts.isObjectLiteralExpression(object) && ts.isVariableDeclaration(container) && container.initializer === object && ts.isIdentifier(container.name)) {
        return `${container.name.text}.${member}`;
      }
      if (ts.isObjectLiteralExpression(object) && ts.isCallExpression(container)) {
        const callOwner = container.parent;
        if (ts.isPropertyAssignment(callOwner) && (ts.isIdentifier(callOwner.name) || ts.isStringLiteralLike(callOwner.name))) {
          return `${callOwner.name.text}.${member}`;
        }
        if (ts.isVariableDeclaration(callOwner) && ts.isIdentifier(callOwner.name)) {
          return `${callOwner.name.text}.${member}`;
        }
      }
      return member;
    }
    if (ts.isBinaryExpression(node.parent) && node.parent.operatorToken.kind === ts.SyntaxKind.EqualsToken && ts.isPropertyAccessExpression(node.parent.left)) return node.parent.left.name.text;
    const line = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
    return `<anonymous@${line}>`;
  }
  if (ts.isIdentifier(node.name) || ts.isPrivateIdentifier(node.name) || ts.isStringLiteral(node.name)) return node.name.text;
  return node.name.getText(sourceFile);
}

function ownerName(node, sourceFile) {
  for (let parent = node.parent; parent; parent = parent.parent) {
    if (ts.isClassDeclaration(parent) || ts.isClassExpression(parent)) return parent.name?.text ?? "<anonymous-class>";
  }
  return "<module>";
}

function behaviorMethodName(method) {
  return method.slice(method.lastIndexOf(".") + 1);
}

function stringArgument(call, index) {
  const argument = call.arguments[index];
  return argument && ts.isStringLiteralLike(argument) ? argument.text : "";
}

function isInputParameter(node, parameterNames) {
  return ts.isIdentifier(node) && parameterNames.has(node.text);
}

function rawInputComparison(node, parameterNames) {
  if (!ts.isBinaryExpression(node)) return "";
  const equality = new Set([
    ts.SyntaxKind.EqualsEqualsEqualsToken,
    ts.SyntaxKind.ExclamationEqualsEqualsToken,
    ts.SyntaxKind.EqualsEqualsToken,
    ts.SyntaxKind.ExclamationEqualsToken,
  ]);
  if (!equality.has(node.operatorToken.kind)) return "";
  if (isInputParameter(node.left, parameterNames) && ts.isStringLiteralLike(node.right)) return JSON.stringify(node.right.text);
  if (isInputParameter(node.right, parameterNames) && ts.isStringLiteralLike(node.left)) return JSON.stringify(node.left.text);
  return "";
}

function thisTarget(node, sourceFile) {
  if (!ts.isPropertyAccessExpression(node)) return "";
  let current = node;
  const parts = [];
  while (ts.isPropertyAccessExpression(current)) {
    parts.unshift(current.name.text);
    current = current.expression;
  }
  if (current.kind !== ts.SyntaxKind.ThisKeyword) return "";
  return `this.${parts.join(".")}` || node.getText(sourceFile);
}

function add(set, value) {
  if (value) set.add(value);
}

function unwrapExpression(node) {
  let current = node;
  while (
    ts.isAsExpression(current) ||
    ts.isSatisfiesExpression(current) ||
    ts.isParenthesizedExpression(current) ||
    ts.isTypeAssertionExpression(current)
  ) current = current.expression;
  return current;
}

function property(object, name) {
  for (const candidate of object.properties) {
    if (!ts.isPropertyAssignment(candidate)) continue;
    const key = ts.isIdentifier(candidate.name) || ts.isStringLiteralLike(candidate.name) ? candidate.name.text : "";
    if (key === name) return candidate.initializer;
  }
  return undefined;
}

function platformCondition(node, platform, windowsStyle) {
  const expression = unwrapExpression(node);
  if (ts.isIdentifier(expression) && expression.text === "windowsKeybindings") return windowsStyle;
  if (!ts.isBinaryExpression(expression)) throw new Error(`unsupported keybinding platform condition ${expression.getText()}`);
  const left = expression.left.getText();
  const right = expression.right;
  if (left !== "process.platform" || !ts.isStringLiteralLike(right)) {
    throw new Error(`unsupported keybinding platform condition ${expression.getText()}`);
  }
  const equal = expression.operatorToken.kind === ts.SyntaxKind.EqualsEqualsEqualsToken || expression.operatorToken.kind === ts.SyntaxKind.EqualsEqualsToken;
  const notEqual = expression.operatorToken.kind === ts.SyntaxKind.ExclamationEqualsEqualsToken || expression.operatorToken.kind === ts.SyntaxKind.ExclamationEqualsToken;
  if (!equal && !notEqual) throw new Error(`unsupported keybinding platform operator ${expression.operatorToken.getText()}`);
  return equal ? platform === right.text : platform !== right.text;
}

function keyList(node, platform, windowsStyle = platform === "win32") {
  const expression = unwrapExpression(node);
  if (ts.isStringLiteralLike(expression)) return [expression.text];
  if (ts.isArrayLiteralExpression(expression)) {
    return expression.elements.map((element) => {
      const value = unwrapExpression(element);
      if (!ts.isStringLiteralLike(value)) throw new Error(`non-literal keybinding default ${value.getText()}`);
      return value.text;
    });
  }
  if (ts.isConditionalExpression(expression)) {
    return keyList(platformCondition(expression.condition, platform, windowsStyle) ? expression.whenTrue : expression.whenFalse, platform, windowsStyle);
  }
  throw new Error(`unsupported keybinding default ${expression.getText()}`);
}

function collectKeybindingMetadata(sourceFile, relativePath, definitions, declarations) {
  const visit = (node) => {
    if (ts.isInterfaceDeclaration(node) && (node.name.text === "Keybindings" || node.name.text === "AppKeybindings")) {
      for (const member of node.members) {
        if (!ts.isPropertySignature(member) || !member.name) continue;
        if (ts.isStringLiteralLike(member.name)) declarations.add(member.name.text);
      }
    }
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && (node.name.text === "TUI_KEYBINDINGS" || node.name.text === "KEYBINDINGS") && node.initializer) {
      const object = unwrapExpression(node.initializer);
      if (!ts.isObjectLiteralExpression(object)) throw new Error(`${node.name.text} is not an object literal`);
      for (const candidate of object.properties) {
        if (!ts.isPropertyAssignment(candidate)) continue;
        const id = ts.isStringLiteralLike(candidate.name) ? candidate.name.text : "";
        if (!id.startsWith("tui.") && !id.startsWith("app.")) continue;
        const value = unwrapExpression(candidate.initializer);
        if (!ts.isObjectLiteralExpression(value)) throw new Error(`${id} definition is not an object literal`);
        const defaults = property(value, "defaultKeys");
        if (!defaults) throw new Error(`${id} has no defaultKeys`);
        const descriptionNode = property(value, "description");
        const descriptionValue = descriptionNode ? unwrapExpression(descriptionNode) : undefined;
        if (descriptionValue && !ts.isStringLiteralLike(descriptionValue)) throw new Error(`${id} has a non-literal description`);
        const previous = definitions.get(id);
        if (previous) {
          if (previous.definitionSet === "KEYBINDINGS" && node.name.text === "TUI_KEYBINDINGS") {
            if (previous.description === "" && descriptionValue) previous.description = descriptionValue.text;
            continue;
          }
          if (!(previous.definitionSet === "TUI_KEYBINDINGS" && node.name.text === "KEYBINDINGS")) {
            throw new Error(`duplicate keybinding definition ${id}`);
          }
        }
        const startLine = sourceFile.getLineAndCharacterOfPosition(candidate.getStart(sourceFile)).line + 1;
        definitions.set(id, {
          id,
          definitionSet: node.name.text,
          defaults: {
            darwin: keyList(defaults, "darwin"),
            linux: keyList(defaults, "linux"),
            linuxWsl: keyList(defaults, "linux", true),
            win32: keyList(defaults, "win32"),
          },
          description: descriptionValue?.text ?? previous?.description ?? "",
          path: relativePath,
          line: startLine,
        });
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
}

function inspectHandler(node, sourceFile, relativePath) {
  const bindings = new Set();
  const rawInputs = new Set();
  const callbacks = new Set();
  const delegates = new Set();
  const mutations = new Set();
  const boundaryOperators = new Set();
  let branchCount = 0;
  const method = propertyName(node, sourceFile);
  const descendNested = isProductionEffectMethod(method, relativePath);
  const parameterNames = new Set(node.parameters.flatMap((parameter) => ts.isIdentifier(parameter.name) ? [parameter.name.text] : []));

  const visit = (current) => {
    if (!descendNested && current !== node.body && ts.isFunctionLike(current)) return;
    if (ts.isIfStatement(current) || ts.isCaseClause(current) || ts.isConditionalExpression(current)) branchCount++;
    if (ts.isCallExpression(current)) {
      const expression = current.expression;
      if (ts.isPropertyAccessExpression(expression) && expression.name.text === "matches") {
        add(bindings, stringArgument(current, 1));
      }
      if (ts.isPropertyAccessExpression(expression) && expression.name.text === "onAction") {
        add(bindings, stringArgument(current, 0));
      }
      if (ts.isIdentifier(expression) && expression.text === "matchesKey" && current.arguments[1]) {
        add(bindings, `matchesKey:${current.arguments[1].getText(sourceFile)}`);
      }
      if (ts.isPropertyAccessExpression(expression)) {
        const target = thisTarget(expression, sourceFile);
        if (expression.name.text === "handleInput") add(delegates, target);
        if (expression.name.text.startsWith("on") || descendNested && expression.expression.kind === ts.SyntaxKind.ThisKeyword) add(callbacks, target);
      }
    }
    add(rawInputs, rawInputComparison(current, parameterNames));
    if (ts.isBinaryExpression(current)) {
      if (current.operatorToken.kind === ts.SyntaxKind.EqualsToken) add(mutations, thisTarget(current.left, sourceFile));
      const operator = current.operatorToken.getText(sourceFile);
      if (["<", "<=", ">", ">=", "%"].includes(operator)) boundaryOperators.add(operator);
    }
    if (ts.isPrefixUnaryExpression(current) || ts.isPostfixUnaryExpression(current)) add(mutations, thisTarget(current.operand, sourceFile));
    ts.forEachChild(current, visit);
  };
  visit(node.body);

  const owner = ownerName(node, sourceFile);
  const source = node.getText(sourceFile);
  const start = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
  const end = sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1;
  return {
    id: `input:${relativePath}#${owner}.${method}`,
    path: relativePath,
    owner,
    method,
    startLine: start,
    endLine: end,
    sourceHash: sha256(source),
    async: node.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword) ?? false,
    branchCount,
    bindings: [...bindings].sort(),
    rawInputs: [...rawInputs].sort(),
    callbacks: [...callbacks].sort(),
    delegates: [...delegates].sort(),
    mutations: [...mutations].sort(),
    boundaryOperators: [...boundaryOperators].sort(),
  };
}

function fileConstants(sourceFile) {
  const constants = new Map();
  for (const statement of sourceFile.statements) {
    if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue;
    for (const declaration of statement.declarationList.declarations) {
      if (ts.isIdentifier(declaration.name) && declaration.initializer) constants.set(declaration.name.text, declaration);
    }
  }
  return constants;
}

function inspectRenderer(node, sourceFile, relativePath, constants) {
  const themeCalls = new Set();
  const layoutCalls = new Set();
  const glyphs = new Set();
  const numericLiterals = new Set();
  const identifiers = new Set();
  let branchCount = 0;
  const layoutNames = new Set(["visibleWidth", "truncateToWidth", "sliceByWidth", "wrapTextWithAnsi", "padToWidth", "repeat"]);
  const visit = (current) => {
    if (current !== node.body && ts.isFunctionLike(current)) return;
    if (ts.isIfStatement(current) || ts.isCaseClause(current) || ts.isConditionalExpression(current)) branchCount++;
    if (ts.isIdentifier(current)) identifiers.add(current.text);
    if (ts.isNumericLiteral(current)) numericLiterals.add(current.text);
    if (ts.isStringLiteralLike(current) || ts.isNoSubstitutionTemplateLiteral(current)) {
      const value = current.text;
      if (value.length <= 120 && (/[^\x20-\x7e]/u.test(value) || /^\s|\s$| {2,}|\x1b|[→←↑↓│─┌┐└┘█●○◆◇]/u.test(value))) glyphs.add(JSON.stringify(value));
    }
    if (ts.isCallExpression(current)) {
      const expression = current.expression;
      if (ts.isPropertyAccessExpression(expression)) {
        const receiver = expression.expression.getText(sourceFile);
        const name = expression.name.text;
        if (receiver.includes("theme") || receiver === "chalk") {
          const first = current.arguments[0]?.getText(sourceFile) ?? "";
          themeCalls.add(`${receiver}.${name}(${first})`);
        }
        if (layoutNames.has(name) || receiver === "Math" && ["min", "max", "floor", "ceil"].includes(name)) layoutCalls.add(`${receiver}.${name}`);
      } else if (ts.isIdentifier(expression) && layoutNames.has(expression.text)) {
        layoutCalls.add(expression.text);
      }
    }
    ts.forEachChild(current, visit);
  };
  visit(node.body);
  const dependencies = [];
  for (const name of [...identifiers].sort()) {
    const declaration = constants.get(name);
    if (!declaration) continue;
    dependencies.push({
      name,
      value: declaration.initializer.getText(sourceFile),
      line: sourceFile.getLineAndCharacterOfPosition(declaration.getStart(sourceFile)).line + 1,
    });
  }
  const owner = ownerName(node, sourceFile);
  const method = propertyName(node, sourceFile);
  const source = node.getText(sourceFile);
  return {
    id: `render:${relativePath}#${owner}.${method}`,
    path: relativePath,
    owner,
    method,
    startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
    endLine: sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1,
    sourceHash: sha256(source),
    branchCount,
    themeCalls: [...themeCalls].sort(),
    layoutCalls: [...layoutCalls].sort(),
    glyphs: [...glyphs].sort(),
    numericLiterals: [...numericLiterals].sort(),
    dependencies,
  };
}

export function extractBehaviorInputInventory({ sourceRoot, upstreamVersion }) {
  const handlers = [];
  const renderers = [];
  const definitions = new Map();
  const declarations = new Set();
  for (const file of sourceFiles(sourceRoot)) {
    const relativePath = normalizePath(path.relative(sourceRoot, file));
    const source = fs.readFileSync(file, "utf8");
    const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const constants = fileConstants(sourceFile);
    collectKeybindingMetadata(sourceFile, relativePath, definitions, declarations);
    const visit = (node) => {
      if (ts.isFunctionLike(node) && node.body) {
        const handler = inspectHandler(node, sourceFile, relativePath);
        const method = behaviorMethodName(handler.method);
        if (method === "handleInput" || handler.bindings.length > 0 || isProductionEffectMethod(method, relativePath)) handlers.push(handler);
        if (method === "render" || method.startsWith("render") || RENDER_PRIMITIVES.has(method)) {
          renderers.push(inspectRenderer(node, sourceFile, relativePath, constants));
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }
  handlers.sort((left, right) => left.id.localeCompare(right.id));
  renderers.sort((left, right) => left.id.localeCompare(right.id));
  const duplicates = handlers.filter((handler, index) => index > 0 && handler.id === handlers[index - 1].id);
  if (duplicates.length > 0) throw new Error(`duplicate input handler id ${duplicates[0].id}`);
  const duplicateRenderers = renderers.filter((renderer, index) => index > 0 && renderer.id === renderers[index - 1].id);
  if (duplicateRenderers.length > 0) throw new Error(`duplicate renderer id ${duplicateRenderers[0].id}`);
  const keybindings = [...definitions.values()].map(({ definitionSet: _definitionSet, ...entry }) => entry).sort((left, right) => left.id.localeCompare(right.id));
  const declared = [...declarations].sort();
  const defined = keybindings.map((entry) => entry.id);
  if (JSON.stringify(declared) !== JSON.stringify(defined)) {
    const missingDefinitions = declared.filter((id) => !definitions.has(id));
    const missingDeclarations = defined.filter((id) => !declarations.has(id));
    throw new Error(`keybinding declarations/definitions differ: missing definitions ${missingDefinitions}, missing declarations ${missingDeclarations}`);
  }
  const consumers = new Map(keybindings.map((entry) => [entry.id, []]));
  for (const handler of handlers) {
    for (const binding of handler.bindings) {
      if (binding.startsWith("matchesKey:")) continue;
      const targets = consumers.get(binding);
      if (!targets) throw new Error(`${handler.id} consumes undefined keybinding ${binding}`);
      targets.push(handler.id);
    }
  }
  for (const entry of keybindings) entry.consumers = consumers.get(entry.id).sort();
  return {
    upstreamVersion,
    keybindings,
    handlers,
    renderers,
  };
}
