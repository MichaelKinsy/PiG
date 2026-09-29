import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire, isBuiltin } from "node:module";
import { tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { compileFunction } from "node:vm";
import { FACET_BUNDLE_ARTIFACT_FORMAT, FACET_BUNDLE_ARTIFACT_FORMAT_VERSION, FACET_BUNDLE_FORMAT, FACET_BUNDLE_FORMAT_VERSION, FACET_BUNDLE_MANIFEST_FILE, } from "./manifest.js";
/** Read and validate a versioned facet bundle manifest. */
export async function readFacetBundleManifest(path) {
    const manifestPath = toFilePath(path);
    let parsed;
    try {
        parsed = JSON.parse(await readFile(manifestPath, "utf8"));
    }
    catch (error) {
        throw new Error(`Could not read facet bundle manifest ${manifestPath}`, { cause: error });
    }
    return validateManifest(parsed, manifestPath);
}
/** Read and verify one transportable entry from a facet bundle on disk. */
export async function readFacetBundleArtifact(options) {
    if (options.entry.length === 0)
        throw new TypeError("Facet bundle entry name must not be empty");
    const manifestPath = toFilePath(options.manifestPath);
    const manifest = await readFacetBundleManifest(manifestPath);
    const entry = manifest.entries[options.entry];
    if (entry === undefined)
        throw new Error(`Facet bundle ${manifest.plugin.id} has no entry named ${options.entry}`);
    const modulePath = resolveBundleFile(manifestPath, entry.file, "entry");
    const source = await readFile(modulePath, "utf8");
    verifySource(source, entry);
    const sourceMapContents = entry.sourceMap === undefined
        ? undefined
        : await readFile(resolveBundleFile(manifestPath, entry.sourceMap, "source map"), "utf8");
    return Object.freeze({
        format: FACET_BUNDLE_ARTIFACT_FORMAT,
        formatVersion: FACET_BUNDLE_ARTIFACT_FORMAT_VERSION,
        plugin: manifest.plugin,
        entryName: options.entry,
        entry,
        source,
        ...(sourceMapContents === undefined ? {} : { sourceMapContents }),
    });
}
/** Materialize a transported artifact and create a fresh VM-compiled CommonJS generation for each load. */
export function createFacetBundleArtifactLoader(options) {
    const artifact = validateArtifact(options.artifact);
    const temporaryParent = resolve(options.temporaryDirectory ?? tmpdir());
    return {
        async load() {
            await mkdir(temporaryParent, { recursive: true });
            const directory = await mkdtemp(join(temporaryParent, "chord-facet-"));
            try {
                await materializeArtifact(directory, artifact);
                const loaded = await createFacetBundleLoader({
                    manifestPath: join(directory, FACET_BUNDLE_MANIFEST_FILE),
                    entry: artifact.entryName,
                    resolveExternal: options.resolveExternal,
                }).load();
                let disposed = false;
                return {
                    get facets() {
                        return loaded.facets;
                    },
                    async dispose() {
                        if (disposed)
                            return;
                        disposed = true;
                        const errors = [];
                        try {
                            await loaded.dispose();
                        }
                        catch (error) {
                            errors.push(error);
                        }
                        try {
                            await rm(directory, { force: true, recursive: true });
                        }
                        catch (error) {
                            errors.push(error);
                        }
                        if (errors.length === 1)
                            throw errors[0];
                        if (errors.length > 1)
                            throw new AggregateError(errors, "Failed to dispose facet bundle artifact");
                    },
                };
            }
            catch (error) {
                try {
                    await rm(directory, { force: true, recursive: true });
                }
                catch (cleanupError) {
                    throw new AggregateError([error, cleanupError], "Facet bundle artifact loading and cleanup failed");
                }
                throw error;
            }
        },
    };
}
/** Create a reusable loader for one opaque entry in a facet bundle manifest. */
export function createFacetBundleLoader(options) {
    if (options.entry.length === 0)
        throw new TypeError("Facet bundle entry name must not be empty");
    const manifestPath = toFilePath(options.manifestPath);
    return {
        async load() {
            const manifest = await readFacetBundleManifest(manifestPath);
            const entry = manifest.entries[options.entry];
            if (entry === undefined) {
                throw new Error(`Facet bundle ${manifest.plugin.id} has no entry named ${options.entry}`);
            }
            const modulePath = resolveBundleFile(manifestPath, entry.file, "entry");
            try {
                const source = await readFile(modulePath, "utf8");
                if (options.verifyIntegrity !== false)
                    verifySource(source, entry);
                const exported = executeCommonJsModule(source, modulePath, entry.externalImports, options.resolveExternal);
                let facets = facetsFromModule(exported, manifest.plugin.id, options.entry);
                let disposed = false;
                return {
                    get facets() {
                        return facets;
                    },
                    async dispose() {
                        if (disposed)
                            return;
                        disposed = true;
                        facets = Object.freeze([]);
                    },
                };
            }
            catch (error) {
                const detail = error instanceof Error ? `: ${error.message}` : "";
                throw new Error(`Could not load facet bundle entry ${manifest.plugin.id}/${options.entry}${detail}`, {
                    cause: error,
                });
            }
        },
    };
}
function executeCommonJsModule(source, modulePath, externalImports, resolver) {
    const requireFromModule = createRequire(modulePath);
    const externalTargets = new Map();
    for (const specifier of externalImports) {
        if (!isBuiltin(specifier))
            validatePackageSpecifier(specifier);
        externalTargets.set(specifier, toRequireSpecifier(resolveExternalTarget(specifier, resolver)));
    }
    const targetFor = (specifier) => {
        const target = externalTargets.get(specifier);
        if (target === undefined)
            throw new Error(`Facet bundle required undeclared external import: ${specifier}`);
        return target;
    };
    const requireExternal = Object.assign((specifier) => requireFromModule(targetFor(specifier)), {
        resolve: (specifier) => requireFromModule.resolve(targetFor(specifier)),
    });
    const commonJsModule = { exports: Object.create(null) };
    const compiled = compileFunction(source, ["exports", "require", "module", "__filename", "__dirname"], {
        filename: modulePath,
    });
    Reflect.apply(compiled, commonJsModule.exports, [
        commonJsModule.exports,
        requireExternal,
        commonJsModule,
        modulePath,
        dirname(modulePath),
    ]);
    return commonJsModule.exports;
}
function toRequireSpecifier(target) {
    if (isAbsolute(target) || isBuiltin(target))
        return target;
    let url;
    try {
        url = new URL(target);
    }
    catch {
        return target;
    }
    if (url.protocol === "file:")
        return fileURLToPath(url);
    if (url.protocol === "node:")
        return url.href;
    throw new Error(`Facet bundle external target must be a file, package, or built-in module: ${target}`);
}
function toFilePath(path) {
    if (typeof path === "string")
        return resolve(path);
    if (path.protocol !== "file:")
        throw new TypeError(`Facet bundle manifest must be a file URL, not ${path.protocol}`);
    return fileURLToPath(path);
}
function resolveBundleFile(manifestPath, file, label) {
    if (file.length === 0 || isAbsolute(file) || basename(file) !== file || file === "." || file === "..") {
        throw new Error(`Facet bundle ${label} must be a filename relative to its manifest`);
    }
    return resolve(dirname(manifestPath), file);
}
function verifySource(source, entry) {
    const expected = parseIntegrity(entry.integrity);
    const actual = createHash("sha256").update(source, "utf8").digest("base64");
    if (actual !== expected)
        throw new Error(`Facet bundle integrity check failed for ${entry.file}`);
}
function parseIntegrity(integrity) {
    const prefix = "sha256-";
    if (!integrity.startsWith(prefix) || integrity.length === prefix.length) {
        throw new Error("Facet bundle entry has an invalid SHA-256 integrity value");
    }
    return integrity.slice(prefix.length);
}
function facetsFromModule(imported, pluginId, entryName) {
    if (!isRecord(imported))
        throw new Error(`Facet bundle entry ${pluginId}/${entryName} did not export a module`);
    const exported = imported.default;
    const candidates = Array.isArray(exported) ? exported : [exported];
    if (candidates.length === 0) {
        throw new Error(`Facet bundle entry ${pluginId}/${entryName} exported no facets`);
    }
    const facets = [];
    for (const candidate of candidates) {
        if (!isRecord(candidate) || typeof candidate.id !== "string" || candidate.id.length === 0) {
            throw new Error(`Facet bundle entry ${pluginId}/${entryName} has a facet with an invalid ID`);
        }
        if (typeof candidate.setup !== "function") {
            throw new Error(`Facet bundle entry ${pluginId}/${entryName} facet ${candidate.id} has no setup function`);
        }
        facets.push(candidate);
    }
    const ids = facets.map(({ id }) => id);
    if (new Set(ids).size !== ids.length) {
        throw new Error(`Facet bundle entry ${pluginId}/${entryName} exports duplicate facet IDs`);
    }
    return Object.freeze(facets);
}
function validateArtifact(value) {
    if (!isRecord(value) ||
        value.format !== FACET_BUNDLE_ARTIFACT_FORMAT ||
        value.formatVersion !== FACET_BUNDLE_ARTIFACT_FORMAT_VERSION ||
        typeof value.entryName !== "string" ||
        value.entryName.length === 0 ||
        typeof value.source !== "string") {
        throw new Error("Invalid facet bundle artifact");
    }
    const manifest = validateManifest({
        format: FACET_BUNDLE_FORMAT,
        formatVersion: FACET_BUNDLE_FORMAT_VERSION,
        plugin: value.plugin,
        entries: { [value.entryName]: value.entry },
    }, "facet bundle artifact");
    const entry = manifest.entries[value.entryName];
    if (entry.sourceMap === undefined) {
        if (value.sourceMapContents !== undefined) {
            throw new Error("Facet bundle artifact has source map contents without a source map");
        }
    }
    else if (typeof value.sourceMapContents !== "string") {
        throw new Error("Facet bundle artifact is missing its source map contents");
    }
    verifySource(value.source, entry);
    return Object.freeze({
        format: FACET_BUNDLE_ARTIFACT_FORMAT,
        formatVersion: FACET_BUNDLE_ARTIFACT_FORMAT_VERSION,
        plugin: manifest.plugin,
        entryName: value.entryName,
        entry,
        source: value.source,
        ...(value.sourceMapContents === undefined ? {} : { sourceMapContents: value.sourceMapContents }),
    });
}
async function materializeArtifact(directory, artifact) {
    const manifest = {
        format: FACET_BUNDLE_FORMAT,
        formatVersion: FACET_BUNDLE_FORMAT_VERSION,
        plugin: artifact.plugin,
        entries: { [artifact.entryName]: artifact.entry },
    };
    await Promise.all([
        writeFile(join(directory, artifact.entry.file), artifact.source, "utf8"),
        writeFile(join(directory, FACET_BUNDLE_MANIFEST_FILE), `${JSON.stringify(manifest, null, 2)}\n`, "utf8"),
        ...(artifact.entry.sourceMap === undefined
            ? []
            : [writeFile(join(directory, artifact.entry.sourceMap), artifact.sourceMapContents, "utf8")]),
    ]);
}
function resolveExternalTarget(specifier, resolver) {
    const resolved = resolver?.(specifier);
    if (resolved !== undefined)
        return typeof resolved === "string" ? resolved : resolved.href;
    const extension = import.meta.url.endsWith(".ts") ? "ts" : "js";
    if (specifier === "@earendil-works/chord")
        return new URL(`../index.${extension}`, import.meta.url).href;
    if (specifier === "@earendil-works/chord/context") {
        return new URL(`../context/index.${extension}`, import.meta.url).href;
    }
    if (specifier === "@earendil-works/chord/node")
        return new URL(`../node.${extension}`, import.meta.url).href;
    return import.meta.resolve(specifier);
}
function validatePackageSpecifier(specifier) {
    const segments = specifier.split("/");
    const packageParts = specifier.startsWith("@") ? 2 : 1;
    if (specifier.startsWith(".") ||
        specifier.startsWith("/") ||
        specifier.startsWith("#") ||
        specifier.includes(":") ||
        specifier.includes("\\") ||
        segments.length < packageParts ||
        segments.some((part) => part.length === 0 || part === "." || part === "..")) {
        throw new Error(`Facet bundle artifact has an unsupported external import: ${specifier}`);
    }
}
function validateManifest(value, path) {
    if (!isRecord(value) || value.format !== FACET_BUNDLE_FORMAT) {
        throw new Error(`Invalid facet bundle manifest format in ${path}`);
    }
    if (value.formatVersion !== FACET_BUNDLE_FORMAT_VERSION) {
        throw new Error(`Unsupported facet bundle manifest version in ${path}: ${String(value.formatVersion)}`);
    }
    if (!isRecord(value.plugin) || typeof value.plugin.id !== "string" || value.plugin.id.length === 0) {
        throw new Error(`Facet bundle manifest has an invalid plugin identity in ${path}`);
    }
    if (value.plugin.version !== undefined &&
        (typeof value.plugin.version !== "string" || value.plugin.version.length === 0)) {
        throw new Error(`Facet bundle manifest has an invalid plugin version in ${path}`);
    }
    if (!isRecord(value.entries) || Object.keys(value.entries).length === 0) {
        throw new Error(`Facet bundle manifest has no entries in ${path}`);
    }
    const entries = {};
    for (const [name, candidate] of Object.entries(value.entries)) {
        if (name.length === 0 || !isRecord(candidate)) {
            throw new Error(`Facet bundle manifest has an invalid entry in ${path}`);
        }
        if (typeof candidate.file !== "string")
            throw new Error(`Facet bundle entry ${name} has no file`);
        resolveBundleFile(path, candidate.file, `entry ${name}`);
        if (typeof candidate.integrity !== "string")
            throw new Error(`Facet bundle entry ${name} has no integrity`);
        parseIntegrity(candidate.integrity);
        if (!Array.isArray(candidate.externalImports) ||
            candidate.externalImports.some((item) => typeof item !== "string")) {
            throw new Error(`Facet bundle entry ${name} has invalid external imports`);
        }
        const externalImports = Object.freeze([...candidate.externalImports]);
        if (new Set(externalImports).size !== externalImports.length) {
            throw new Error(`Facet bundle entry ${name} has duplicate external imports`);
        }
        if (candidate.sourceMap !== undefined) {
            if (typeof candidate.sourceMap !== "string")
                throw new Error(`Facet bundle entry ${name} has an invalid source map`);
            resolveBundleFile(path, candidate.sourceMap, `entry ${name} source map`);
        }
        entries[name] = Object.freeze({
            file: candidate.file,
            integrity: candidate.integrity,
            externalImports,
            ...(candidate.sourceMap === undefined ? {} : { sourceMap: candidate.sourceMap }),
        });
    }
    return Object.freeze({
        format: FACET_BUNDLE_FORMAT,
        formatVersion: FACET_BUNDLE_FORMAT_VERSION,
        plugin: Object.freeze({
            id: value.plugin.id,
            ...(value.plugin.version === undefined ? {} : { version: value.plugin.version }),
        }),
        entries: Object.freeze(entries),
    });
}
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
//# sourceMappingURL=bundle-loader.js.map