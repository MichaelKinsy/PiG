import { dirname, join } from "node:path";
import { createModels, lazyStream, ModelsError, normalizeContext, } from "../../pi-ai/sdk-bundle/index.js";
import * as builtinProviderCatalog from "../../pi-ai/sdk-bundle/providers.js";
import { getAgentDir } from "../config.js";
import { operationSignal, raceWithAbortSignal } from "../utils/abort.js";
import { AuthStorage as DefaultAuthStorage } from "./auth-storage.js";
import { ModelConfig } from "./model-config.js";
import { FileModelsStore, InMemoryCodingAgentModelsStore } from "./models-store.js";
import { composeModelProvider, configuredRequestAuthStatus, resolveCompatibilityRequestConfig, resolveConfiguredModelHeaders, validateExtensionProvider, } from "./provider-composer.js";
import { withRemoteCatalog } from "./remote-catalog-provider.js";
import { RuntimeCredentials } from "./runtime-credentials.js";
/** Credentials changed successfully, but the local model/auth snapshot could not be synchronized. */
export class CredentialSynchronizationError extends Error {
    providerId;
    operation;
    credential;
    constructor(providerId, operation, credential, options) {
        super(`Credential ${operation} committed for ${providerId}, but local synchronization failed`, options);
        this.name = "CredentialSynchronizationError";
        this.providerId = providerId;
        this.operation = operation;
        this.credential = credential;
    }
}
function mergeHeaders(base, override) {
    if (!base && !override)
        return undefined;
    const merged = { ...base };
    for (const [name, value] of Object.entries(override ?? {})) {
        const lowerName = name.toLowerCase();
        for (const existingName of Object.keys(merged)) {
            if (existingName.toLowerCase() === lowerName)
                delete merged[existingName];
        }
        merged[name] = value;
    }
    return merged;
}
/** Configured pi-ai Models collection used by coding-agent and SDK consumers. */
export class ModelRuntime {
    models;
    credentials;
    defaultBuiltins;
    builtins = new Map();
    nativeExtensionProviders = new Map();
    extensionProviders = new Map();
    compositionErrors = new Map();
    modelsPath;
    modelNetworkEnabled;
    config;
    snapshot = {
        all: [],
        available: [],
        configuredProviders: new Set(),
        storedProviders: new Set(),
        auth: new Map(),
    };
    availabilityRefreshSeq = 0;
    availabilityErrorSeq = 0;
    providerAvailabilitySeq = new Map();
    availabilityError;
    credentialOperations = new Map();
    constructor(credentials, config, modelsPath, modelsStore, providers, modelNetworkEnabled) {
        this.credentials = credentials;
        this.config = config;
        this.modelsPath = modelsPath;
        this.modelNetworkEnabled = modelNetworkEnabled;
        this.defaultBuiltins = new Map(providers.map((provider) => [provider.id, provider]));
        for (const [providerId, provider] of this.defaultBuiltins)
            this.builtins.set(providerId, provider);
        this.models = createModels({ credentials, modelsStore });
        this.rebuildProviders();
    }
    static async create(options = {}) {
        const credentials = new RuntimeCredentials(options.credentials ?? DefaultAuthStorage.create(options.authPath));
        const modelsPath = options.modelsPath === null ? undefined : (options.modelsPath ?? join(getAgentDir(), "models.json"));
        const config = await ModelConfig.load(modelsPath);
        const modelsStore = options.modelsStore ??
            (modelsPath
                ? new FileModelsStore(options.modelsStorePath ?? join(dirname(modelsPath), "models-store.json"))
                : new InMemoryCodingAgentModelsStore());
        const builtinModelDataGeneratedAt = builtinProviderCatalog.getBuiltinModelDataGeneratedAt();
        const providers = builtinProviderCatalog
            .builtinProviders()
            .map((provider) => provider.id === "radius"
            ? provider
            : withRemoteCatalog(provider, options.catalogBaseUrl, builtinModelDataGeneratedAt));
        const runtime = new ModelRuntime(credentials, config, modelsPath, modelsStore, providers, process.env.PI_OFFLINE === undefined);
        runtime.configureRadiusProviders();
        runtime.rebuildProviders();
        const refreshFromNetwork = runtime.modelNetworkEnabled && options.allowModelNetwork === true;
        const controller = refreshFromNetwork && options.modelRefreshTimeoutMs !== undefined ? new AbortController() : undefined;
        const timeout = controller ? setTimeout(() => controller.abort(), options.modelRefreshTimeoutMs) : undefined;
        const signal = controller
            ? options.signal
                ? AbortSignal.any([options.signal, controller.signal])
                : controller.signal
            : options.signal;
        try {
            if (options.refreshOnCreate !== false) {
                await runtime.refresh({ allowNetwork: refreshFromNetwork, signal });
            }
        }
        finally {
            if (timeout)
                clearTimeout(timeout);
        }
        return runtime;
    }
    configureRadiusProviders() {
        this.builtins.clear();
        for (const [providerId, provider] of this.defaultBuiltins)
            this.builtins.set(providerId, provider);
        for (const providerId of this.config.getProviderIds()) {
            const config = this.config.getProvider(providerId);
            if (config?.oauth !== "radius" || !config.baseUrl)
                continue;
            this.builtins.set(providerId, builtinProviderCatalog.radiusProvider({
                id: providerId,
                name: config.name ?? providerId,
                gateway: config.baseUrl.replace(/\/v1\/?$/u, ""),
            }));
        }
    }
    providerIds() {
        return new Set([
            ...this.builtins.keys(),
            ...this.nativeExtensionProviders.keys(),
            ...this.config.getProviderIds(),
            ...this.extensionProviders.keys(),
        ]);
    }
    recomposeProvider(providerId) {
        const base = this.nativeExtensionProviders.get(providerId) ?? this.builtins.get(providerId);
        const extension = this.extensionProviders.get(providerId);
        if (!base && !this.config.getProvider(providerId) && !extension) {
            this.models.deleteProvider(providerId);
            this.compositionErrors.delete(providerId);
            return;
        }
        if (base && !this.config.getProvider(providerId) && !extension) {
            // No overlays: use the builtin untouched so its auth/login/stream behavior is exact.
            this.models.setProvider(base);
            this.compositionErrors.delete(providerId);
            return;
        }
        try {
            this.models.setProvider(composeModelProvider(providerId, base, this.config, extension));
            this.compositionErrors.delete(providerId);
        }
        catch (error) {
            this.compositionErrors.set(providerId, error instanceof Error ? error.message : String(error));
            if (base)
                this.models.setProvider(base);
            else
                this.models.deleteProvider(providerId);
        }
    }
    rebuildProviders() {
        this.models.clearProviders();
        this.compositionErrors.clear();
        for (const providerId of this.providerIds())
            this.recomposeProvider(providerId);
        this.updateModelSnapshot();
    }
    updateModelSnapshot() {
        const all = [...this.models.getModels()];
        this.snapshot = {
            ...this.snapshot,
            all,
            available: all.filter((model) => this.snapshot.configuredProviders.has(model.provider)),
        };
    }
    async runAvailabilityRefresh(seq, errorSeq, signal) {
        const providers = this.models.getProviders();
        const [available, checks, credentials] = await Promise.all([
            this.models.getAvailable(undefined, { signal }),
            Promise.all(providers.map(async (provider) => [
                provider.id,
                await this.models.checkAuth(provider.id, { signal }),
            ])),
            this.credentials.list({ signal }),
        ]);
        if (seq !== this.availabilityRefreshSeq)
            return;
        const auth = new Map(checks);
        const configuredProviders = new Set(checks
            .filter((entry) => entry[1] !== undefined)
            .map(([providerId]) => providerId));
        this.snapshot = {
            all: [...this.models.getModels()],
            available: [...available],
            configuredProviders,
            storedProviders: new Set(credentials.map((entry) => entry.providerId)),
            auth,
        };
        if (errorSeq === this.availabilityErrorSeq)
            this.availabilityError = undefined;
    }
    queueAvailabilityRefresh(signal) {
        const seq = ++this.availabilityRefreshSeq;
        for (const [providerId, providerSeq] of this.providerAvailabilitySeq) {
            this.providerAvailabilitySeq.set(providerId, providerSeq + 1);
        }
        const errorSeq = ++this.availabilityErrorSeq;
        const effectiveSignal = operationSignal(signal);
        return this.runAvailabilityRefresh(seq, errorSeq, effectiveSignal).catch((error) => {
            if (errorSeq === this.availabilityErrorSeq && !effectiveSignal.aborted) {
                this.availabilityError = error instanceof Error ? error.message : String(error);
            }
            throw error;
        });
    }
    async refreshProviderAvailability(providerId, signal) {
        // Invalidate any full availability pass that started before this credential change.
        ++this.availabilityRefreshSeq;
        const providerSeq = (this.providerAvailabilitySeq.get(providerId) ?? 0) + 1;
        this.providerAvailabilitySeq.set(providerId, providerSeq);
        const errorSeq = ++this.availabilityErrorSeq;
        try {
            const [available, auth, credential] = await Promise.all([
                this.models.getAvailable(providerId, { signal }),
                this.models.checkAuth(providerId, { signal }),
                this.credentials.read(providerId, { signal }),
            ]);
            signal.throwIfAborted();
            if (this.providerAvailabilitySeq.get(providerId) !== providerSeq)
                return;
            const configuredProviders = new Set(this.snapshot.configuredProviders);
            const storedProviders = new Set(this.snapshot.storedProviders);
            const authByProvider = new Map(this.snapshot.auth);
            if (auth) {
                configuredProviders.add(providerId);
                authByProvider.set(providerId, auth);
            }
            else {
                configuredProviders.delete(providerId);
                authByProvider.delete(providerId);
            }
            if (credential)
                storedProviders.add(providerId);
            else
                storedProviders.delete(providerId);
            const all = [...this.models.getModels()];
            const availableById = new Map([...this.snapshot.available.filter((model) => model.provider !== providerId), ...available].map((model) => [
                `${model.provider}\0${model.id}`,
                model,
            ]));
            this.snapshot = {
                all,
                available: all.flatMap((model) => availableById.get(`${model.provider}\0${model.id}`) ?? []),
                configuredProviders,
                storedProviders,
                auth: authByProvider,
            };
            if (errorSeq === this.availabilityErrorSeq)
                this.availabilityError = undefined;
        }
        catch (error) {
            if (this.providerAvailabilitySeq.get(providerId) === providerSeq &&
                errorSeq === this.availabilityErrorSeq &&
                !signal.aborted) {
                this.availabilityError = error instanceof Error ? error.message : String(error);
            }
            throw error;
        }
    }
    getProviders() {
        return this.models.getProviders();
    }
    getProvider(providerId) {
        return this.models.getProvider(providerId);
    }
    getModels(providerId) {
        return this.models.getModels(providerId);
    }
    getModel(providerId, modelId) {
        return this.models.getModel(providerId, modelId);
    }
    async checkAuth(providerId, options) {
        return this.models.checkAuth(providerId, options);
    }
    async getAvailable(providerId, options) {
        if (providerId) {
            const errorSeq = ++this.availabilityErrorSeq;
            try {
                const available = await this.models.getAvailable(providerId, options);
                if (errorSeq === this.availabilityErrorSeq)
                    this.availabilityError = undefined;
                return available;
            }
            catch (error) {
                if (errorSeq === this.availabilityErrorSeq && !options?.signal?.aborted) {
                    this.availabilityError = error instanceof Error ? error.message : String(error);
                }
                throw error;
            }
        }
        await this.queueAvailabilityRefresh(options?.signal);
        return this.snapshot.available;
    }
    getAvailableSnapshot() {
        return this.snapshot.available;
    }
    getError() {
        const errors = [];
        const configError = this.config.getError();
        if (configError)
            errors.push(configError);
        for (const [providerId, error] of this.compositionErrors) {
            errors.push(`Provider "${providerId}": ${error}`);
        }
        if (this.availabilityError)
            errors.push(`Availability refresh: ${this.availabilityError}`);
        return errors.length > 0 ? errors.join("\n\n") : undefined;
    }
    getRegisteredProviderConfig(providerId) {
        return this.extensionProviders.get(providerId);
    }
    getRegisteredProviderIds() {
        return [...new Set([...this.extensionProviders.keys(), ...this.nativeExtensionProviders.keys()])];
    }
    getRegisteredNativeProvider(providerId) {
        return this.nativeExtensionProviders.get(providerId);
    }
    /** @internal Compatibility fallback for ModelRegistry when provider auth is unconfigured. */
    getCompatibilityRequestConfig(model) {
        return resolveCompatibilityRequestConfig(model, this.config.getProvider(model.provider), this.extensionProviders.get(model.provider));
    }
    isUsingOAuth(providerId) {
        return this.snapshot.auth.get(providerId)?.type === "oauth";
    }
    isUsingSubscription(providerId) {
        return this.isUsingOAuth(providerId) && this.models.getProvider(providerId)?.auth.oauth?.isSubscription === true;
    }
    hasConfiguredAuth(providerId) {
        return this.snapshot.configuredProviders.has(providerId);
    }
    async getAuth(providerOrModel, overrides = {}) {
        if (typeof providerOrModel === "string")
            return this.models.getAuth(providerOrModel, overrides);
        const resolution = await this.models.getAuth(providerOrModel, overrides);
        if (!resolution)
            return undefined;
        const configuredHeaders = resolveConfiguredModelHeaders(providerOrModel, this.config.getProvider(providerOrModel.provider), this.extensionProviders.get(providerOrModel.provider), { ...(resolution.env ?? {}), ...(overrides.env ?? {}) });
        return {
            ...resolution,
            auth: {
                ...resolution.auth,
                headers: mergeHeaders(resolution.auth.headers, configuredHeaders),
            },
        };
    }
    enqueueCredentialOperation(providerId, signal, task) {
        const previous = this.credentialOperations.get(providerId) ?? Promise.resolve();
        let markStarted;
        const started = new Promise((resolve) => {
            markStarted = resolve;
        });
        const operation = (async () => {
            await previous.catch(() => { });
            signal.throwIfAborted();
            markStarted?.();
            return task();
        })();
        const tail = operation.catch(() => { });
        this.credentialOperations.set(providerId, tail);
        void tail.then(() => {
            if (this.credentialOperations.get(providerId) === tail)
                this.credentialOperations.delete(providerId);
        });
        return raceWithAbortSignal(started, signal).then(() => operation);
    }
    async synchronizeCredentialState(providerId, operation, credential, signal) {
        try {
            signal.throwIfAborted();
            this.recomposeProvider(providerId);
            const compositionError = this.compositionErrors.get(providerId);
            if (compositionError)
                throw new Error(compositionError);
            const result = await this.models.refresh({ allowNetwork: false, providers: [providerId], signal });
            if (result.aborted)
                signal.throwIfAborted();
            const refreshError = result.errors.get(providerId);
            if (refreshError)
                throw refreshError;
            this.updateModelSnapshot();
            await this.refreshProviderAvailability(providerId, signal);
        }
        catch (cause) {
            throw new CredentialSynchronizationError(providerId, operation, credential, { cause });
        }
    }
    setRuntimeApiKey(providerId, apiKey, options = {}) {
        const signal = operationSignal(options.signal);
        return this.enqueueCredentialOperation(providerId, signal, async () => {
            this.credentials.setRuntimeApiKey(providerId, apiKey);
            await this.synchronizeCredentialState(providerId, "setRuntimeApiKey", { type: "api_key", key: apiKey }, signal);
        });
    }
    removeRuntimeApiKey(providerId, options = {}) {
        const signal = operationSignal(options.signal);
        return this.enqueueCredentialOperation(providerId, signal, async () => {
            this.credentials.removeRuntimeApiKey(providerId);
            await this.synchronizeCredentialState(providerId, "removeRuntimeApiKey", undefined, signal);
        });
    }
    listCredentials(options) {
        return this.credentials.list(options);
    }
    getProviderAuthStatus(providerId) {
        if (this.credentials.hasRuntimeApiKey(providerId))
            return { configured: true, source: "runtime" };
        if (this.snapshot.storedProviders.has(providerId))
            return { configured: true, source: "stored" };
        const configured = configuredRequestAuthStatus(this.config.getProvider(providerId), this.extensionProviders.get(providerId));
        if (configured)
            return configured;
        const check = this.snapshot.auth.get(providerId);
        return check ? { configured: true, source: "environment", label: check.source } : { configured: false };
    }
    async prepareRequest(model, options) {
        const provider = this.models.getProvider(model.provider);
        if (!provider)
            throw new ModelsError("provider", `Unknown provider: ${model.provider}`);
        const resolution = await this.getAuth(model, {
            apiKey: options?.apiKey,
            env: options?.env,
            signal: options?.signal,
        });
        if (!resolution)
            throw new ModelsError("auth", `Provider is not configured: ${model.provider}`);
        const { transformHeaders, ...rawProviderOptions } = options ?? {};
        const providerOptions = rawProviderOptions;
        let headers = mergeHeaders(resolution.auth.headers, providerOptions.headers);
        if (transformHeaders)
            headers = await transformHeaders(headers ?? {});
        const env = resolution.env || providerOptions.env
            ? { ...(resolution.env ?? {}), ...(providerOptions.env ?? {}) }
            : undefined;
        return {
            provider,
            model: resolution.auth.baseUrl ? { ...model, baseUrl: resolution.auth.baseUrl } : model,
            options: {
                ...providerOptions,
                apiKey: providerOptions.apiKey ?? resolution.auth.apiKey,
                headers,
                env,
            },
        };
    }
    stream(model, context, options) {
        const transcript = normalizeContext(context);
        return lazyStream(model, async () => {
            const prepared = await this.prepareRequest(model, options);
            return prepared.provider.stream(prepared.model, transcript, prepared.options);
        });
    }
    complete(model, context, options) {
        return this.stream(model, context, options).result();
    }
    streamSimple(model, context, options) {
        const transcript = normalizeContext(context);
        return lazyStream(model, async () => {
            const prepared = await this.prepareRequest(model, options);
            return prepared.provider.streamSimple(prepared.model, transcript, prepared.options);
        });
    }
    completeSimple(model, context, options) {
        return this.streamSimple(model, context, options).result();
    }
    streamDeferred(model, handle, options) {
        return lazyStream(model, async () => {
            const prepared = await this.prepareRequest(model, options);
            if (!prepared.provider.fetchDeferred) {
                throw new ModelsError("provider", `Provider ${model.provider} does not support deferred responses`);
            }
            return prepared.provider.fetchDeferred(prepared.model, handle, prepared.options);
        });
    }
    async fetchDeferred(model, handle, options) {
        return this.streamDeferred(model, handle, options).result();
    }
    async cancelDeferred(model, handle, options) {
        const prepared = await this.prepareRequest(model, options);
        if (!prepared.provider.cancelDeferred) {
            throw new ModelsError("provider", `Provider ${model.provider} does not support deferred responses`);
        }
        await prepared.provider.cancelDeferred(prepared.model, handle, prepared.options);
    }
    login(providerId, type, interaction) {
        const signal = operationSignal(interaction.signal);
        return this.enqueueCredentialOperation(providerId, signal, async () => {
            const credential = await this.models.login(providerId, type, { ...interaction, signal });
            await this.synchronizeCredentialState(providerId, "login", credential, signal);
            return credential;
        });
    }
    logout(providerId, options = {}) {
        const signal = operationSignal(options.signal);
        return this.enqueueCredentialOperation(providerId, signal, async () => {
            await this.models.logout(providerId, { signal });
            await this.synchronizeCredentialState(providerId, "logout", undefined, signal);
        });
    }
    async refresh(options = {}) {
        this.config = await ModelConfig.load(this.modelsPath);
        this.configureRadiusProviders();
        if (options.providers) {
            for (const providerId of new Set(options.providers))
                this.recomposeProvider(providerId);
            this.updateModelSnapshot();
        }
        else {
            this.rebuildProviders();
        }
        const refreshOptions = {
            ...options,
            allowNetwork: options.allowNetwork ?? this.modelNetworkEnabled,
        };
        // Published pi-ai builds before ModelsStore returned void and accepted a provider ID.
        // The fallback keeps source-mode CLI tests working without rebuilding workspace dependencies.
        const result = (await this.models.refresh(refreshOptions)) ?? {
            aborted: refreshOptions.signal?.aborted ?? false,
            errors: new Map(),
        };
        const errors = new Map(result.errors);
        this.updateModelSnapshot();
        if (options.providers) {
            await Promise.all([...new Set(options.providers)].map(async (providerId) => {
                try {
                    await this.refreshProviderAvailability(providerId, operationSignal(options.signal));
                }
                catch (error) {
                    if (!options.signal?.aborted) {
                        errors.set(providerId, error instanceof Error ? error : new Error(String(error)));
                    }
                }
            }));
        }
        else {
            try {
                await this.queueAvailabilityRefresh(options.signal);
            }
            catch {
                // Availability errors are recorded by the latest pass; refreshed models remain usable.
            }
        }
        return { aborted: result.aborted || (options.signal?.aborted ?? false), errors };
    }
    registerNativeProvider(provider) {
        if (!provider.id.trim())
            throw new Error("Provider id must not be empty.");
        this.extensionProviders.delete(provider.id);
        this.nativeExtensionProviders.set(provider.id, provider);
        this.recomposeProvider(provider.id);
        this.updateModelSnapshot();
        void this.refresh({ allowNetwork: false });
    }
    registerProvider(providerId, config) {
        // Validate the incoming registration on its own, like the legacy registry:
        // a broken re-registration must throw without touching the stored config.
        validateExtensionProvider(providerId, this.builtins.get(providerId), this.config.getProvider(providerId), config);
        this.nativeExtensionProviders.delete(providerId);
        // Re-registration merges defined values over the previous registration and
        // preserves undefined ones, matching the legacy ModelRegistry contract.
        const previous = this.extensionProviders.get(providerId);
        const effective = { ...previous };
        for (const [key, value] of Object.entries(config)) {
            if (value !== undefined)
                effective[key] = value;
        }
        this.extensionProviders.set(providerId, effective);
        this.recomposeProvider(providerId);
        this.updateModelSnapshot();
        if (this.snapshot.storedProviders.has(providerId) ||
            configuredRequestAuthStatus(this.config.getProvider(providerId), effective)?.configured) {
            const configuredProviders = new Set(this.snapshot.configuredProviders).add(providerId);
            const auth = new Map(this.snapshot.auth);
            // Provisional entry until the async refresh lands; never clobber a real check result.
            if (!auth.get(providerId)) {
                auth.set(providerId, {
                    type: effective.oauth && !effective.apiKey ? "oauth" : "api_key",
                    source: "configured provider",
                });
            }
            this.snapshot = {
                ...this.snapshot,
                auth,
                configuredProviders,
                available: this.snapshot.all.filter((model) => configuredProviders.has(model.provider)),
            };
        }
        void this.refresh({ allowNetwork: false });
    }
    unregisterProvider(providerId) {
        this.extensionProviders.delete(providerId);
        this.nativeExtensionProviders.delete(providerId);
        this.recomposeProvider(providerId);
        this.updateModelSnapshot();
        void this.refresh({ allowNetwork: false });
    }
}
//# sourceMappingURL=model-runtime.js.map