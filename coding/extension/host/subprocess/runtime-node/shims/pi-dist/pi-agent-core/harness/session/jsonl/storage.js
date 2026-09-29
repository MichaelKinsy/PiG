import { uuidv7 } from "../../../../pi-ai/utils/uuid.js";
import { insertUsage } from "../commit.js";
import { InMemoryStorageState } from "../in-memory-storage-state.js";
import { fileValue, parseJsonlTransaction, publishFileAtomically, publishJsonl, readJsonlHeader, serializeJsonlTransaction, } from "./io.js";
import { LegacyV3Source } from "./legacy-v3.js";
import { JSONL_STORAGE_VERSION } from "./types.js";
function splitCompleteLines(content) {
    if (content.endsWith("\n"))
        return { lines: content.slice(0, -1).split("\n"), torn: false };
    const lastNewline = content.lastIndexOf("\n");
    if (lastNewline === -1)
        return { lines: [], torn: true };
    return { lines: content.slice(0, lastNewline).split("\n"), torn: true };
}
/** JSONL storage backed by an injected filesystem capability. */
export class JsonlStorage {
    fileSystem;
    path;
    now;
    header;
    backing;
    storageState = new InMemoryStorageState();
    commitQueue = Promise.resolve();
    state = "open";
    closePromise;
    constructor(options, header, backing) {
        this.fileSystem = options.fileSystem;
        this.path = options.path;
        this.now = options.now ?? Date.now;
        this.header = header;
        this.backing = backing;
    }
    static async create(options, header, initialWrites, context) {
        const storage = new JsonlStorage(options, header, { kind: "v4" });
        const prepared = storage.storageState.prepareCommit(initialWrites, storage.now());
        await publishJsonl(options.fileSystem, options.path, header, context, async (append) => {
            if (prepared.writes.length !== 0)
                await append(prepared.writes);
        });
        storage.storageState.applyValidated(prepared.writes);
        return storage;
    }
    static async open(options, context) {
        const reader = fileValue(await options.fileSystem.openTextLineReader(options.path, context), `Failed to read JSONL storage ${options.path}`);
        const parsed = await readJsonlHeader(reader, options.path, context).finally(() => reader.close(context));
        return parsed.format === "v3-legacy"
            ? JsonlStorage.openLegacyV3(options, context)
            : JsonlStorage.openV4(options, parsed.header, context);
    }
    static async openV4(options, header, context) {
        const content = fileValue(await options.fileSystem.readTextFile(options.path, context), `Failed to read JSONL storage ${options.path}`);
        const { lines, torn } = splitCompleteLines(content);
        if (header.storageVersion !== JSONL_STORAGE_VERSION) {
            throw new Error(`Session ${header.id} uses unsupported storage version ${header.storageVersion}`);
        }
        const storage = new JsonlStorage(options, header, { kind: "v4" });
        for (let index = 1; index < lines.length; index++) {
            const line = lines[index];
            try {
                storage.replayCommitted(parseJsonlTransaction(line));
            }
            catch (error) {
                throw new Error(`Invalid JSONL storage ${options.path}: line ${index + 1}`, { cause: error });
            }
        }
        if (header.nextSeq !== undefined)
            storage.storageState.advanceNextSeq(header.nextSeq);
        if (torn) {
            await publishFileAtomically(options.fileSystem, options.path, context, (append) => append(`${lines.join("\n")}\n`));
        }
        return storage;
    }
    static async openLegacyV3(options, context) {
        const source = await LegacyV3Source.read(options.fileSystem, options.path, context);
        const storage = new JsonlStorage(options, { ...source.header, nextSeq: source.nextSeq }, { kind: "v3", source });
        for await (const write of source.writes(context))
            storage.replayCommitted([write]);
        return storage;
    }
    replayCommitted(writes) {
        this.storageState.validateCommitted(writes);
        this.storageState.applyValidated(writes);
    }
    async commit(writes, context) {
        if (this.state !== "open")
            throw new Error("JsonlStorage is closed");
        const result = this.commitQueue.then(() => this.applyCommit(writes, context));
        this.commitQueue = result.then(() => undefined, () => undefined);
        return result;
    }
    async applyCommit(writes, context) {
        if (this.backing.kind === "v3" && writes.length !== 0) {
            return this.upgradeLegacyV3ToV4(this.backing.source, writes, context);
        }
        const prepared = this.storageState.prepareCommit(writes, this.now());
        if (prepared.writes.length !== 0) {
            fileValue(await this.fileSystem.appendFile(this.path, `${serializeJsonlTransaction(prepared.writes)}\n`, context), `Failed to append JSONL storage ${this.path}`);
        }
        const stats = this.storageState.applyValidated(prepared.writes);
        return { ...prepared.result, stats: this.withImportedUsage(stats) };
    }
    /** Atomically upgrade legacy v3 backing and preserve the first caller write as a v4 transaction. */
    async upgradeLegacyV3ToV4(source, callerWrites, context) {
        const timestamp = this.now();
        const prepared = this.storageState.prepareCommit([
            insertUsage({
                id: uuidv7(timestamp),
                usage: source.importedUsage,
                adjustment: true,
                details: { source: "v3-import" },
            }),
            ...callerWrites,
        ], timestamp);
        const nextSeq = prepared.result.firstSeq + prepared.writes.length;
        const upgradedHeader = { ...this.header, nextSeq };
        await publishJsonl(this.fileSystem, this.path, upgradedHeader, context, async (append) => {
            for await (const write of source.writes(context))
                await append([write]);
            await append(prepared.writes);
        });
        const stats = this.storageState.applyValidated(prepared.writes);
        this.backing = { kind: "v4" };
        // The first sequence belongs to the internal usage adjustment; return only caller-write sequences.
        return {
            ...prepared.result,
            firstSeq: prepared.result.firstSeq + 1,
            seqs: prepared.result.seqs.slice(1),
            stats,
        };
    }
    getEntries(ids, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        return Promise.resolve(this.storageState.getEntries(ids));
    }
    getValue(address, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        return Promise.resolve(this.storageState.getValue(address));
    }
    scanValues(prefix, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        return Promise.resolve(this.storageState.scanValues(prefix));
    }
    async readList(address, options, _context) {
        if (this.state !== "open")
            throw new Error("JsonlStorage is closed");
        return this.storageState.readList(address, options);
    }
    async scanBranch(query, _context) {
        if (this.state !== "open")
            throw new Error("JsonlStorage is closed");
        return this.storageState.scanBranch(query);
    }
    async scanBranchStructure(query, _context) {
        if (this.state !== "open")
            throw new Error("JsonlStorage is closed");
        return this.storageState.scanBranchStructure(query);
    }
    scanEntries(query, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        return Promise.resolve(this.storageState.scanEntries(query));
    }
    scanUsage(query, _context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        return Promise.resolve(this.storageState.scanUsage(query));
    }
    getStats(_context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        return Promise.resolve(this.withImportedUsage(this.storageState.getStats()));
    }
    withImportedUsage(stats) {
        return this.backing.kind === "v4" ? stats : { ...stats, usage: this.backing.source.importedUsage };
    }
    isLegacyV3() {
        return this.backing.kind === "v3";
    }
    /** Capture the first sequence a later source commit would use. */
    captureForkNextSeq(_context) {
        if (this.state !== "open")
            return Promise.reject(new Error("JsonlStorage is closed"));
        const result = this.commitQueue.then(() => this.storageState.getNextSeq());
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
//# sourceMappingURL=storage.js.map