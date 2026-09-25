import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

export const TRACKED_PACKAGES = ["agent", "ai", "coding-agent", "tui"];

export function semanticHash(value) {
  return `sha256:${crypto.createHash("sha256").update(JSON.stringify(value)).digest("hex")}`;
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function stableSubpath(subpath) {
  return subpath === "." ? "." : subpath.replace(/^\.\//, "");
}

function normalizePath(value) {
  return value.split(path.sep).join("/");
}

function packageKey(name) {
  const keys = {
    "@earendil-works/pi-agent-core": "agent",
    "@earendil-works/pi-ai": "ai",
    "@earendil-works/pi-coding-agent": "coding-agent",
    "@earendil-works/pi-tui": "tui",
  };
  const key = keys[name];
  if (!key) {
    throw new Error(`unsupported tracked package ${JSON.stringify(name)}`);
  }
  return key;
}

function publicTargets(pkg) {
  if (pkg.exports && typeof pkg.exports === "object") {
    const targets = [];
    for (const [subpath, raw] of Object.entries(pkg.exports)) {
      if (subpath === "./package.json") continue;
      const target = typeof raw === "string" ? raw : raw?.types ?? raw?.import;
      if (typeof target === "string") targets.push({ subpath, target });
    }
    return targets;
  }
  const target = pkg.types ?? pkg.typings ?? pkg.main;
  return typeof target === "string" ? [{ subpath: ".", target }] : [];
}

function sourceTarget(packageRoot, target) {
  let mapped = target.replace(/^\.\/dist\//, "./src/");
  mapped = mapped.replace(/\.d\.ts$/, ".ts").replace(/\.js$/, ".ts");
  if (fs.existsSync(path.resolve(packageRoot, mapped))) return mapped;
  const unbundled = mapped.replace(/^\.\/src\/bundle\//, "./src/");
  return fs.existsSync(path.resolve(packageRoot, unbundled)) ? unbundled : mapped;
}

function publishedTarget(packageRoot, target) {
  if (!target.endsWith(".js")) return target;
  const declaration = target.replace(/\.js$/, ".d.ts");
  if (fs.existsSync(path.resolve(packageRoot, declaration))) return declaration;
  const unbundled = declaration.replace(/^\.\/dist\/bundle\//, "./dist/");
  return fs.existsSync(path.resolve(packageRoot, unbundled)) ? unbundled : target;
}

function expandTarget(packageRoot, subpath, target) {
  if (!target.includes("*")) {
    const absolute = path.resolve(packageRoot, target);
    if (!fs.existsSync(absolute)) {
      throw new Error(`public entrypoint ${subpath} does not exist: ${absolute}`);
    }
    return [{ subpath, file: absolute }];
  }

  const star = target.indexOf("*");
  const prefix = target.slice(0, star);
  const suffix = target.slice(star + 1);
  const slash = prefix.lastIndexOf("/");
  const directory = path.resolve(packageRoot, prefix.slice(0, slash + 1));
  const basenamePrefix = prefix.slice(slash + 1);
  if (!fs.existsSync(directory)) {
    throw new Error(`public wildcard directory does not exist: ${directory}`);
  }
  const entries = [];
  for (const dirent of fs.readdirSync(directory, { withFileTypes: true })) {
    if (!dirent.isFile()) continue;
    if (suffix === ".ts" && dirent.name.endsWith(".d.ts")) continue;
    if (!dirent.name.startsWith(basenamePrefix) || !dirent.name.endsWith(suffix)) continue;
    const wildcard = dirent.name.slice(basenamePrefix.length, dirent.name.length - suffix.length || undefined);
    entries.push({ subpath: subpath.replace("*", wildcard), file: path.join(directory, dirent.name) });
  }
  return entries.sort((a, b) => a.subpath.localeCompare(b.subpath));
}

function resolveSourcePackages(root) {
  return TRACKED_PACKAGES.map((key) => path.join(root, "packages", key));
}

function resolvePublishedPackages(codingAgentRoot) {
  return [
    path.join(codingAgentRoot, "node_modules", "@earendil-works", "pi-agent-core"),
    path.join(codingAgentRoot, "node_modules", "@earendil-works", "pi-ai"),
    codingAgentRoot,
    path.join(codingAgentRoot, "node_modules", "@earendil-works", "pi-tui"),
  ];
}

export function resolvePackages({ origin, root }) {
  const packageRoots = origin === "source" ? resolveSourcePackages(root) : resolvePublishedPackages(root);
  return packageRoots.map((packageRoot, index) => {
    const manifestPath = path.join(packageRoot, "package.json");
    if (!fs.existsSync(manifestPath)) throw new Error(`package manifest not found: ${manifestPath}`);
    const manifest = readJSON(manifestPath);
    const key = packageKey(manifest.name);
    if (key !== TRACKED_PACKAGES[index]) {
      throw new Error(`package name mismatch at ${manifestPath}: expected ${TRACKED_PACKAGES[index]}, got ${manifest.name}`);
    }
    const entrypoints = publicTargets(manifest).flatMap(({ subpath, target }) =>
      expandTarget(packageRoot, subpath, origin === "source" ? sourceTarget(packageRoot, target) : publishedTarget(packageRoot, target)),
    );
    if (entrypoints.length === 0) throw new Error(`${manifest.name} has no public entrypoints`);
    return { key, name: manifest.name, version: manifest.version, root: packageRoot, entrypoints };
  }).sort((a, b) => a.key.localeCompare(b.key));
}

function loadProgram(entrypoints, { origin, root, dependencyRoot }) {
  const roots = [...new Set(entrypoints.map((entry) => entry.file))];
  const paths = origin === "source" ? {
    "@earendil-works/pi-agent-core": [path.join(root, "packages/agent/src/index.ts")],
    "@earendil-works/pi-agent-core/*": [path.join(root, "packages/agent/src/*.ts")],
    "@earendil-works/pi-ai": [path.join(root, "packages/ai/src/index.ts")],
    "@earendil-works/pi-ai/*": [path.join(root, "packages/ai/src/*.ts"), path.join(root, "packages/ai/src/providers/*.ts")],
    "@earendil-works/pi-tui": [path.join(root, "packages/tui/src/index.ts")],
    "@earendil-works/pi-tui/*": [path.join(root, "packages/tui/src/*.ts"), path.join(root, "packages/tui/src/components/*.ts")],
  } : undefined;
  const options = {
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.Node16,
    moduleResolution: ts.ModuleResolutionKind.Node16,
    strict: true,
    skipLibCheck: true,
    types: dependencyRoot ? ["node"] : [],
    typeRoots: dependencyRoot ? [path.join(dependencyRoot, "node_modules", "@types")] : undefined,
    allowImportingTsExtensions: true,
    noEmit: true,
    baseUrl: origin === "source" ? root : undefined,
    paths,
  };
  const host = ts.createCompilerHost(options);
  if (dependencyRoot) {
    host.resolveModuleNames = (moduleNames, containingFile) => moduleNames.map((name) => {
      const sourceResolution = ts.resolveModuleName(name, containingFile, options, host).resolvedModule;
      if (sourceResolution) return sourceResolution;
      const fallbackOptions = { ...options, baseUrl: undefined, paths: undefined };
      return ts.resolveModuleName(name, path.join(dependencyRoot, "index.d.ts"), fallbackOptions, host).resolvedModule;
    });
  }
  return ts.createProgram({ rootNames: roots, options, host });
}

function symbolKind(symbol) {
  const flags = symbol.flags;
  if (flags & ts.SymbolFlags.Class) return "class";
  if (flags & ts.SymbolFlags.Interface) return "interface";
  if (flags & ts.SymbolFlags.TypeAlias) return "type-alias";
  if (flags & ts.SymbolFlags.Enum) return "enum";
  if (flags & ts.SymbolFlags.Function) return "function";
  if (flags & ts.SymbolFlags.NamespaceModule) return "namespace";
  if (flags & ts.SymbolFlags.Variable) return "variable";
  if (flags & ts.SymbolFlags.Alias) return "alias";
  return "symbol";
}

function normalizeCompilerText(value) {
  return value.replace(/__@([A-Za-z0-9_]+)@[0-9]+/g, "[Symbol.$1]");
}

function typeString(checker, type, node) {
  return normalizeCompilerText(checker.typeToString(
    type,
    node,
    ts.TypeFormatFlags.NoTruncation |
      ts.TypeFormatFlags.UseAliasDefinedOutsideCurrentScope |
      ts.TypeFormatFlags.WriteArrowStyleSignature,
  ));
}

function typeParameters(checker, declaration) {
  return (declaration?.typeParameters ?? []).map((parameter) => ({
    name: parameter.name.text,
    constraint: parameter.constraint ? typeString(checker, checker.getTypeFromTypeNode(parameter.constraint), parameter) : "",
    default: parameter.default ? typeString(checker, checker.getTypeFromTypeNode(parameter.default), parameter) : "",
  }));
}

function signatures(checker, type, kind, node) {
  const signatureKind = kind === "construct" ? ts.SignatureKind.Construct : ts.SignatureKind.Call;
  return checker.getSignaturesOfType(type, signatureKind).filter((signature) => {
    const declaration = signature.getDeclaration();
    return !declaration?.modifiers?.some(
      (modifier) => modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword,
    );
  }).map((signature) => {
    const declaration = signature.getDeclaration();
    const minimum = signature.minArgumentCount;
    return {
      typeParameters: (signature.typeParameters ?? []).map((parameter) => ({
        name: parameter.symbol?.name ?? "",
        constraint: parameter.getConstraint() ? typeString(checker, parameter.getConstraint(), declaration) : "",
        default: parameter.getDefault() ? typeString(checker, parameter.getDefault(), declaration) : "",
      })),
      parameters: signature.parameters.map((parameter, index) => {
        const decl = parameter.valueDeclaration ?? parameter.declarations?.[0];
        const optional = index >= minimum || Boolean(parameter.flags & ts.SymbolFlags.Optional);
        let parameterType = typeString(checker, checker.getTypeOfSymbolAtLocation(parameter, decl ?? node), decl ?? node);
        if (optional) {
          parameterType = parameterType.replace(/ \| undefined$/, "");
          if (parameterType.startsWith("((") && parameterType.endsWith(")")) {
            parameterType = parameterType.slice(1, -1);
          }
        }
        return {
          name: parameter.name,
          type: parameterType,
          optional,
          rest: Boolean(decl?.dotDotDotToken),
        };
      }),
      returns: typeString(checker, signature.getReturnType(), declaration ?? node),
    };
  });
}

function propertyShape(checker, ownerType, property, node) {
  const declaration = property.valueDeclaration ?? property.declarations?.[0] ?? node;
  const type = checker.getTypeOfSymbolAtLocation(property, declaration);
  return {
    name: normalizeCompilerText(property.name),
    optional: Boolean(property.flags & ts.SymbolFlags.Optional),
    readonly: Boolean(
      declaration?.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ReadonlyKeyword) ||
        ((ts.getCheckFlags?.(property) ?? 0) & ts.CheckFlags.Readonly),
    ),
    type: typeString(checker, type, declaration),
    calls: signatures(checker, type, "call", declaration),
  };
}

function enumMembers(checker, symbol) {
  const declaration = symbol.declarations?.find(ts.isEnumDeclaration);
  if (!declaration) return [];
  return declaration.members.map((member) => ({
    name: member.name.getText(),
    value: checker.getConstantValue(member) ?? "",
  }));
}

function externalAlias(symbol) {
  for (const declaration of symbol.declarations ?? []) {
    if (!ts.isExportSpecifier(declaration)) continue;
    const exportDeclaration = declaration.parent?.parent;
    const moduleSpecifier = exportDeclaration?.moduleSpecifier;
    if (!moduleSpecifier || !ts.isStringLiteral(moduleSpecifier) || moduleSpecifier.text.startsWith(".")) continue;
    return { module: moduleSpecifier.text, name: declaration.propertyName?.text ?? declaration.name.text };
  }
  return undefined;
}

function publicProperty(property) {
  const declaration = property.valueDeclaration ?? property.declarations?.[0];
  if (declaration?.name && ts.isPrivateIdentifier(declaration.name)) return false;
  if (normalizeCompilerText(property.name).startsWith("#")) return false;
  return !declaration?.modifiers?.some(
    (modifier) => modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword,
  );
}

function canonicalShape(checker, symbol, node) {
  const external = externalAlias(symbol);
  if (external) {
    return {
      externalModule: external.module,
      externalName: external.name,
      aliasTarget: "",
      type: "",
      typeParameters: [],
      calls: [],
      constructs: [],
      properties: [],
      enumMembers: [],
    };
  }
  const resolved = symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol;
  const declaration = resolved.valueDeclaration ?? resolved.declarations?.[0] ?? node;
  const valueType = checker.getTypeOfSymbolAtLocation(resolved, declaration);
  const declaredFlags = ts.SymbolFlags.Class | ts.SymbolFlags.Interface | ts.SymbolFlags.TypeAlias | ts.SymbolFlags.Enum;
  const type = resolved.flags & declaredFlags ? checker.getDeclaredTypeOfSymbol(resolved) : valueType;
  const valueCalls = signatures(checker, valueType, "call", declaration);
  const typeCalls = signatures(checker, type, "call", declaration);
  return {
    aliasTarget: resolved === symbol ? "" : resolved.name,
    type: typeString(checker, type, declaration),
    typeParameters: typeParameters(checker, declaration),
    calls: valueCalls.length ? valueCalls : typeCalls,
    constructs: signatures(checker, valueType, "construct", declaration),
    properties: resolved.flags & (ts.SymbolFlags.Class | ts.SymbolFlags.Interface)
      ? checker.getPropertiesOfType(type).filter(publicProperty).map((property) => propertyShape(checker, type, property, declaration)).sort((a, b) => a.name.localeCompare(b.name))
      : [],
    enumMembers: enumMembers(checker, resolved),
  };
}

function childID(parentID, role, name) {
  return `${parentID}::${role}:${encodeURIComponent(String(name))}`;
}

function callIdentity(call, index) {
  const discriminator = call.parameters?.[0]?.type?.match(/^"([^"]+)"$/)?.[1];
  return discriminator ?? index;
}

function childInterface(parent, { id, name, kind, role, shape, source }) {
  return {
    id,
    parentId: parent.id,
    role,
    package: parent.package,
    entrypoint: parent.entrypoint,
    name,
    kind,
    shape,
    shapeHash: semanticHash(shape),
    source,
  };
}

function childInterfaces(checker, exported, resolved, node, root, parent, shape) {
  const declaration = resolved.valueDeclaration ?? resolved.declarations?.[0] ?? node;
  const valueType = checker.getTypeOfSymbolAtLocation(resolved, declaration);
  const declaredFlags = ts.SymbolFlags.Class | ts.SymbolFlags.Interface | ts.SymbolFlags.TypeAlias | ts.SymbolFlags.Enum;
  const type = resolved.flags & declaredFlags ? checker.getDeclaredTypeOfSymbol(resolved) : valueType;
  const children = [];

  shape.calls.forEach((call, index) => {
    children.push(childInterface(parent, {
      id: childID(parent.id, "call", callIdentity(call, index)),
      name: `${exported.name} call ${index}`,
      kind: "call-overload",
      role: "call-overload",
      shape: call,
      source: parent.source,
    }));
  });
  shape.constructs.forEach((construct, index) => {
    children.push(childInterface(parent, {
      id: childID(parent.id, "construct", index),
      name: `${exported.name} constructor ${index}`,
      kind: "construct-overload",
      role: "construct-overload",
      shape: construct,
      source: parent.source,
    }));
  });

  const properties = new Map(
    checker.getPropertiesOfType(type).filter(publicProperty).map((property) => [normalizeCompilerText(property.name), property]),
  );
  for (const property of shape.properties) {
    const propertyID = childID(parent.id, "property", property.name);
    const propertySymbol = properties.get(property.name);
    const propertySource = propertySymbol ? sourceLocation(root, propertySymbol, declaration) : parent.source;
    const propertyParent = childInterface(parent, {
      id: propertyID,
      name: `${exported.name}.${property.name}`,
      kind: "property",
      role: "property",
      shape: property,
      source: propertySource,
    });
    children.push(propertyParent);
    property.calls.forEach((call, index) => {
      children.push(childInterface(propertyParent, {
        id: childID(propertyID, "call", callIdentity(call, index)),
        name: `${exported.name}.${property.name} call ${index}`,
        kind: "call-overload",
        role: "call-overload",
        shape: call,
        source: propertySource,
      }));
    });
  }
  shape.enumMembers.forEach((member) => {
    children.push(childInterface(parent, {
      id: childID(parent.id, "enum-member", member.name),
      name: `${exported.name}.${member.name}`,
      kind: "enum-member",
      role: "enum-member",
      shape: member,
      source: parent.source,
    }));
  });
  return children;
}

export function stableSourcePath(root, fileName) {
  const absolute = path.resolve(fileName);
  const relative = path.relative(root, absolute);
  if (relative !== ".." && !relative.startsWith(`..${path.sep}`)) return normalizePath(relative);
  const normalized = normalizePath(absolute);
  const marker = "/node_modules/";
  const markerIndex = normalized.lastIndexOf(marker);
  if (markerIndex >= 0) return `node_modules/${normalized.slice(markerIndex + marker.length)}`;
  throw new Error(`declaration source escapes inventory root without a stable dependency path: ${fileName}`);
}

function sourceLocation(root, symbol, fallback) {
  const declaration = symbol.valueDeclaration ?? symbol.declarations?.[0] ?? fallback;
  const file = declaration.getSourceFile();
  const position = file.getLineAndCharacterOfPosition(declaration.getStart(file));
  return {
    path: stableSourcePath(root, file.fileName),
    line: position.line + 1,
    column: position.character + 1,
  };
}

export function extractInventory({ origin, root, upstreamVersion, packageKeys = TRACKED_PACKAGES, dependencyRoot }) {
  if (ts.version !== "5.9.3") throw new Error(`TypeScript version skew: got ${ts.version}, want 5.9.3`);
  const wanted = new Set(packageKeys);
  const packages = resolvePackages({ origin, root }).filter((pkg) => wanted.has(pkg.key));
  if (packages.length !== wanted.size) throw new Error(`requested package set was not resolved: ${[...wanted].join(",")}`);
  for (const pkg of packages) {
    if (pkg.version !== upstreamVersion) {
      throw new Error(`${pkg.name} version skew: got ${pkg.version}, want ${upstreamVersion}`);
    }
  }
  const interfaces = [];
  const seen = new Set();
  for (const pkg of packages) {
    for (const entrypoint of pkg.entrypoints) {
      if (process.env.PIG_INTERFACE_DEBUG === "1") process.stderr.write(`extract ${pkg.key} ${entrypoint.subpath}\n`);
      const program = loadProgram([entrypoint], { origin, root, dependencyRoot });
      const checker = program.getTypeChecker();
      const source = program.getSourceFile(entrypoint.file);
      if (!source) throw new Error(`TypeScript program omitted public entrypoint ${entrypoint.file}`);
      const moduleSymbol = checker.getSymbolAtLocation(source);
      if (!moduleSymbol) throw new Error(`public entrypoint has no module symbol: ${entrypoint.file}`);
      for (const exported of checker.getExportsOfModule(moduleSymbol).sort((a, b) => a.name.localeCompare(b.name))) {
        const id = `pkg:${pkg.key}/${stableSubpath(entrypoint.subpath)}#${exported.name}`;
        if (seen.has(id)) throw new Error(`duplicate stable interface ID ${id}`);
        seen.add(id);
        const external = externalAlias(exported);
        const resolved = external ? exported : (exported.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(exported) : exported);
        const shape = canonicalShape(checker, exported, source);
        const parent = {
          id,
          package: pkg.name,
          entrypoint: entrypoint.subpath,
          name: exported.name,
          kind: external ? "external" : symbolKind(resolved),
          shape,
          shapeHash: semanticHash(shape),
          source: sourceLocation(root, external ? exported : resolved, source),
        };
        interfaces.push(parent);
        for (const child of childInterfaces(checker, exported, resolved, source, root, parent, shape)) {
          if (seen.has(child.id)) throw new Error(`duplicate stable interface ID ${child.id}`);
          seen.add(child.id);
          interfaces.push(child);
        }
      }
    }
  }
  interfaces.sort((a, b) => a.id.localeCompare(b.id));
  return {
    upstreamVersion,
    typescriptVersion: ts.version,
    origin,
    packages: packages.map((pkg) => ({
      key: pkg.key,
      name: pkg.name,
      version: pkg.version,
      entrypoints: pkg.entrypoints.map((entry) => ({ subpath: entry.subpath, file: normalizePath(path.relative(root, entry.file)) })),
    })),
    interfaces,
  };
}

function walkFiles(root, suffix, out = []) {
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const file = path.join(root, entry.name);
    if (entry.isDirectory()) walkFiles(file, suffix, out);
    else if (entry.isFile() && entry.name.endsWith(suffix)) out.push(file);
  }
  return out;
}

export function verifyPublishedSources({ publishedRoot, sourceRoot, upstreamVersion }) {
  const problems = [];
  let checked = 0;
  for (const pkg of resolvePackages({ origin: "published", root: publishedRoot })) {
    if (pkg.version !== upstreamVersion) {
      problems.push(`${pkg.name}: published version ${pkg.version}, want ${upstreamVersion}`);
      continue;
    }
    const dist = path.join(pkg.root, "dist");
    for (const mapFile of walkFiles(dist, ".d.ts.map")) {
      const declarationMap = readJSON(mapFile);
      const sources = declarationMap.sources ?? [];
      const contents = declarationMap.sourcesContent ?? [];
      if (sources.length !== contents.length) {
        problems.push(`${normalizePath(path.relative(publishedRoot, mapFile))}: sourcesContent is incomplete`);
        continue;
      }
      for (let index = 0; index < sources.length; index += 1) {
        const publishedSource = path.resolve(path.dirname(mapFile), declarationMap.sourceRoot ?? "", sources[index]);
        const relative = path.relative(pkg.root, publishedSource);
        if (relative.startsWith("..") || path.isAbsolute(relative)) continue;
        const pinnedSource = path.join(sourceRoot, "packages", pkg.key, relative);
        if (!fs.existsSync(pinnedSource)) {
          problems.push(`${pkg.name}: declaration source missing from mirror: ${normalizePath(relative)}`);
          continue;
        }
        checked += 1;
        if (fs.readFileSync(pinnedSource, "utf8") !== contents[index]) {
          problems.push(`${pkg.name}: declaration source differs from mirror: ${normalizePath(relative)}`);
        }
      }
    }
  }
  if (checked === 0) problems.push("published declaration maps did not verify any pinned source files");
  return { checked, problems };
}

export function compareInventories(source, published, { compareShapes = true } = {}) {
  const sourceByID = new Map(source.interfaces.map((entry) => [entry.id, entry]));
  const publishedByID = new Map(published.interfaces.map((entry) => [entry.id, entry]));
  const problems = [];
  for (const id of [...new Set([...sourceByID.keys(), ...publishedByID.keys()])].sort()) {
    const left = sourceByID.get(id);
    const right = publishedByID.get(id);
    if (!left) problems.push(`${id}: missing from source inventory`);
    else if (!right) problems.push(`${id}: missing from published inventory`);
    else if (compareShapes && JSON.stringify(left.shape) !== JSON.stringify(right.shape)) problems.push(`${id}: source/published shape mismatch`);
  }
  return problems;
}
