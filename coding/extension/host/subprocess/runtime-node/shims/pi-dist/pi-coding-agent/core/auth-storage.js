/**
 * CredentialStore implementation backed by auth.json.
 * Provider auth orchestration belongs to ModelRuntime and pi-ai Models.
 */
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "fs";
import { dirname, join } from "path";
import lockfile from "../../../proper-lockfile.mjs";
import { setTimeout as sleep } from "timers/promises";
import { getAgentDir } from "../config.js";
import { raceWithAbortSignal } from "../utils/abort.js";
import { getFileRevision, normalizePath } from "../utils/paths.js";
import { stripBom } from "../utils/text.js";
import { isCommandConfigValue, resolveConfigValue } from "./resolve-config-value.js";
// The mode applies only on creation so administrator-managed modes and ACLs remain intact.
const AUTH_FILE_WRITE_OPTIONS = { encoding: "utf-8", mode: 0o600 };
let sharedAuthFileReadState;
export class FileAuthStorageBackend {
    authPath;
    constructor(authPath = join(getAgentDir(), "auth.json")) {
        this.authPath = normalizePath(authPath);
    }
    ensureParentDir() {
        const dir = dirname(this.authPath);
        if (!existsSync(dir)) {
            mkdirSync(dir, { recursive: true, mode: 0o700 });
        }
    }
    ensureFileExists() {
        if (!existsSync(this.authPath)) {
            writeFileSync(this.authPath, "{}", AUTH_FILE_WRITE_OPTIONS);
        }
    }
    acquireLockSyncWithRetry(path) {
        const maxAttempts = 10;
        const delayMs = 20;
        let lastError;
        for (let attempt = 1; attempt <= maxAttempts; attempt++) {
            try {
                return lockfile.lockSync(path, { realpath: false });
            }
            catch (error) {
                const code = typeof error === "object" && error !== null && "code" in error
                    ? String(error.code)
                    : undefined;
                if (code !== "ELOCKED" || attempt === maxAttempts) {
                    throw error;
                }
                lastError = error;
                const start = Date.now();
                while (Date.now() - start < delayMs) {
                    // Sleep synchronously to avoid changing callers to async.
                }
            }
        }
        throw lastError ?? new Error("Failed to acquire auth storage lock");
    }
    withLock(fn) {
        this.ensureParentDir();
        this.ensureFileExists();
        let release;
        try {
            release = this.acquireLockSyncWithRetry(this.authPath);
            const current = existsSync(this.authPath) ? readFileSync(this.authPath, "utf-8") : undefined;
            const { result, next } = fn(current);
            if (next !== undefined) {
                writeFileSync(this.authPath, next, AUTH_FILE_WRITE_OPTIONS);
            }
            return result;
        }
        finally {
            if (release) {
                release();
            }
        }
    }
    async acquireLockAsync(signal, onCompromised) {
        const staleMs = 30_000;
        const maxDelayMs = 2_000;
        const deadline = Date.now() + staleMs;
        let retry = 0;
        while (true) {
            signal?.throwIfAborted();
            let release;
            try {
                release = await lockfile.lock(this.authPath, {
                    realpath: false,
                    retries: 0,
                    stale: staleMs,
                    onCompromised,
                });
            }
            catch (error) {
                signal?.throwIfAborted();
                const code = typeof error === "object" && error !== null && "code" in error
                    ? String(error.code)
                    : undefined;
                const remainingMs = deadline - Date.now();
                if (code !== "ELOCKED" || remainingMs <= 0)
                    throw error;
                const baseDelayMs = Math.min(10 * 2 ** retry, maxDelayMs / 2);
                retry++;
                const delayMs = Math.min(Math.round(baseDelayMs * (1 + Math.random())), remainingMs);
                if (signal)
                    await sleep(delayMs, undefined, { signal });
                else
                    await sleep(delayMs);
                continue;
            }
            if (signal?.aborted) {
                await release();
                signal.throwIfAborted();
            }
            return release;
        }
    }
    async withLockAsync(fn, options) {
        options?.signal?.throwIfAborted();
        this.ensureParentDir();
        this.ensureFileExists();
        let release;
        let lockCompromised = false;
        let lockCompromisedError;
        const throwIfCompromised = () => {
            if (lockCompromised) {
                throw lockCompromisedError ?? new Error("Auth storage lock was compromised");
            }
        };
        try {
            release = await this.acquireLockAsync(options?.signal, (error) => {
                lockCompromised = true;
                lockCompromisedError = error;
            });
            throwIfCompromised();
            options?.signal?.throwIfAborted();
            const current = existsSync(this.authPath) ? readFileSync(this.authPath, "utf-8") : undefined;
            const { result, next } = await fn(current);
            throwIfCompromised();
            options?.signal?.throwIfAborted();
            if (next !== undefined) {
                writeFileSync(this.authPath, next, AUTH_FILE_WRITE_OPTIONS);
            }
            throwIfCompromised();
            return result;
        }
        finally {
            if (release) {
                try {
                    await release();
                }
                catch {
                    // Ignore unlock errors when lock is compromised.
                }
            }
        }
    }
}
export class ReadOnlyAuthStorage {
    authPath;
    data;
    constructor(authPath = join(getAgentDir(), "auth.json")) {
        this.authPath = normalizePath(authPath);
    }
    load() {
        if (this.data)
            return this.data;
        let parsed;
        try {
            parsed = JSON.parse(stripBom(readFileSync(this.authPath, "utf-8")));
        }
        catch (error) {
            if (error.code === "ENOENT") {
                this.data = {};
                return this.data;
            }
            throw new Error(`Failed to read auth.json: ${error instanceof Error ? error.message : String(error)}`);
        }
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
            throw new Error("Invalid auth.json: expected an object");
        }
        for (const [providerId, credential] of Object.entries(parsed)) {
            if (typeof credential !== "object" || credential === null || Array.isArray(credential)) {
                throw new Error(`Invalid auth.json credential for provider "${providerId}"`);
            }
            const value = credential;
            if (value.type === "api_key") {
                const validKey = value.key === undefined || typeof value.key === "string";
                const validEnv = value.env === undefined ||
                    (typeof value.env === "object" &&
                        value.env !== null &&
                        !Array.isArray(value.env) &&
                        Object.values(value.env).every((entry) => typeof entry === "string"));
                if (validKey && validEnv)
                    continue;
            }
            else if (value.type === "oauth" &&
                typeof value.access === "string" &&
                typeof value.refresh === "string" &&
                typeof value.expires === "number" &&
                Number.isFinite(value.expires)) {
                continue;
            }
            throw new Error(`Invalid auth.json credential for provider "${providerId}"`);
        }
        this.data = parsed;
        return this.data;
    }
    async read(providerId, options) {
        options?.signal?.throwIfAborted();
        const credential = this.load()[providerId];
        options?.signal?.throwIfAborted();
        if (!credential)
            return undefined;
        if (credential.type !== "api_key" || !credential.key || isCommandConfigValue(credential.key)) {
            return structuredClone(credential);
        }
        return { ...credential, key: resolveConfigValue(credential.key, credential.env) };
    }
    async list(options) {
        options?.signal?.throwIfAborted();
        const credentials = Object.entries(this.load()).map(([providerId, credential]) => ({
            providerId,
            type: credential.type,
        }));
        options?.signal?.throwIfAborted();
        return credentials;
    }
    async modify(_providerId, _fn, _options) {
        throw new Error("Read-only credential storage cannot modify auth.json");
    }
    async delete(_providerId, _options) {
        throw new Error("Read-only credential storage cannot modify auth.json");
    }
}
export class InMemoryAuthStorageBackend {
    value;
    asyncChain = Promise.resolve();
    withLock(fn) {
        const { result, next } = fn(this.value);
        if (next !== undefined) {
            this.value = next;
        }
        return result;
    }
    withLockAsync(fn, options) {
        const previous = this.asyncChain;
        const operation = (async () => {
            await previous.catch(() => { });
            options?.signal?.throwIfAborted();
            const { result, next } = await fn(this.value);
            options?.signal?.throwIfAborted();
            if (next !== undefined) {
                this.value = next;
            }
            return result;
        })();
        this.asyncChain = operation.catch(() => { });
        return raceWithAbortSignal(operation, options?.signal);
    }
}
/**
 * Credential storage backed by a JSON file.
 */
