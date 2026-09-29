import { uuidv7 } from "../../../../pi-ai/utils/uuid.js";
import { StorageBackedSession } from "../session.js";
import { parseJsonlSessionHeader } from "./codec.js";
import { runJsonlFork } from "./fork.js";
import { fileValue } from "./io.js";
import { LegacyV3Source, metadataFromLegacyV3Header } from "./legacy-v3.js";
import { JsonlStorage } from "./storage.js";
import { JSONL_FORMAT_VERSION, JSONL_STORAGE_VERSION, } from "./types.js";
function metadataFromHeader(header, path, modifiedAt) {
    return {
        id: header.id,
        createdAt: header.createdAt,
        storageVersion: header.storageVersion,
        cwd: header.cwd,
        path,
        modifiedAt,
        ...(header.parentSessionId === undefined ? {} : { parentSessionId: header.parentSessionId }),
        ...(header.legacyParentSessionPath === undefined
            ? {}
            : { legacyParentSessionPath: header.legacyParentSessionPath }),
    };
}
function sessionDirectoryName(cwd) {
    return `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
}
function sessionFileName(createdAt, id) {
    const timestamp = new Date(createdAt).toISOString().replace(/[:.]/g, "-");
    return `${timestamp}_${encodeURIComponent(id)}.jsonl`;
}
/** File-backed format-4 session repository lifecycle. */
export class JsonlSessionRepo {
    fileSystem;
    sessionsRootInput;
    now;
    openSessions = new Map();
    pendingCreates = new Set();
    closed = false;
    closePromise;
    constructor(options) {
        this.fileSystem = options.fileSystem;
        this.sessionsRootInput = options.sessionsRoot;
        this.now = options.now ?? Date.now;
    }
    async create(options, context) {
        this.assertOpen();
        const createdAt = this.now();
        const { cwd, id } = await this.resolveCreateDestination(options.cwd, options.id, createdAt, context);
        const key = this.sessionKey(cwd, id);
        if (this.openSessions.has(key) || this.pendingCreates.has(key))
            throw new Error(`Session already exists: ${id}`);
        this.pendingCreates.add(key);
        let path;
        let storage;
        try {
            path = await this.resolveNewSessionPath(cwd, createdAt, id, context);
            const header = {
                v: JSONL_FORMAT_VERSION,
                kind: "header",
                id,
                storageVersion: JSONL_STORAGE_VERSION,
                createdAt,
                cwd,
                ...(options.parentSessionId === undefined ? {} : { parentSessionId: options.parentSessionId }),
            };
            storage = await JsonlStorage.create({ fileSystem: this.fileSystem, path, now: this.now }, header, [], context);
            const info = fileValue(await this.fileSystem.fileInfo(path, context), `Failed to read session ${path}`);
            return this.publishOpenSession(metadataFromHeader(header, path, info.mtimeMs), storage, key);
        }
        catch (error) {
            await storage?.close(context).catch(() => undefined);
            if (path !== undefined)
                await this.fileSystem.remove(path, { force: true }, context);
            throw error;
        }
        finally {
            this.pendingCreates.delete(key);
        }
    }
    async open(metadata, context) {
        this.assertOpen();
        const key = this.sessionKey(metadata.cwd, metadata.id);
        if (this.openSessions.has(key))
            throw new Error(`Session is already open: ${metadata.id}`);
        let storage;
        try {
            storage = await this.loadStorage(metadata, context);
            return this.publishOpenSession(metadata, storage, key);
        }
        catch (error) {
            await storage?.close(context);
            throw error;
        }
    }
    async list(options, context) {
        options ??= {};
        this.assertOpen();
        const cwd = options.cwd === undefined
            ? undefined
            : fileValue(await this.fileSystem.absolutePath(options.cwd, context), `Failed to resolve session cwd ${options.cwd}`);
        const root = await this.root(context);
        if (!fileValue(await this.fileSystem.exists(root, context), `Failed to check sessions root ${root}`))
            return [];
        const directories = cwd === undefined ? await this.sessionDirectories(root, context) : [await this.sessionDirectory(cwd, context)];
        const metadata = [];
        for (const directory of directories)
            metadata.push(...(await this.listDirectory(directory, cwd, context)));
        return metadata.sort((left, right) => right.createdAt - left.createdAt || left.id.localeCompare(right.id) || left.cwd.localeCompare(right.cwd));
    }
    async delete(metadata, context) {
        this.assertOpen();
        const key = this.sessionKey(metadata.cwd, metadata.id);
        if (this.openSessions.has(key))
            throw new Error(`Session is open: ${metadata.id}`);
        if (!fileValue(await this.fileSystem.exists(metadata.path, context), `Failed to check session ${metadata.path}`)) {
            throw new Error(`Session file does not exist: ${metadata.path}`);
        }
        fileValue(await this.fileSystem.remove(metadata.path, undefined, context), `Failed to delete session ${metadata.path}`);
    }
    async fork(source, options, context) {
        this.assertOpen();
        const createdAt = this.now();
        const cwd = source.cwd;
        const id = options.id ?? uuidv7(createdAt);
        const destinationKey = this.sessionKey(cwd, id);
        if (this.openSessions.has(destinationKey) || this.pendingCreates.has(destinationKey)) {
            throw new Error(`Session already exists: ${id}`);
        }
        this.pendingCreates.add(destinationKey);
        const sourceStorage = this.openSessions.get(this.sessionKey(source.cwd, source.id));
        let path;
        let storage;
        try {
            const input = await this.resolveForkInput(source, sourceStorage, context);
            path = await this.resolveNewSessionPath(cwd, createdAt, id, context);
            const header = {
                v: JSONL_FORMAT_VERSION,
                kind: "header",
                id,
                storageVersion: JSONL_STORAGE_VERSION,
                createdAt,
                cwd,
                parentSessionId: source.id,
            };
            await runJsonlFork({
                input,
                fileSystem: this.fileSystem,
                destinationPath: path,
                destinationHeader: header,
                fork: options,
            }, context);
            storage = await JsonlStorage.open({ fileSystem: this.fileSystem, path, now: this.now }, context);
            const info = fileValue(await this.fileSystem.fileInfo(path, context), `Failed to read session ${path}`);
            return this.publishOpenSession(metadataFromHeader(header, path, info.mtimeMs), storage, destinationKey);
        }
        catch (error) {
            await storage?.close(context).catch(() => undefined);
            if (path !== undefined)
                await this.fileSystem.remove(path, { force: true }, context);
            throw error;
        }
        finally {
            this.pendingCreates.delete(destinationKey);
        }
    }
    close(_context) {
        if (this.closePromise !== undefined)
            return this.closePromise;
        this.closed = true;
        // TODO: Define ownership semantics before deciding whether repository close should close session handles.
        this.closePromise = Promise.resolve();
        return this.closePromise;
    }
    async resolveCreateDestination(cwdInput, id, createdAt, context) {
        const destinationId = id ?? uuidv7(createdAt);
        const cwd = fileValue(await this.fileSystem.absolutePath(cwdInput, context), `Failed to resolve session cwd ${cwdInput}`);
        return { cwd, id: destinationId };
    }
    async listDirectory(directory, cwd, context) {
        if (!fileValue(await this.fileSystem.exists(directory, context), `Failed to check sessions directory ${directory}`)) {
            return [];
        }
        const files = fileValue(await this.fileSystem.listDir(directory, context), `Failed to list sessions directory ${directory}`).filter((file) => file.kind !== "directory" && file.name.endsWith(".jsonl"));
        const metadata = [];
        for (const file of files) {
            const discovered = await this.readSessionMetadata(file, context);
            if (discovered === undefined)
                continue;
            // Directory encoding is lossy: /a/b and /a-b both map to --a-b--.
            if (cwd === undefined || discovered.cwd === cwd)
                metadata.push(discovered);
        }
        return metadata;
    }
    async readSessionMetadata(file, context) {
        const lines = fileValue(await this.fileSystem.readTextLines(file.path, { maxLines: 1 }, context), `Failed to read session header ${file.path}`);
        if (lines[0] === undefined)
            return undefined;
        const parsedHeader = parseJsonlSessionHeader(lines[0]);
        if (!parsedHeader.ok)
            return undefined;
        if (parsedHeader.value.format === "v3-legacy") {
            const metadata = await metadataFromLegacyV3Header(this.fileSystem, parsedHeader.value.header, context);
            return { ...metadata, path: file.path, modifiedAt: file.mtimeMs };
        }
        return metadataFromHeader(parsedHeader.value.header, file.path, file.mtimeMs);
    }
    async sessionDirectories(root, context) {
        return fileValue(await this.fileSystem.listDir(root, context), `Failed to list sessions root ${root}`)
            .filter((entry) => entry.kind === "directory")
            .map((entry) => entry.path);
    }
    async sessionDirectory(cwd, context) {
        return fileValue(await this.fileSystem.joinPath([await this.root(context), sessionDirectoryName(cwd)], context), `Failed to resolve sessions directory for ${cwd}`);
    }
    async resolveNewSessionPath(cwd, createdAt, id, context) {
        const directory = await this.sessionDirectory(cwd, context);
        await this.assertSessionIdAvailable(directory, id, context);
        fileValue(await this.fileSystem.createDir(directory, undefined, context), `Failed to create sessions directory ${directory}`);
        return fileValue(await this.fileSystem.joinPath([directory, sessionFileName(createdAt, id)], context), `Failed to resolve path for session ${id}`);
    }
    async assertSessionIdAvailable(directory, id, context) {
        if (!fileValue(await this.fileSystem.exists(directory, context), `Failed to check sessions directory ${directory}`))
            return;
        const suffix = `_${encodeURIComponent(id)}.jsonl`;
        const idExists = fileValue(await this.fileSystem.listDir(directory, context), `Failed to list sessions directory ${directory}`).some((entry) => entry.kind !== "directory" && entry.name.endsWith(suffix));
        if (idExists)
            throw new Error(`Session already exists: ${id}`);
    }
    async resolveForkInput(source, storage, context) {
        if (storage !== undefined) {
            if (storage.isLegacyV3()) {
                throw new Error("Cannot fork an open legacy v3 JSONL session; commit a non-empty transaction to upgrade it to format 4 first");
            }
            const nextSeq = await storage.captureForkNextSeq(context);
            return { kind: "open", metadata: source, nextSeq };
        }
        if (await this.isLegacyV3ForkSource(source, context)) {
            const normalized = await LegacyV3Source.read(this.fileSystem, source.path, context);
            if (normalized.header.id !== source.id || normalized.header.cwd !== source.cwd) {
                throw new Error(`Session identity does not match header: ${source.id}`);
            }
            return { kind: "legacy-v3", normalized };
        }
        return { kind: "closed", metadata: source };
    }
    async isLegacyV3ForkSource(source, context) {
        const lines = fileValue(await this.fileSystem.readTextLines(source.path, { maxLines: 1 }, context), `Failed to read session header ${source.path}`);
        if (lines[0] === undefined)
            return false;
        const parsed = parseJsonlSessionHeader(lines[0]);
        return parsed.ok && parsed.value.format === "v3-legacy";
    }
    publishOpenSession(metadata, storage, key) {
        if (this.openSessions.has(key))
            throw new Error(`Session is already open: ${metadata.id}`);
        const session = new StorageBackedSession(metadata, storage, {
            onClose: () => {
                if (this.openSessions.get(key) === storage)
                    this.openSessions.delete(key);
            },
        });
        this.openSessions.set(key, storage);
        return session;
    }
    sessionKey(cwd, id) {
        return `${cwd}\0${id}`;
    }
    async loadStorage(metadata, context) {
        if (!fileValue(await this.fileSystem.exists(metadata.path, context), `Failed to check session ${metadata.path}`)) {
            throw new Error(`Session file does not exist: ${metadata.path}`);
        }
        const storage = await JsonlStorage.open({
            fileSystem: this.fileSystem,
            path: metadata.path,
            now: this.now,
        }, context);
        try {
            if (storage.header.id !== metadata.id || storage.header.cwd !== metadata.cwd) {
                throw new Error(`Session identity does not match header: ${metadata.id}`);
            }
            if (storage.header.storageVersion !== JSONL_STORAGE_VERSION) {
                throw new Error(`Session ${metadata.id} uses unsupported storage version ${storage.header.storageVersion}`);
            }
            return storage;
        }
        catch (error) {
            await storage.close(context);
            throw error;
        }
    }
    root(context) {
        return this.fileSystem
            .absolutePath(this.sessionsRootInput, context)
            .then((result) => fileValue(result, `Failed to resolve sessions root ${this.sessionsRootInput}`));
    }
    assertOpen() {
        if (this.closed)
            throw new Error("JsonlSessionRepo is closed");
    }
}
//# sourceMappingURL=repo.js.map