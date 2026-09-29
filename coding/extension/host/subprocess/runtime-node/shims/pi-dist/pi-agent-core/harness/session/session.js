import { uuidv7 } from "../../../pi-ai/utils/uuid.js";
import { insertEntry } from "./commit.js";
import { MutationLine } from "./mutation-line.js";
import { appendList as appendListWrite, branchTip, deleteList as deleteListWrite, deleteValue as deleteValueWrite, entryLabel, sessionName, setValue as setValueWrite, } from "./values.js";
/** Durable session state is internally inconsistent and cannot be safely advanced. */
export class SessionInvariantError extends Error {
    constructor(message) {
        super(message);
        this.name = "SessionInvariantError";
    }
}
/** A requested Branch name is invalid. */
export class SessionInvalidBranchError extends Error {
    branch;
    reason;
    constructor(branch, reason) {
        super(`Invalid branch ${JSON.stringify(branch)}: ${reason}`);
        this.name = "SessionInvalidBranchError";
        this.branch = branch;
        this.reason = reason;
    }
}
/** A requested branch already exists. */
export class SessionBranchExistsError extends Error {
    branch;
    constructor(branch) {
        super(`Branch already exists: ${branch}`);
        this.name = "SessionBranchExistsError";
        this.branch = branch;
    }
}
/** A pending assistant message cannot be persisted as a session entry. */
export class SessionPendingAssistantMessageError extends Error {
    constructor() {
        super("Cannot persist a pending assistant message");
        this.name = "SessionPendingAssistantMessageError";
    }
}
/** A requested session entry target does not exist. */
export class SessionUnknownTargetError extends Error {
    targetId;
    constructor(targetId) {
        super(`Unknown target: ${targetId}`);
        this.name = "SessionUnknownTargetError";
        this.targetId = targetId;
    }
}
class StorageBackedSessionMutation {
    storage;
    release;
    active = true;
    commitResult;
    endPromise;
    constructor(storage, release) {
        this.storage = storage;
        this.release = release;
    }
    commit(writes, context) {
        this.assertActive();
        if (this.commitResult !== undefined)
            return Promise.reject(new Error("SessionMutator commit already attempted"));
        try {
            for (const write of writes) {
                if (write.kind === "entry" &&
                    write.entry.type === "message" &&
                    write.entry.message.role === "assistant" &&
                    write.entry.message.stopReason === "pending") {
                    throw new SessionPendingAssistantMessageError();
                }
            }
            this.commitResult = this.storage.commit(writes, context);
        }
        catch (error) {
            this.commitResult = Promise.reject(error);
        }
        return this.commitResult;
    }
    end(_context) {
        if (this.endPromise !== undefined)
            return this.endPromise;
        this.active = false;
        this.endPromise = this.settle().finally(this.release);
        return this.endPromise;
    }
    getEntries(ids, context) {
        this.assertActive();
        return this.storage.getEntries(ids, context);
    }
    getStats(context) {
        this.assertActive();
        return this.storage.getStats(context);
    }
    getValue(address, context) {
        this.assertActive();
        return this.storage.getValue(address, context);
    }
    scanValues(prefix, context) {
        this.assertActive();
        return this.storage.scanValues(prefix, context);
    }
    readList(address, options, context) {
        this.assertActive();
        return this.storage.readList(address, options, context);
    }
    scanBranch(query, context) {
        this.assertActive();
        return this.storage.scanBranch(query, context);
    }
    settle() {
        return (this.commitResult?.then(() => undefined, () => undefined) ?? Promise.resolve());
    }
    assertActive() {
        if (!this.active)
            throw new Error("SessionMutator cannot be used outside its mutation callback");
    }
}
class StorageBackedBranch {
    name;
    session;
    constructor(name, session) {
        this.name = name;
        this.session = session;
    }
    getTipId(context) {
        return this.session.getBranchTip(this.name, context);
    }
    async findEntries(query, context) {
        query ??= {};
        const start = query.start ?? (await this.getTipId(context));
        if (start === null)
            return [];
        return this.session.scanBranch({ ...query, start, order: query.order ?? "newestFirst" }, context);
    }
    async findEntry(query, context) {
        query ??= {};
        return (await this.findEntries({ ...query, limit: query.limit === undefined ? 1 : Math.min(query.limit, 1) }, context))[0];
    }
    appendMessage(message, context) {
        return this.session.appendToBranch(this.name, { type: "message", message }, context);
    }
    appendCustomEntry(customType, data, context) {
        return this.session.appendToBranch(this.name, { type: "custom", customType, ...(data === undefined ? {} : { data }) }, context);
    }
}
/** Package-internal typed boundary shared by concrete session repositories. */
export class StorageBackedSession {
    metadata;
    idGenerator;
    storage;
    mutationLine;
    onClose;
    branches = new Map();
    closedError = new Error("Session is closed");
    state = "open";
    closePromise;
    constructor(metadata, storage, options = {}) {
        this.metadata = metadata;
        this.idGenerator = options.idGenerator ?? { next: uuidv7 };
        this.storage = storage;
        this.mutationLine = options.mutationLine ?? new MutationLine();
        this.onClose = options.onClose;
    }
    async beginMutation(_context) {
        this.assertOpen();
        let grant;
        let rejectGrant;
        const granted = new Promise((resolve, reject) => {
            grant = resolve;
            rejectGrant = reject;
        });
        let release;
        const finished = new Promise((resolve) => {
            release = resolve;
        });
        const line = this.mutationLine.run(async () => {
            grant(new StorageBackedSessionMutation(this.storage, release));
            await finished;
        });
        void line.catch(rejectGrant);
        return granted;
    }
    async mutate(mutation, context) {
        const mutator = await this.beginMutation(context);
        try {
            return await mutation(mutator, context);
        }
        finally {
            await mutator.end(context);
        }
    }
    async getEntries(ids, context) {
        this.assertOpen();
        return this.storage.getEntries(ids, context);
    }
    async getEntry(id, context) {
        return (await this.getEntries([id], context)).get(id);
    }
    async getValue(address, context) {
        this.assertOpen();
        return this.storage.getValue(address, context);
    }
    async scanValues(prefix, context) {
        this.assertOpen();
        return this.storage.scanValues(prefix, context);
    }
    async readList(address, options, context) {
        this.assertOpen();
        return this.storage.readList(address, options, context);
    }
    async scanBranch(query, context) {
        this.assertOpen();
        return this.storage.scanBranch(query, context);
    }
    async getStats(context) {
        this.assertOpen();
        return this.storage.getStats(context);
    }
    async getName(context) {
        return (await this.getValue(sessionName, context))?.value;
    }
    async getLabel(targetId, context) {
        return (await this.getValue(entryLabel(targetId), context))?.value;
    }
    async findEntries(query, context) {
        query ??= {};
        this.assertOpen();
        const order = query.order ?? "desc";
        if (query.cursor !== undefined) {
            if (order === "asc" && query.cursor.seq === Number.MAX_SAFE_INTEGER)
                return [];
            if (order === "desc" && query.cursor.seq <= 1)
                return [];
        }
        return this.storage.scanEntries({
            type: query.type,
            customType: query.customType,
            order,
            limit: query.limit,
            ...(query.cursor === undefined
                ? {}
                : order === "asc"
                    ? { fromSeq: query.cursor.seq + 1 }
                    : { toSeq: query.cursor.seq - 1 }),
        }, context);
    }
    async findEntry(query, context) {
        query ??= {};
        return (await this.findEntries({ ...query, limit: query.limit === undefined ? 1 : Math.min(query.limit, 1) }, context))[0];
    }
    async branch(name, context) {
        this.assertValidBranchName(name);
        if ((await this.getValue(branchTip(name), context)) === undefined)
            return undefined;
        return this.getOrCreateBranchObject(name);
    }
    async createBranch(name, at, context) {
        this.assertOpen();
        this.assertValidBranchName(name);
        await this.mutate(async (mutator) => {
            if ((await mutator.getValue(branchTip(name), context)) !== undefined) {
                throw new SessionBranchExistsError(name);
            }
            if (at !== null && !(await mutator.getEntries([at], context)).has(at)) {
                throw new SessionUnknownTargetError(at);
            }
            await mutator.commit([setValueWrite(branchTip(name), at)], context);
        }, context);
        return this.getOrCreateBranchObject(name);
    }
    setValue(address, next, context) {
        return this.mutate(async (mutator) => {
            await mutator.commit([setValueWrite(address, next)], context);
        }, context);
    }
    deleteValue(address, context) {
        return this.mutate(async (mutator) => {
            await mutator.commit([deleteValueWrite(address)], context);
        }, context);
    }
    appendList(address, element, context) {
        return this.mutate(async (mutator) => {
            await mutator.commit([appendListWrite(address, element)], context);
        }, context);
    }
    deleteList(address, context) {
        return this.mutate(async (mutator) => {
            await mutator.commit([deleteListWrite(address)], context);
        }, context);
    }
    setName(name, context) {
        return name === undefined ? this.deleteValue(sessionName, context) : this.setValue(sessionName, name, context);
    }
    setLabel(targetId, label, context) {
        const address = entryLabel(targetId);
        return label === undefined ? this.deleteValue(address, context) : this.setValue(address, label, context);
    }
    close(context) {
        if (this.closePromise !== undefined)
            return this.closePromise;
        this.state = "closing";
        this.closePromise = this.mutationLine
            .seal(this.closedError)
            .then(() => this.storage.close(context))
            .finally(() => {
            this.state = "closed";
            this.onClose?.();
        });
        return this.closePromise;
    }
    async getBranchTip(name, context) {
        const stored = await this.getValue(branchTip(name), context);
        if (stored === undefined)
            throw new SessionInvariantError(`Unknown branch: ${name}`);
        return stored.value;
    }
    async appendToBranch(name, entry, context) {
        this.assertOpen();
        if (entry.type === "message" && entry.message.role === "assistant" && entry.message.stopReason === "pending") {
            throw new SessionPendingAssistantMessageError();
        }
        const id = this.idGenerator.next();
        await this.mutate(async (mutator) => {
            const tip = await mutator.getValue(branchTip(name), context);
            if (tip === undefined)
                throw new SessionInvariantError(`Unknown branch: ${name}`);
            await mutator.commit([
                insertEntry(entry.type === "message"
                    ? { id, parentId: tip.value, type: "message", message: entry.message }
                    : {
                        id,
                        parentId: tip.value,
                        type: "custom",
                        customType: entry.customType,
                        ...(entry.data === undefined ? {} : { data: entry.data }),
                    }),
                setValueWrite(branchTip(name), id),
            ], context);
        }, context);
        return id;
    }
    getOrCreateBranchObject(name) {
        let branch = this.branches.get(name);
        if (branch === undefined) {
            branch = new StorageBackedBranch(name, this);
            this.branches.set(name, branch);
        }
        return branch;
    }
    assertValidBranchName(name) {
        if (name.length === 0)
            throw new SessionInvalidBranchError(name, "branch name must not be empty");
        if (name.includes("\u0000")) {
            throw new SessionInvalidBranchError(name, "branch name must not contain \\u0000");
        }
    }
    assertOpen() {
        if (this.state !== "open")
            throw this.closedError;
    }
}
//# sourceMappingURL=session.js.map