export class AuthStorage {
    storage;
    authPath;
    readState;
    constructor(storage, authPath) {
        this.storage = storage;
        this.authPath = authPath;
        this.readState =
            authPath && sharedAuthFileReadState?.authPath === authPath ? sharedAuthFileReadState.readState : { data: {} };
        if (authPath && !sharedAuthFileReadState) {
            sharedAuthFileReadState = { authPath, readState: this.readState };
        }
        if (authPath) {
            const revision = getFileRevision(authPath);
            if (revision !== undefined && revision === this.readState.revision)
                return;
        }
        this.reload();
    }
    static create(authPath = join(getAgentDir(), "auth.json")) {
        const normalizedAuthPath = normalizePath(authPath);
        return new AuthStorage(new FileAuthStorageBackend(normalizedAuthPath), normalizedAuthPath);
    }
    static fromStorage(storage) {
        return new AuthStorage(storage);
    }
    static inMemory(data = {}) {
        const storage = new InMemoryAuthStorageBackend();
        storage.withLock(() => ({ result: undefined, next: JSON.stringify(data, null, 2) }));
        return AuthStorage.fromStorage(storage);
    }
    parseStorageData(content) {
        if (!content) {
            return {};
        }
        return JSON.parse(stripBom(content));
    }
    updateReadState(data, revision) {
        this.readState.data = data;
        this.readState.revision = revision;
    }
    /**
     * Reload credentials from storage.
     */
    reload() {
        let content;
        let revision;
        try {
            this.storage.withLock((current) => {
                content = current;
                revision = this.authPath ? getFileRevision(this.authPath) : undefined;
                return { result: undefined };
            });
            this.updateReadState(this.parseStorageData(content), revision);
        }
        catch {
            // Preserve the last valid in-memory snapshot.
        }
    }
    async reloadFromStorageAsync(options) {
        return this.storage.withLockAsync(async (content) => {
            const currentData = this.parseStorageData(content);
            const revision = this.authPath ? getFileRevision(this.authPath) : undefined;
            this.updateReadState(currentData, revision);
            return { result: currentData };
        }, options);
    }
    async readLatestData(options) {
        options?.signal?.throwIfAborted();
        if (!this.authPath) {
            const reload = this.reloadFromStorageAsync(options);
            return options?.signal ? reload : reload.catch(() => this.readState.data);
        }
        const revision = getFileRevision(this.authPath);
        if (revision !== undefined && revision === this.readState.revision)
            return this.readState.data;
        if (!this.readState.reload) {
            const controller = new AbortController();
            const reload = {
                controller,
                promise: this.reloadFromStorageAsync({ signal: controller.signal }),
                readers: 0,
            };
            this.readState.reload = reload;
            void reload.promise.then(() => {
                if (this.readState.reload === reload)
                    this.readState.reload = undefined;
            }, () => {
                if (this.readState.reload === reload)
                    this.readState.reload = undefined;
            });
        }
        const reload = this.readState.reload;
        reload.readers++;
        try {
            const result = raceWithAbortSignal(reload.promise, options?.signal);
            return options?.signal ? await result : await result.catch(() => this.readState.data);
        }
        finally {
            reload.readers--;
            if (reload.readers === 0 && this.readState.reload === reload) {
                this.readState.reload = undefined;
                reload.controller.abort();
            }
        }
    }
    async read(provider, options) {
        const credential = (await this.readLatestData(options))[provider];
        options?.signal?.throwIfAborted();
        if (credential?.type !== "api_key")
            return credential;
        if (credential.key === undefined)
            return credential;
        return { ...credential, key: resolveConfigValue(credential.key, credential.env) };
    }
    async modify(provider, fn, options) {
        let latestData = this.readState.data;
        let revision;
        const result = await this.storage.withLockAsync(async (content) => {
            const currentData = this.parseStorageData(content);
            const next = await fn(currentData[provider]);
            if (next === undefined) {
                latestData = currentData;
                revision = this.authPath ? getFileRevision(this.authPath) : undefined;
                return { result: currentData[provider] };
            }
            const merged = { ...currentData, [provider]: next };
            latestData = merged;
            return { result: next, next: JSON.stringify(merged, null, 2) };
        }, options);
        this.updateReadState(latestData, revision);
        return result;
    }
    async delete(provider, options) {
        let latestData = this.readState.data;
        await this.storage.withLockAsync(async (content) => {
            const currentData = this.parseStorageData(content);
            delete currentData[provider];
            latestData = currentData;
            return { result: undefined, next: JSON.stringify(currentData, null, 2) };
        }, options);
        this.updateReadState(latestData);
    }
    /** List credential metadata without resolving configured key values. */
    async list(options) {
        const entries = Object.entries(await this.readLatestData(options));
        options?.signal?.throwIfAborted();
        return entries.map(([providerId, credential]) => ({ providerId, type: credential.type }));
    }
}
/**
 * One-off synchronous read of a stored credential from an auth.json file,
 * without instantiating a store or resolving configured key values.
 */
export function readStoredCredential(providerId, authPath = join(getAgentDir(), "auth.json")) {
    try {
        const data = JSON.parse(stripBom(readFileSync(normalizePath(authPath), "utf-8")));
        return data[providerId];
    }
    catch {
        return undefined;
    }
}
//# sourceMappingURL=auth-storage.js.map