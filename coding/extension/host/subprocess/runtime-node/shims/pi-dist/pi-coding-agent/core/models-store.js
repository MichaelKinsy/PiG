import { join } from "node:path";
import { getAgentDir } from "../config.js";
import { raceWithAbortSignal } from "../utils/abort.js";
import { getFileRevision, normalizePath } from "../utils/paths.js";
import { stripBom } from "../utils/text.js";
import { FileAuthStorageBackend } from "./auth-storage.js";
// Optimize the common path without retaining an unbounded set of custom paths.
let sharedModelsFileReadState;
export class InMemoryCodingAgentModelsStore {
    entries = new Map();
    async read(providerId, options) {
        options?.signal?.throwIfAborted();
        const entry = this.entries.get(providerId);
        return entry ? structuredClone(entry) : undefined;
    }
    async write(providerId, entry, options) {
        options?.signal?.throwIfAborted();
        this.entries.set(providerId, structuredClone(entry));
    }
    async delete(providerId, options) {
        options?.signal?.throwIfAborted();
        this.entries.delete(providerId);
    }
}
/** Locked JSON-backed storage for dynamically refreshed provider catalogs. */
export class FileModelsStore {
    storage;
    path;
    readState;
    constructor(path = join(getAgentDir(), "models-store.json")) {
        this.path = normalizePath(path);
        this.storage = new FileAuthStorageBackend(this.path);
        this.readState =
            sharedModelsFileReadState?.path === this.path ? sharedModelsFileReadState.readState : { data: {} };
        if (!sharedModelsFileReadState) {
            sharedModelsFileReadState = { path: this.path, readState: this.readState };
        }
    }
    parse(content) {
        return content ? JSON.parse(stripBom(content)) : {};
    }
    updateReadState(readState, data, revision) {
        readState.data = data;
        readState.revision = revision;
    }
    reloadFromStorage(readState, options) {
        return this.storage.withLockAsync(async (content) => {
            const data = this.parse(content);
            this.updateReadState(readState, data, getFileRevision(this.path));
            return { result: data };
        }, options);
    }
    async readLatest(readState, options) {
        options?.signal?.throwIfAborted();
        const revision = getFileRevision(this.path);
        if (revision !== undefined && revision === readState.revision)
            return readState.data;
        if (!readState.reload) {
            const controller = new AbortController();
            const reload = {
                controller,
                promise: this.reloadFromStorage(readState, { signal: controller.signal }),
                readers: 0,
            };
            readState.reload = reload;
            void reload.promise.then(() => {
                if (readState.reload === reload)
                    readState.reload = undefined;
            }, () => {
                if (readState.reload === reload)
                    readState.reload = undefined;
            });
        }
        const reload = readState.reload;
        reload.readers++;
        try {
            return await raceWithAbortSignal(reload.promise, options?.signal);
        }
        finally {
            reload.readers--;
            if (reload.readers === 0 && readState.reload === reload) {
                readState.reload = undefined;
                reload.controller.abort();
            }
        }
    }
    async read(providerId, options) {
        const entry = (await this.readLatest(this.readState, options))[providerId];
        options?.signal?.throwIfAborted();
        return entry ? structuredClone(entry) : undefined;
    }
    async write(providerId, entry, options) {
        let latest;
        await this.storage.withLockAsync(async (content) => {
            const current = this.parse(content);
            current[providerId] = structuredClone(entry);
            latest = current;
            return { result: undefined, next: JSON.stringify(current, null, 2) };
        }, options);
        if (latest)
            this.updateReadState(this.readState, latest);
    }
    async delete(providerId, options) {
        let latest;
        await this.storage.withLockAsync(async (content) => {
            const current = this.parse(content);
            delete current[providerId];
            latest = current;
            return { result: undefined, next: JSON.stringify(current, null, 2) };
        }, options);
        if (latest)
            this.updateReadState(this.readState, latest);
    }
}
//# sourceMappingURL=models-store.js.map