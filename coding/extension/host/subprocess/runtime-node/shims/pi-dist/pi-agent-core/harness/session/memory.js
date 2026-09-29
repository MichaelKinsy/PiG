import { uuidv7 } from "../../../pi-ai/utils/uuid.js";
import { InMemoryStorageState } from "./in-memory-storage-state.js";
import { StorageBackedSession } from "./session.js";
export class MemoryStorage {
    now;
    storageState = new InMemoryStorageState();
    commitQueue = Promise.resolve();
    state = "open";
    closePromise;
    constructor(options = {}) {
        this.now = options.now ?? Date.now;
    }
    async commit(writes, _context) {
        if (this.state !== "open")
            throw new Error("MemoryStorage is closed");
        const result = this.commitQueue.then(() => {
            const prepared = this.storageState.prepareCommit(writes, this.now());
            const stats = this.storageState.applyValidated(prepared.writes);
            return { ...prepared.result, stats };
        });
        this.commitQueue = result.then(() => undefined, () => undefined);
        return result;
    }
    getEntries(ids, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        return Promise.resolve(this.storageState.getEntries(ids));
    }
    getValue(address, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        return Promise.resolve(this.storageState.getValue(address));
    }
    scanValues(prefix, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        return Promise.resolve(this.storageState.scanValues(prefix));
    }
    async readList(address, options, _context) {
        if (this.state !== "open")
            throw new Error("MemoryStorage is closed");
        return this.storageState.readList(address, options);
    }
    async scanBranch(query, _context) {
        if (this.state !== "open")
            throw new Error("MemoryStorage is closed");
        return this.storageState.scanBranch(query);
    }
    async scanBranchStructure(query, _context) {
        if (this.state !== "open")
            throw new Error("MemoryStorage is closed");
        return this.storageState.scanBranchStructure(query);
    }
    scanEntries(query, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        return Promise.resolve(this.storageState.scanEntries(query));
    }
    scanUsage(query, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        return Promise.resolve(this.storageState.scanUsage(query));
    }
    getStats(_context) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        return Promise.resolve(this.storageState.getStats());
    }
    /** Construct a destination storage at one serialized boundary between source commits. */
    fork(options) {
        if (this.state !== "open")
            return Promise.reject(new Error("MemoryStorage is closed"));
        const result = this.commitQueue.then(() => {
            const destination = new MemoryStorage({ now: this.now });
            destination.storageState = this.storageState.createFork(options);
            return destination;
        });
        this.commitQueue = result.then(() => undefined, () => undefined);
        return result;
    }
    close(_context) {
        if (this.closePromise !== undefined)
            return this.closePromise;
        this.state = "closing";
        this.closePromise = this.commitQueue.then(() => {
            this.state = "closed";
        });
        return this.closePromise;
    }
}
const MEMORY_STORAGE_VERSION = 1;
class MemorySessionFacade {
    metadata;
    idGenerator;
    session;
    onClose;
    admitted = new Set();
    closedError = new Error("Session is closed");
    state = "open";
    closePromise;
    constructor(session, onClose) {
        this.session = session;
        this.metadata = session.metadata;
        this.idGenerator = session.idGenerator;
        this.onClose = onClose;
    }
    async beginMutation(context) {
        let resolveFinished;
        const finished = new Promise((resolve) => {
            resolveFinished = resolve;
        });
        this.admitted.add(finished);
        let source;
        try {
            source = await this.admit(() => this.session.beginMutation(context));
        }
        catch (error) {
            this.admitted.delete(finished);
            resolveFinished();
            throw error;
        }
        if (this.state !== "open") {
            await source.end(context);
            this.admitted.delete(finished);
            resolveFinished();
            throw this.closedError;
        }
        let ended = false;
        return {
            commit: (writes, commitContext) => source.commit(writes, commitContext),
            end: async (endContext) => {
                try {
                    await source.end(endContext);
                }
                finally {
                    if (!ended) {
                        ended = true;
                        this.admitted.delete(finished);
                        resolveFinished();
                    }
                }
            },
            getEntries: (ids, readContext) => source.getEntries(ids, readContext),
            getStats: (readContext) => source.getStats(readContext),
            getValue: (address, readContext) => source.getValue(address, readContext),
            scanValues: (prefix, readContext) => source.scanValues(prefix, readContext),
            readList: (address, options, readContext) => source.readList(address, options, readContext),
            scanBranch: (query, readContext) => source.scanBranch(query, readContext),
        };
    }
    mutate(mutation, context) {
        return this.admit(() => this.session.mutate((mutator, mutationContext) => {
            if (this.state !== "open")
                throw this.closedError;
            return mutation(mutator, mutationContext);
        }, context));
    }
    getEntries(ids, context) {
        return this.admit(() => this.session.getEntries(ids, context));
    }
    getEntry(id, context) {
        return this.admit(() => this.session.getEntry(id, context));
    }
    getValue(address, context) {
        return this.admit(() => this.session.getValue(address, context));
    }
    scanValues(prefix, context) {
        return this.admit(() => this.session.scanValues(prefix, context));
    }
    readList(address, options, context) {
        return this.admit(() => this.session.readList(address, options, context));
    }
    scanBranch(query, context) {
        return this.admit(() => this.session.scanBranch(query, context));
    }
    getStats(context) {
        return this.admit(() => this.session.getStats(context));
    }
    getName(context) {
        return this.admit(() => this.session.getName(context));
    }
    getLabel(targetId, context) {
        return this.admit(() => this.session.getLabel(targetId, context));
    }
    findEntries(query, context) {
        return this.admit(() => this.session.findEntries(query, context));
    }
    findEntry(query, context) {
        return this.admit(() => this.session.findEntry(query, context));
    }
    async branch(name, context) {
        const branch = await this.admit(() => this.session.branch(name, context));
        return branch === undefined ? undefined : this.wrapBranch(branch);
    }
    async createBranch(name, at, context) {
        return this.wrapBranch(await this.admit(() => this.session.createBranch(name, at, context)));
    }
    setValue(address, next, context) {
        return this.admit(() => this.session.setValue(address, next, context));
    }
    deleteValue(address, context) {
        return this.admit(() => this.session.deleteValue(address, context));
    }
    appendList(address, element, context) {
        return this.admit(() => this.session.appendList(address, element, context));
    }
    deleteList(address, context) {
        return this.admit(() => this.session.deleteList(address, context));
    }
    setName(name, context) {
        return this.admit(() => this.session.setName(name, context));
    }
    setLabel(targetId, label, context) {
        return this.admit(() => this.session.setLabel(targetId, label, context));
    }
    close(_context) {
        if (this.closePromise !== undefined)
            return this.closePromise;
        this.state = "closing";
        this.closePromise = Promise.allSettled([...this.admitted]).then(() => {
            this.state = "closed";
            this.onClose();
        });
        return this.closePromise;
    }
    wrapBranch(branch) {
        return {
            name: branch.name,
            getTipId: (context) => this.admit(() => branch.getTipId(context)),
            findEntries: (query, context) => this.admit(() => branch.findEntries(query, context)),
            findEntry: (query, context) => this.admit(() => branch.findEntry(query, context)),
            appendMessage: (message, context) => this.admit(() => branch.appendMessage(message, context)),
            appendCustomEntry: (customType, data, context) => this.admit(() => branch.appendCustomEntry(customType, data, context)),
        };
    }
    admit(operation) {
        if (this.state !== "open")
            return Promise.reject(this.closedError);
        let result;
        try {
            result = operation();
        }
        catch (error) {
            result = Promise.reject(error);
        }
        this.admitted.add(result);
        void result.then(() => this.admitted.delete(result), () => this.admitted.delete(result));
        return result;
    }
}
export class MemorySessionRepo {
    now;
    sessions = new Map();
    pendingIds = new Set();
    closed = false;
    closePromise;
    constructor(options = {}) {
        this.now = options.now ?? Date.now;
    }
    async create(options, context) {
        this.assertOpen();
        const createdAt = this.now();
        const id = options.id ?? uuidv7(createdAt);
        this.reserveId(id);
        const metadata = {
            id,
            createdAt,
            storageVersion: MEMORY_STORAGE_VERSION,
            ...(options.parentSessionId === undefined ? {} : { parentSessionId: options.parentSessionId }),
        };
        const storage = new MemoryStorage({ now: this.now });
        const session = new StorageBackedSession(metadata, storage);
        try {
            const record = {
                metadata,
                storage,
                session,
                open: true,
            };
            this.sessions.set(id, record);
            return this.openRecord(record);
        }
        catch (error) {
            await session.close(context);
            throw error;
        }
        finally {
            this.pendingIds.delete(id);
        }
    }
    open(metadata, _context) {
        // Memory sessions are always created at the current storage version, so
        // persistent-backend version gating does not apply here.
        this.assertOpen();
        const record = this.sessions.get(metadata.id);
        if (record === undefined)
            return Promise.reject(new Error(`Unknown session: ${metadata.id}`));
        if (record.open)
            return Promise.reject(new Error(`Session is already open: ${metadata.id}`));
        record.open = true;
        return Promise.resolve(this.openRecord(record));
    }
    list(_options, _context) {
        this.assertOpen();
        return Promise.resolve([...this.sessions.values()].map(({ metadata }) => metadata));
    }
    async delete(metadata, context) {
        this.assertOpen();
        const record = this.sessions.get(metadata.id);
        if (record === undefined)
            throw new Error(`Unknown session: ${metadata.id}`);
        if (record.open)
            throw new Error(`Session is open: ${metadata.id}`);
        await record.session.close(context);
        this.sessions.delete(metadata.id);
    }
    async fork(source, options, _context) {
        this.assertOpen();
        const sourceRecord = this.sessions.get(source.id);
        if (sourceRecord === undefined)
            throw new Error(`Unknown session: ${source.id}`);
        const createdAt = this.now();
        const id = options.id ?? uuidv7(createdAt);
        this.reserveId(id);
        try {
            const storage = await sourceRecord.storage.fork(options);
            const metadata = {
                id,
                createdAt,
                storageVersion: MEMORY_STORAGE_VERSION,
                parentSessionId: sourceRecord.metadata.id,
            };
            const session = new StorageBackedSession(metadata, storage);
            const record = {
                metadata,
                storage,
                session,
                open: true,
            };
            this.sessions.set(id, record);
            return this.openRecord(record);
        }
        finally {
            this.pendingIds.delete(id);
        }
    }
    close(context) {
        if (this.closePromise !== undefined)
            return this.closePromise;
        this.closed = true;
        this.closePromise = Promise.all([...this.sessions.values()].map(({ session }) => session.close(context))).then(() => undefined);
        return this.closePromise;
    }
    openRecord(record) {
        return new MemorySessionFacade(record.session, () => {
            record.open = false;
        });
    }
    reserveId(id) {
        if (this.sessions.has(id) || this.pendingIds.has(id))
            throw new Error(`Session already exists: ${id}`);
        this.pendingIds.add(id);
    }
    assertOpen() {
        if (this.closed)
            throw new Error("MemorySessionRepo is closed");
    }
}
//# sourceMappingURL=memory.js.map