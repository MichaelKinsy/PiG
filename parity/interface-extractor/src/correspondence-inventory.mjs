import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

function normalizePath(value) {
  return value.split(path.sep).join("/");
}

function sha256(value) {
  return `sha256:${crypto.createHash("sha256").update(value).digest("hex")}`;
}

function unwrap(node) {
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

function requiredString(object, name, sourceFile) {
  const value = property(object, name);
  if (!value || !ts.isStringLiteralLike(unwrap(value))) {
    throw new Error(`${sourceFile.fileName}: setting ${name} must be a string literal`);
  }
  return unwrap(value).text;
}

function stringOrExpression(node, sourceFile) {
  const value = unwrap(node);
  if (ts.isStringLiteralLike(value) || ts.isNoSubstitutionTemplateLiteral(value)) {
    return { value: value.text, expression: "" };
  }
  return { value: "", expression: value.getText(sourceFile) };
}

function stringListOrExpression(node, sourceFile) {
  const value = unwrap(node);
  if (ts.isArrayLiteralExpression(value)) {
    const items = [];
    for (const element of value.elements) {
      const item = unwrap(element);
      if (!ts.isStringLiteralLike(item)) return { values: [], valuesExpression: value.getText(sourceFile) };
      items.push(item.text);
    }
    return { values: items, valuesExpression: "" };
  }
  return { values: [], valuesExpression: value.getText(sourceFile) };
}

function semanticEffects(node, sourceFile) {
  const calls = new Set();
  const writes = new Set();
  const reads = new Set();
  const visitWritesAndCalls = (candidate) => {
    if (ts.isCallExpression(candidate) || ts.isNewExpression(candidate)) calls.add(candidate.expression.getText(sourceFile));
    if (
      ts.isBinaryExpression(candidate) &&
      candidate.operatorToken.kind >= ts.SyntaxKind.FirstAssignment &&
      candidate.operatorToken.kind <= ts.SyntaxKind.LastAssignment &&
      (ts.isIdentifier(unwrap(candidate.left)) || ts.isPropertyAccessExpression(unwrap(candidate.left)) || ts.isElementAccessExpression(unwrap(candidate.left)))
    ) writes.add(unwrap(candidate.left).getText(sourceFile));
    if (
      (ts.isPrefixUnaryExpression(candidate) || ts.isPostfixUnaryExpression(candidate)) &&
      (ts.isIdentifier(unwrap(candidate.operand)) || ts.isPropertyAccessExpression(unwrap(candidate.operand)) || ts.isElementAccessExpression(unwrap(candidate.operand)))
    ) {
      writes.add(unwrap(candidate.operand).getText(sourceFile));
    }
    ts.forEachChild(candidate, visitWritesAndCalls);
  };
  visitWritesAndCalls(node);
  const visitReads = (candidate) => {
    const writeTarget =
      (ts.isBinaryExpression(candidate.parent) && unwrap(candidate.parent.left) === candidate && candidate.parent.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && candidate.parent.operatorToken.kind <= ts.SyntaxKind.LastAssignment) ||
      ((ts.isPrefixUnaryExpression(candidate.parent) || ts.isPostfixUnaryExpression(candidate.parent)) && unwrap(candidate.parent.operand) === candidate);
    if (ts.isPropertyAccessExpression(candidate) && !writeTarget) {
      reads.add(candidate.getText(sourceFile));
    }
    ts.forEachChild(candidate, visitReads);
  };
  visitReads(node);
  return {
    reads: [...reads].sort(),
    writes: [...writes].sort(),
    calls: [...calls].sort(),
  };
}

function settingItem(node, sourceFile, relativePath, gate) {
  const object = unwrap(node);
  if (!ts.isObjectLiteralExpression(object)) throw new Error(`${relativePath}: setting item is not an object literal`);
  const id = requiredString(object, "id", sourceFile);
  const label = requiredString(object, "label", sourceFile);
  const descriptionNode = property(object, "description");
  const currentValue = property(object, "currentValue");
  const valuesNode = property(object, "values");
  const submenuNode = property(object, "submenu");
  if (!descriptionNode || !currentValue || Boolean(valuesNode) === Boolean(submenuNode)) {
    throw new Error(`${relativePath}: setting ${id} must have description, currentValue, and exactly one of values or submenu`);
  }
  const description = stringOrExpression(descriptionNode, sourceFile);
  const values = valuesNode ? stringListOrExpression(valuesNode, sourceFile) : { values: [], valuesExpression: "" };
  const currentEffects = semanticEffects(currentValue, sourceFile);
  const source = object.getText(sourceFile);
  return {
    id,
    label,
    description: description.value,
    descriptionExpression: description.expression,
    currentValueExpression: unwrap(currentValue).getText(sourceFile),
    currentReads: currentEffects.reads,
    currentWrites: currentEffects.writes,
    currentCalls: currentEffects.calls,
    values: values.values,
    valuesExpression: values.valuesExpression,
    submenuExpression: submenuNode ? unwrap(submenuNode).getText(sourceFile) : "",
    gate,
    positionExpression: "",
    path: relativePath,
    startLine: sourceFile.getLineAndCharacterOfPosition(object.getStart(sourceFile)).line + 1,
    endLine: sourceFile.getLineAndCharacterOfPosition(object.getEnd()).line + 1,
    sourceHash: sha256(source),
  };
}

function enclosingGate(node, stop, sourceFile) {
  const gates = [];
  for (let current = node.parent; current && current !== stop; current = current.parent) {
    if (ts.isIfStatement(current)) gates.push(current.expression.getText(sourceFile));
  }
  gates.reverse();
  return gates.join(" && ");
}

function findSettingsSelector(sourceFile) {
  let found;
  const visit = (node) => {
    if (ts.isClassDeclaration(node) && node.name?.text === "SettingsSelectorComponent") found = node;
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  if (!found) throw new Error(`${sourceFile.fileName}: SettingsSelectorComponent not found`);
  return found;
}

function findConstructor(classNode) {
  const constructor = classNode.members.find(ts.isConstructorDeclaration);
  if (!constructor?.body) throw new Error(`${classNode.getSourceFile().fileName}: SettingsSelectorComponent constructor not found`);
  return constructor;
}

function initialSettingsArray(constructor, sourceFile) {
  let array;
  const visit = (node) => {
    if (
      ts.isVariableDeclaration(node) &&
      ts.isIdentifier(node.name) &&
      node.name.text === "items" &&
      node.initializer &&
      ts.isArrayLiteralExpression(unwrap(node.initializer))
    ) array = unwrap(node.initializer);
    ts.forEachChild(node, visit);
  };
  visit(constructor.body);
  if (!array) throw new Error(`${sourceFile.fileName}: SettingsSelectorComponent items array not found`);
  return array;
}

function insertionAnchors(constructor, sourceFile) {
  const anchors = new Map();
  const visit = (node) => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer && ts.isCallExpression(unwrap(node.initializer))) {
      const call = unwrap(node.initializer);
      if (ts.isPropertyAccessExpression(call.expression) && call.expression.expression.getText(sourceFile) === "items" && call.expression.name.text === "findIndex") {
        const callback = call.arguments[0];
        if (!callback || !ts.isArrowFunction(callback)) throw new Error(`${sourceFile.fileName}: items.findIndex lacks arrow callback`);
        let anchor = "";
        const scan = (candidate) => {
          if (
            ts.isBinaryExpression(candidate) &&
            candidate.operatorToken.kind === ts.SyntaxKind.EqualsEqualsEqualsToken &&
            ts.isPropertyAccessExpression(candidate.left) &&
            candidate.left.name.text === "id" &&
            ts.isStringLiteralLike(candidate.right)
          ) anchor = candidate.right.text;
          ts.forEachChild(candidate, scan);
        };
        scan(callback.body);
        if (!anchor) throw new Error(`${sourceFile.fileName}: unsupported items.findIndex ${callback.getText(sourceFile)}`);
        anchors.set(node.name.text, anchor);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(constructor.body);
  return anchors;
}

function settingsInsertions(constructor, sourceFile, relativePath, anchors) {
  const insertions = [];
  const visit = (node) => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.expression.getText(sourceFile) === "items" && node.expression.name.text === "splice") {
      if (node.arguments.length !== 3 || node.arguments[1].getText(sourceFile) !== "0") {
        throw new Error(`${relativePath}: unsupported items.splice ${node.getText(sourceFile)}`);
      }
      const position = unwrap(node.arguments[0]);
      let anchor = "";
      let index = -1;
      let indexExpression = "";
      if (ts.isNumericLiteral(position)) {
        index = Number(position.text);
      } else if (
        ts.isConditionalExpression(position) &&
        ts.isNumericLiteral(unwrap(position.whenTrue)) &&
        ts.isNumericLiteral(unwrap(position.whenFalse))
      ) {
        index = Number(unwrap(position.whenTrue).text);
        indexExpression = position.getText(sourceFile);
      } else if (ts.isBinaryExpression(position) && position.operatorToken.kind === ts.SyntaxKind.PlusToken) {
        const anchorIndex = position.left;
        const offset = position.right;
        if (!ts.isIdentifier(anchorIndex) || !ts.isNumericLiteral(offset) || offset.text !== "1") {
          throw new Error(`${relativePath}: unsupported items.splice index ${node.getText(sourceFile)}`);
        }
        anchor = anchors.get(anchorIndex.text) ?? "";
        if (!anchor) throw new Error(`${relativePath}: items.splice uses unknown anchor ${anchorIndex.text}`);
      } else {
        throw new Error(`${relativePath}: unsupported items.splice index ${node.getText(sourceFile)}`);
      }
      const item = settingItem(node.arguments[2], sourceFile, relativePath, enclosingGate(node, constructor, sourceFile));
      if (indexExpression) item.positionExpression = indexExpression;
      insertions.push({ anchor, index, item });
    }
    ts.forEachChild(node, visit);
  };
  visit(constructor.body);
  return insertions;
}

function callbackCases(constructor, sourceFile) {
  const callbacks = [];
  const visit = (node) => {
    if (ts.isCaseClause(node) && node.expression && ts.isStringLiteralLike(node.expression)) {
      callbacks.push({ id: node.expression.text, ...semanticEffects(node, sourceFile) });
    }
    ts.forEachChild(node, visit);
  };
  visit(constructor.body);
  return callbacks.sort((left, right) => left.id.localeCompare(right.id));
}

function functionLikeObjectProperty(object, name, sourceFile) {
  const initializer = property(object, name);
  const value = initializer && unwrap(initializer);
  if (!value || (!ts.isArrowFunction(value) && !ts.isFunctionExpression(value))) {
    throw new Error(`${sourceFile.fileName}: production callback ${name} is missing or is not a function`);
  }
  return value;
}

function settingsProductionCallbacks(sourceRoot, settingsTable) {
  const relativePath = "packages/coding-agent/src/modes/interactive/interactive-mode.ts";
  const file = path.join(sourceRoot, relativePath);
  const source = fs.readFileSync(file, "utf8");
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  let method;
  let callbacks;
  const visit = (node) => {
    if (ts.isClassDeclaration(node) && node.name?.text === "InteractiveMode") {
      method = node.members.find((member) => ts.isMethodDeclaration(member) && ts.isIdentifier(member.name) && member.name.text === "showSettingsSelector");
    }
    if (method && ts.isNewExpression(node) && node.expression.getText(sourceFile) === "SettingsSelectorComponent") {
      const candidate = node.arguments?.[1] && unwrap(node.arguments[1]);
      if (!candidate || !ts.isObjectLiteralExpression(candidate)) throw new Error(`${relativePath}: SettingsSelectorComponent callbacks object not found`);
      callbacks = candidate;
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  if (!method?.body || !callbacks) throw new Error(`${relativePath}: showSettingsSelector production callbacks not found`);
  return settingsTable.items.map((item) => {
    const dispatch = settingsTable.callbacks.find((candidate) => candidate.id === item.id);
    if (!dispatch) throw new Error(`${relativePath}: setting ${item.id} dispatch not found`);
    const handlers = dispatch.calls
      .filter((call) => call.startsWith("callbacks.on"))
      .map((call) => call.slice("callbacks.".length));
    if (item.id === "theme" && handlers.length === 0) handlers.push("onThemeChange");
		const uniqueHandlers = [...new Set(handlers)].sort();
		if (uniqueHandlers.length === 0) throw new Error(`${relativePath}: setting ${item.id} has no production callback`);
		const segments = uniqueHandlers.map((handlerName) => {
			const handler = functionLikeObjectProperty(callbacks, handlerName, sourceFile);
			const effects = semanticEffects(handler, sourceFile);
			return effectSegment("runtime-callback", relativePath, handler, sourceFile, effects);
		});
		return { id: item.id, handler: uniqueHandlers.join("+"), segments };
  }).sort((left, right) => left.id.localeCompare(right.id));
}

function submenuEffects(constructor, id, sourceFile) {
  let effects;
  const visit = (node) => {
    if (effects || !ts.isObjectLiteralExpression(unwrap(node))) {
      if (!effects) ts.forEachChild(node, visit);
      return;
    }
    const object = unwrap(node);
    const idNode = property(object, "id");
    const submenuNode = property(object, "submenu");
    if (idNode && submenuNode && ts.isStringLiteralLike(unwrap(idNode)) && unwrap(idNode).text === id) {
      effects = semanticEffects(submenuNode, sourceFile);
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(constructor.body);
  return effects;
}

function extractSettings(sourceRoot) {
  const relativePath = "packages/coding-agent/src/modes/interactive/components/settings-selector.ts";
  const file = path.join(sourceRoot, relativePath);
  const source = fs.readFileSync(file, "utf8");
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const selector = findSettingsSelector(sourceFile);
  const constructor = findConstructor(selector);
  const array = initialSettingsArray(constructor, sourceFile);
  const items = array.elements.map((node) => settingItem(node, sourceFile, relativePath, ""));
  const anchors = insertionAnchors(constructor, sourceFile);
  for (const insertion of settingsInsertions(constructor, sourceFile, relativePath, anchors)) {
    if (insertion.index >= 0) {
      if (insertion.index > items.length) throw new Error(`${relativePath}: insertion index ${insertion.index} exceeds item count`);
      items.splice(insertion.index, 0, insertion.item);
      continue;
    }
    const anchorIndex = items.findIndex((item) => item.id === insertion.anchor);
    if (anchorIndex < 0) throw new Error(`${relativePath}: insertion anchor ${insertion.anchor} is absent`);
    items.splice(anchorIndex + 1, 0, insertion.item);
  }
  const ids = items.map((item) => item.id);
  if (new Set(ids).size !== ids.length) throw new Error(`${relativePath}: duplicate setting id`);
  const callbackMap = new Map(callbackCases(constructor, sourceFile).map((callback) => [callback.id, callback]));
  for (const item of items) {
    if (!item.submenuExpression) continue;
    const effects = submenuEffects(constructor, item.id, sourceFile);
    if (!effects) throw new Error(`${relativePath}: setting ${item.id} submenu effects not found`);
    callbackMap.set(item.id, { id: item.id, ...effects });
  }
  for (const item of items) {
    if (!callbackMap.has(item.id)) throw new Error(`${relativePath}: setting ${item.id} has no dispatch case or submenu`);
  }
  const table = {
    id: "table:settings-selector",
    path: relativePath,
    owner: "SettingsSelectorComponent",
    orderProfile: "all-capabilities",
    sourceHash: sha256(selector.getText(sourceFile)),
    items,
    callbacks: [...callbackMap.values()].sort((left, right) => left.id.localeCompare(right.id)),
    productionCallbacks: [],
  };
  table.productionCallbacks = settingsProductionCallbacks(sourceRoot, table);
  return table;
}

function resolveStaticString(node, declarations, resolving = new Set()) {
  const value = unwrap(node);
  if (ts.isStringLiteralLike(value) || ts.isNoSubstitutionTemplateLiteral(value)) return value.text;
  if (ts.isTemplateExpression(value)) {
    let text = value.head.text;
    for (const span of value.templateSpans) {
      const interpolation = resolveStaticString(span.expression, declarations, resolving);
      if (interpolation === undefined) return undefined;
      text += interpolation + span.literal.text;
    }
    return text;
  }
  if (ts.isBinaryExpression(value) && value.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const left = resolveStaticString(value.left, declarations, resolving);
    const right = resolveStaticString(value.right, declarations, resolving);
    return left === undefined || right === undefined ? undefined : left + right;
  }
  if (!ts.isIdentifier(value) || resolving.has(value.text)) return undefined;
  const declaration = declarations.get(value.text);
  if (!declaration?.initializer) return undefined;
  const next = new Set(resolving);
  next.add(value.text);
  return resolveStaticString(declaration.initializer, declarations, next);
}

function promptConstants(sourceRoot) {
  const paths = [
    "packages/coding-agent/src/core/compaction/compaction.ts",
    "packages/coding-agent/src/core/compaction/utils.ts",
  ];
  const constants = [];
  for (const relativePath of paths) {
    const file = path.join(sourceRoot, relativePath);
    const source = fs.readFileSync(file, "utf8");
    const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const declarations = new Map();
    for (const statement of sourceFile.statements) {
      if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue;
      for (const declaration of statement.declarationList.declarations) {
        if (ts.isIdentifier(declaration.name) && declaration.initializer) declarations.set(declaration.name.text, declaration);
      }
    }
    const visit = (node) => {
      if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && /prompt/i.test(node.name.text) && node.initializer) {
        const topLevel = ts.isVariableStatement(node.parent.parent) && ts.isSourceFile(node.parent.parent.parent);
        if (!topLevel) {
          ts.forEachChild(node, visit);
          return;
        }
        const value = resolveStaticString(node.initializer, declarations, new Set([node.name.text]));
        if (value === undefined) {
          throw new Error(`${relativePath}: prompt ${node.name.text} is not a static string`);
        }
        constants.push({
          id: `constant:${relativePath}#${node.name.text}`,
          path: relativePath,
          name: node.name.text,
          value,
          utf16Length: value.length,
          sourceHash: sha256(node.getText(sourceFile)),
          valueHash: sha256(value),
          startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
          endLine: sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1,
        });
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }
  return constants.sort((left, right) => left.id.localeCompare(right.id));
}

function functionCalls(functionNode, sourceFile) {
	return nodeCalls(functionNode.body, sourceFile);
}

function nodeCalls(root, sourceFile) {
  const calls = [];
  const conditions = [];
  const visit = (node) => {
    if (ts.isIfStatement(node)) {
      visit(node.expression);
      conditions.push(node.expression.getText(sourceFile));
      visit(node.thenStatement);
      conditions.pop();
      if (node.elseStatement) {
        conditions.push(`else:${node.expression.getText(sourceFile)}`);
        visit(node.elseStatement);
        conditions.pop();
      }
      return;
    }
    if (ts.isCallExpression(node) || ts.isNewExpression(node)) {
      let current = node.parent;
      while (ts.isParenthesizedExpression(current) || ts.isAsExpression(current) || ts.isSatisfiesExpression(current)) current = current.parent;
      calls.push({
        ordinal: calls.length + 1,
        callee: node.expression.getText(sourceFile),
        awaited: ts.isAwaitExpression(current),
        arguments: node.arguments?.map((argument) => argument.getText(sourceFile)) ?? [],
        conditions: [...conditions],
        startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
      });
    }
    ts.forEachChild(node, visit);
  };
  visit(root);
  return calls;
}

function functionTransitions(functionNode, sourceFile) {
	return nodeTransitions(functionNode.body, sourceFile);
}

function nodeTransitions(root, sourceFile) {
  const transitions = [];
  const conditions = [];
  const add = (kind, target, expression, node) => transitions.push({
    ordinal: transitions.length + 1,
    kind,
    target,
    expression,
    conditions: [...conditions],
    startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
  });
  const visit = (node) => {
    if (ts.isIfStatement(node)) {
      visit(node.expression);
      conditions.push(node.expression.getText(sourceFile));
      visit(node.thenStatement);
      conditions.pop();
      if (node.elseStatement) {
        conditions.push(`else:${node.expression.getText(sourceFile)}`);
        visit(node.elseStatement);
        conditions.pop();
      }
      return;
    }
    if (ts.isVariableDeclaration(node) && node.initializer) {
      add("bind", node.name.getText(sourceFile), node.initializer.getText(sourceFile), node);
    } else if (
      ts.isBinaryExpression(node) &&
      node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment &&
      node.operatorToken.kind <= ts.SyntaxKind.LastAssignment
    ) {
      add("update", node.left.getText(sourceFile), node.right.getText(sourceFile), node);
    } else if (ts.isReturnStatement(node)) {
      add("return", "", node.expression?.getText(sourceFile) ?? "undefined", node);
    } else if (ts.isThrowStatement(node)) {
      add("error", "", node.expression.getText(sourceFile), node);
    }
    ts.forEachChild(node, visit);
  };
  visit(root);
  return transitions;
}

function effectSegment(role, relativePath, node, sourceFile, effects = semanticEffects(node, sourceFile)) {
  const source = node.getText(sourceFile);
  return {
    role,
    path: relativePath,
    startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
    endLine: sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1,
    sourceHash: sha256(source),
    reads: effects.reads,
    writes: effects.writes,
    calls: nodeCalls(node, sourceFile),
    transitions: nodeTransitions(node, sourceFile),
  };
}

function compactionFunctions(sourceRoot) {
  const relativePath = "packages/coding-agent/src/core/compaction/compaction.ts";
  const file = path.join(sourceRoot, relativePath);
  const source = fs.readFileSync(file, "utf8");
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const wanted = new Set(["compact", "generateTurnPrefixSummary"]);
  const functions = [];
  const visit = (node) => {
    if (ts.isFunctionDeclaration(node) && node.name && node.body && wanted.has(node.name.text)) {
      const cancellationInputs = node.parameters
        .filter((parameter) => ts.isIdentifier(parameter.name) && parameter.name.text === "signal")
        .map((parameter) => parameter.name.text)
        .sort();
      functions.push({
        id: `function:${relativePath}#${node.name.text}`,
        path: relativePath,
        name: node.name.text,
        kind: "compaction",
        async: node.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword) ?? false,
        cancellationInputs,
        calls: functionCalls(node, sourceFile),
        transitions: functionTransitions(node, sourceFile),
        callers: [],
        startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
        endLine: sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1,
        sourceHash: sha256(node.getText(sourceFile)),
      });
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  if (functions.length !== wanted.size) throw new Error(`${relativePath}: compaction function extraction incomplete`);
  return functions.sort((left, right) => left.id.localeCompare(right.id));
}

function settingsManagerFunctions(sourceRoot) {
  const relativePath = "packages/coding-agent/src/core/settings-manager.ts";
  const file = path.join(sourceRoot, relativePath);
  const source = fs.readFileSync(file, "utf8");
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const wanted = new Map([
    ["deepMergeSettings", "mergeSettings"],
    ["setProjectTrusted", "setProjectTrusted"],
    ["reload", "reload"],
    ["applyOverrides", "applyOverrides"],
    ["persistScopedSettings", "persistScopedSettings"],
    ["save", "saveGlobal"],
    ["saveProjectSettings", "saveProject"],
  ]);
  const functions = [];
  const addFunction = (node, sourceName) => {
    const name = wanted.get(sourceName);
    if (!name || !node.body) return;
    functions.push({
      id: `function:${relativePath}#${sourceName}`,
      path: relativePath,
      name,
      kind: "settings-manager",
      async: node.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword) ?? false,
      cancellationInputs: [],
      calls: functionCalls(node, sourceFile),
      transitions: functionTransitions(node, sourceFile),
      callers: [],
      startLine: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
      endLine: sourceFile.getLineAndCharacterOfPosition(node.getEnd()).line + 1,
      sourceHash: sha256(node.getText(sourceFile)),
    });
  };
  const visit = (node) => {
    if (ts.isFunctionDeclaration(node) && node.name) addFunction(node, node.name.text);
    if (ts.isClassDeclaration(node) && node.name?.text === "SettingsManager") {
      for (const member of node.members) {
        if (ts.isMethodDeclaration(member) && member.name && ts.isIdentifier(member.name)) addFunction(member, member.name.text);
      }
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  if (functions.length !== wanted.size) throw new Error(`${relativePath}: settings manager function extraction incomplete`);
  return functions.sort((left, right) => left.id.localeCompare(right.id));
}

function settingsOrchestrationFunction(sourceRoot) {
  const relativePath = "packages/coding-agent/src/modes/interactive/interactive-mode.ts";
  const file = path.join(sourceRoot, relativePath);
  const source = fs.readFileSync(file, "utf8");
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  let method;
  const visit = (node) => {
    if (ts.isClassDeclaration(node) && node.name?.text === "InteractiveMode") {
      method = node.members.find((member) => ts.isMethodDeclaration(member) && ts.isIdentifier(member.name) && member.name.text === "showSettingsSelector");
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  if (!method?.body) throw new Error(`${relativePath}: showSettingsSelector function not found`);
  return {
    id: `function:${relativePath}#showSettingsSelector`,
    path: relativePath,
    name: "settingsOrchestration",
    kind: "settings-orchestration",
    async: method.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.AsyncKeyword) ?? false,
    cancellationInputs: [],
    calls: functionCalls(method, sourceFile),
    transitions: functionTransitions(method, sourceFile),
    callers: [],
    startLine: sourceFile.getLineAndCharacterOfPosition(method.getStart(sourceFile)).line + 1,
    endLine: sourceFile.getLineAndCharacterOfPosition(method.getEnd()).line + 1,
    sourceHash: sha256(method.getText(sourceFile)),
  };
}

function functionLikeName(node) {
  if (ts.isConstructorDeclaration(node)) return "constructor";
  if ((ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node)) && node.name && ts.isIdentifier(node.name)) return node.name.text;
  return "";
}

function referencedFunction(checker, expression, sourceRoot, target) {
  let symbol = checker.getSymbolAtLocation(unwrap(expression));
  if (symbol?.flags & ts.SymbolFlags.Alias) symbol = checker.getAliasedSymbol(symbol);
  const declaration = symbol?.valueDeclaration;
  return declaration && ts.isFunctionDeclaration(declaration) && declaration.name?.text === target.name &&
    fs.realpathSync(declaration.getSourceFile().fileName) === fs.realpathSync(path.join(sourceRoot, target.path));
}

function addTypeScriptFunctionCallers(sourceRoot, functions) {
  const specs = [
    { function: "compact", path: "packages/coding-agent/src/core/agent-session.ts", caller: "_runDefaultCompaction", callee: "compact" },
    { function: "generateTurnPrefixSummary", path: "packages/coding-agent/src/core/compaction/compaction.ts", caller: "compact", callee: "generateTurnPrefixSummary" },
    { function: "mergeSettings", path: "packages/coding-agent/src/core/settings-manager.ts", caller: "constructor", callee: "deepMergeSettings" },
    { function: "setProjectTrusted", path: "packages/coding-agent/src/package-manager-cli.ts", caller: "createCommandSettingsManager", callee: "settingsManager.setProjectTrusted", count: 2 },
    { function: "reload", path: "packages/coding-agent/src/core/resource-loader.ts", caller: "loadProjectTrustExtensions", callee: "this.settingsManager.reload" },
    { function: "persistScopedSettings", path: "packages/coding-agent/src/core/settings-manager.ts", caller: "save", callee: "this.persistScopedSettings" },
    { function: "saveGlobal", path: "packages/coding-agent/src/core/settings-manager.ts", caller: "setLastChangelogVersion", callee: "this.save" },
    { function: "saveProject", path: "packages/coding-agent/src/core/settings-manager.ts", caller: "updateProjectSettings", callee: "this.saveProjectSettings" },
    { function: "settingsOrchestration", path: "packages/coding-agent/src/modes/interactive/interactive-mode.ts", caller: "setupEditorSubmitHandler", callee: "this.showSettingsSelector" },
  ];
  const indexed = new Map(functions.map((fn) => [fn.name, fn]));
  for (const spec of specs) {
    const target = indexed.get(spec.function);
    if (!target) throw new Error(`TypeScript correspondence caller target ${spec.function} not found`);
    const file = path.resolve(sourceRoot, spec.path);
    const source = fs.readFileSync(file, "utf8");
    // Imported compact can be renamed or shadowed; only its resolved declaration proves the call edge.
    const program = spec.function === "compact" ? ts.createProgram([file], {
      target: ts.ScriptTarget.Latest,
      module: ts.ModuleKind.NodeNext,
      moduleResolution: ts.ModuleResolutionKind.NodeNext,
      noLib: true,
      types: [],
      noEmit: true,
    }) : undefined;
    const checker = program?.getTypeChecker();
    const sourceFile = program ? program.getSourceFile(file) : ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const matches = [];
    const visit = (node, caller = "") => {
      const nestedCaller = functionLikeName(node) || caller;
      if (nestedCaller === spec.caller && (ts.isCallExpression(node) || ts.isNewExpression(node)) &&
        (checker ? referencedFunction(checker, node.expression, sourceRoot, target) : node.expression.getText(sourceFile) === spec.callee)) {
        matches.push(node);
      }
      ts.forEachChild(node, (child) => visit(child, nestedCaller));
    };
    visit(sourceFile);
    const expected = spec.count ?? 1;
    if (matches.length !== expected) throw new Error(`${spec.path}#${spec.caller} has ${matches.length} calls to ${spec.callee}, want ${expected}`);
    for (const call of matches) {
      const expression = call.getText(sourceFile);
      target.callers.push({
        path: spec.path,
        symbol: spec.caller,
        expression,
        startLine: sourceFile.getLineAndCharacterOfPosition(call.getStart(sourceFile)).line + 1,
        sourceHash: sha256(expression),
      });
    }
  }
}

export function extractCorrespondenceInventory({ sourceRoot, upstreamVersion }) {
  if (ts.version !== "5.9.3") throw new Error(`TypeScript version skew: got ${ts.version}, want 5.9.3`);
  const functions = [...compactionFunctions(sourceRoot), ...settingsManagerFunctions(sourceRoot), settingsOrchestrationFunction(sourceRoot)].sort((left, right) => left.id.localeCompare(right.id));
  addTypeScriptFunctionCallers(sourceRoot, functions);
  return {
    source: { language: "typescript", revision: upstreamVersion },
    tables: [extractSettings(sourceRoot)],
    constants: promptConstants(sourceRoot),
    functions,
  };
}
