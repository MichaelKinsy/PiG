import { BACKGROUND_CONTEXT } from "../context/index.js";
import { RemoteServiceBindingImpl } from "../services/consumer.js";
import { ServiceSlot } from "../services/handle.js";
import { InstanceDirectory } from "../services/instances.js";
import { createLoopbackServiceTransport } from "../services/loopback.js";
import { RemoteServiceProvider, validateRemoteServiceImplementation } from "../services/provider.js";
import { MutableReplicatedStateImpl } from "../services/state.js";
class FacetLifecycle {
    id;
    #effects = [];
    #observations = [];
    #activate = [];
    #state = "setting_up";
    #serviceAccess = false;
    constructor(id) {
        this.id = id;
    }
    assertSettingUp(operation) {
        if (this.#state !== "setting_up") {
            throw new Error(`Facet ${this.id} can ${operation} only during setup`);
        }
    }
    assertRunning(operation) {
        if (this.#state !== "setting_up" && this.#state !== "active") {
            throw new Error(`Facet ${this.id} cannot ${operation} while ${this.#state}`);
        }
    }
    assertActive(operation) {
        if (this.#state !== "active")
            throw new Error(`Facet ${this.id} can ${operation} only while active`);
    }
    assertServiceAccess() {
        if (!this.#serviceAccess) {
            throw new Error(`Facet ${this.id} service handles cannot be used while ${this.#state}`);
        }
    }
    revoke() {
        this.#serviceAccess = false;
    }
    own(disposal) {
        this.assertRunning("own resources");
        this.#effects.push(disposal);
    }
    observe(start) {
        this.assertSettingUp("observe services");
        this.#observations.push(start);
    }
    onActivate(callback) {
        this.assertSettingUp("register activation callbacks");
        this.#activate.push(callback);
    }
    prepared() {
        this.assertSettingUp("finish setup");
        this.#state = "prepared";
    }
    async activate() {
        if (this.#state !== "prepared")
            throw new Error(`Facet ${this.id} is not prepared`);
        this.#state = "active";
        this.#serviceAccess = true;
        for (const start of this.#observations)
            this.#effects.push(start());
        for (const callback of this.#activate)
            await callback();
    }
    async dispose() {
        if (this.#state === "dead")
            return;
        this.#state = "disposing";
        const errors = [];
        for (const effect of this.#effects.splice(0).reverse()) {
            try {
                await effect();
            }
            catch (error) {
                errors.push(error);
            }
        }
        this.#observations.length = 0;
        this.#activate.length = 0;
        this.#serviceAccess = false;
        this.#state = "dead";
        if (errors.length === 1)
            throw errors[0];
        if (errors.length > 1)
            throw new AggregateError(errors, `Failed to dispose facet ${this.id}`);
    }
}
class LocalKeyedServiceRegistry {
    #registrations = new Map();
    #disposed = false;
    constructor(services, onError) {
        const ids = services.map(({ id }) => id);
        if (new Set(ids).size !== ids.length)
            throw new TypeError("Local keyed service registry has duplicate IDs");
        for (const serviceId of ids) {
            this.#registrations.set(serviceId, {
                generations: new Map(),
                directory: new InstanceDirectory({ ready: true, onError }),
            });
        }
    }
    spawn(service, key, implementation) {
        this.#assertActive();
        if (key.length === 0)
            throw new TypeError("Local service instance key must not be empty");
        if (typeof implementation !== "object" || implementation === null || Array.isArray(implementation)) {
            throw new TypeError(`Local service ${service.id} implementation must be an object`);
        }
        const registration = this.#registration(service.id);
        if (registration.directory.get(key) !== undefined) {
            throw new Error(`Local service ${service.id} already has a live instance with key ${key}`);
        }
        const generation = (registration.generations.get(key) ?? 0) + 1;
        registration.generations.set(key, generation);
        const instance = {
            key,
            generation,
            service: implementation,
            deactivate() { },
        };
        registration.directory.insert(instance);
        let closed = false;
        return () => {
            if (closed)
                return;
            closed = true;
            registration.directory.remove(instance);
        };
    }
    observe(service, handler) {
        this.#assertActive();
        return this.#registration(service.id).directory.observe(handler);
    }
    dispose() {
        if (this.#disposed)
            return;
        this.#disposed = true;
        for (const registration of this.#registrations.values())
            registration.directory.dispose();
        this.#registrations.clear();
    }
    #registration(serviceId) {
        const registration = this.#registrations.get(serviceId);
        if (registration === undefined)
            throw new Error(`Local keyed service ${serviceId} is not registered`);
        return registration;
    }
    #assertActive() {
        if (this.#disposed)
            throw new Error("Local keyed service registry is disposed");
    }
}
class HostServiceSlots {
    #singletons = new Map();
    #keyedSources = new Map();
    getSingleton(service, assertAccess) {
        let slot = this.#singletons.get(service.id);
        if (slot === undefined) {
            slot = new ServiceSlot(service.id, !service.local);
            this.#singletons.set(service.id, slot);
        }
        return slot.view(assertAccess);
    }
    hasSingleton(serviceId) {
        return this.#singletons.has(serviceId);
    }
    observe(service, assertAccess, handler) {
        const source = this.#keyedSources.get(service.id);
        if (source === undefined)
            throw new Error(`Service ${service.id} is disconnected`);
        let stopped = false;
        const stop = source.observe(service, (target, context) => {
            const slot = new ServiceSlot(service.id, !service.local);
            slot.bind(target);
            return handler(slot.view(() => {
                assertAccess();
                if (stopped || context.abortSignal?.aborted) {
                    throw new Error(`Keyed service ${service.id} observation is closed`);
                }
            }), context);
        });
        return () => {
            if (stopped)
                return;
            stopped = true;
            stop();
        };
    }
    bindSingleton(serviceId, target) {
        this.#singletons.get(serviceId)?.bind(target);
    }
    bindKeyed(serviceId, services) {
        this.#keyedSources.set(serviceId, services);
    }
    dispose() {
        for (const slot of this.#singletons.values())
            slot.unbind();
        this.#singletons.clear();
        this.#keyedSources.clear();
    }
}
class StagedServiceSpawner {
    #lifecycle;
    #validate;
    #instances = new Map();
    #installer;
    constructor(lifecycle, validate) {
        this.#lifecycle = lifecycle;
        this.#validate = validate;
    }
    connect(installer) {
        if (this.#installer !== undefined)
            throw new Error("Facet service provider is already connected");
        this.#installer = installer;
        for (const instance of this.#instances.values()) {
            instance.release = installer(instance.key, instance.implementation);
        }
    }
    spawn(key, implementation) {
        this.#lifecycle.assertActive("spawn service instances");
        this.#validate(key, implementation);
        if (this.#instances.has(key))
            throw new Error(`Facet service already has a live instance with key ${key}`);
        const instance = { key, implementation };
        this.#instances.set(key, instance);
        if (this.#installer !== undefined)
            instance.release = this.#installer(key, implementation);
        const close = () => {
            if (this.#instances.get(key) !== instance)
                return;
            this.#instances.delete(key);
            instance.release?.();
        };
        this.#lifecycle.own(close);
        return close;
    }
}
/** Private lifecycle and dependency kernel behind the atomic host entry point. */
export class FacetKernel {
    #initialFacets;
    #serviceSources;
    #onError;
    #facets = new Map();
    #serviceSlots;
    #sourceBindings = new Map();
    #activationOrder = [];
    #provider;
    #internalServices;
    #localKeyedServices;
    #phase = "setup";
    constructor(options) {
        const ids = options.facets.map((facet) => facet.id);
        if (ids.some((id) => id.length === 0))
            throw new Error("Facet ID must not be empty");
        if (new Set(ids).size !== ids.length)
            throw new Error("Facet IDs must be unique within a generation");
        this.#initialFacets = options.facets;
        this.#serviceSources = options.serviceSources ?? [];
        this.#onError = options.onError ?? (() => { });
        this.#serviceSlots = new HostServiceSlots();
    }
    get provider() {
        if (this.#provider === undefined)
            throw new Error("Facet service provider is not assembled");
        return this.#provider;
    }
    #createFacetRuntime(facetId) {
        return {
            facetId,
            requires: [],
            provides: [],
            lifecycle: new FacetLifecycle(facetId),
            provisions: [],
            singletonViews: new Map(),
        };
    }
    #setupFacet(facet, record) {
        const result = facet.setup(this.#environment(record));
        if (isPromiseLike(result)) {
            void Promise.resolve(result).catch(() => { });
            throw new Error(`Facet ${facet.id} setup must be synchronous`);
        }
        record.lifecycle.prepared();
    }
    async activate() {
        const records = [];
        try {
            for (const facet of this.#initialFacets) {
                const record = this.#createFacetRuntime(facet.id);
                this.#facets.set(facet.id, record);
                this.#setupFacet(facet, record);
                records.push(record);
            }
            this.#phase = "assembling";
            const externalServices = await this.#resolveExternalServices(records);
            this.#activationOrder = validateFacets(records, externalServices);
            this.#assembleProviders();
            this.#bindServices(externalServices);
            this.#phase = "connecting";
            await Promise.all([...this.#sourceBindings.values(), this.#internalServiceBinding].map((services) => services.ready(BACKGROUND_CONTEXT)));
            this.#phase = "activating";
            for (const id of this.#activationOrder)
                await this.#facets.get(id).lifecycle.activate();
            this.#phase = "active";
        }
        catch (error) {
            const cleanupErrors = await this.#terminate();
            if (cleanupErrors.length > 0) {
                throw new AggregateError([error, ...cleanupErrors], "Facet generation startup and cleanup failed");
            }
            throw error;
        }
    }
    async reload(facets) {
        if (this.#phase !== "active")
            throw new Error(`Facet host cannot reload while ${this.#phase}`);
        const ids = facets.map(({ id }) => id);
        if (ids.some((id) => id.length === 0))
            throw new Error("Facet ID must not be empty");
        if (new Set(ids).size !== ids.length)
            throw new Error("Reloaded facet IDs must be unique");
        for (const id of ids) {
            if (!this.#facets.has(id))
                throw new Error(`Facet ${id} is not active`);
        }
        this.#phase = "reloading";
        const staged = [];
        const candidates = [];
        try {
            for (const facet of facets) {
                const record = this.#createFacetRuntime(facet.id);
                staged.push(record);
                this.#setupFacet(facet, record);
                const previous = this.#facets.get(facet.id);
                if (!sameFacetShape(previous, record)) {
                    throw new Error(`Reloaded facet ${facet.id} must preserve its service requirements and provisions`);
                }
                this.#validateReplacementProvisions(record.provisions);
                candidates.push(record);
            }
        }
        catch (error) {
            const cleanupErrors = await disposeFacetRecords(staged.reverse());
            if (cleanupErrors.length > 0) {
                const abortErrors = await this.#abort();
                throw new AggregateError([error, ...cleanupErrors, ...abortErrors], "Facet reload setup and cleanup failed");
            }
            this.#phase = "active";
            throw error;
        }
        const replacements = new Map(candidates.map((record) => [record.facetId, record]));
        const candidateOrder = this.#activationOrder.flatMap((id) => {
            const candidate = replacements.get(id);
            return candidate === undefined ? [] : [candidate];
        });
        try {
            for (const candidate of candidateOrder)
                await candidate.lifecycle.activate();
            for (const candidate of candidateOrder)
                this.#validateReplacementProvisions(candidate.provisions);
        }
        catch (error) {
            const cleanupErrors = await disposeFacetRecords([...candidateOrder].reverse());
            if (cleanupErrors.length > 0) {
                const abortErrors = await this.#abort();
                throw new AggregateError([error, ...cleanupErrors, ...abortErrors], "Facet reload activation and cleanup failed");
            }
            this.#phase = "active";
            throw error;
        }
        const previous = candidateOrder.map(({ facetId }) => this.#facets.get(facetId));
        for (const candidate of candidateOrder)
            this.#facets.set(candidate.facetId, candidate);
        try {
            for (const candidate of candidateOrder) {
                for (const provision of candidate.provisions) {
                    if (provision.kind !== "singleton")
                        continue;
                    if (provision.service.local) {
                        this.#serviceSlots.bindSingleton(provision.service.id, provision.implementation);
                    }
                    else {
                        provision.replace(this.provider);
                    }
                }
            }
            const retirementErrors = await disposeFacetRecords(previous.reverse());
            if (retirementErrors.length === 1)
                throw retirementErrors[0];
            if (retirementErrors.length > 1) {
                throw new AggregateError(retirementErrors, "Failed to retire replaced facets");
            }
            for (const candidate of candidateOrder) {
                for (const provision of candidate.provisions) {
                    if (provision.kind !== "keyed")
                        continue;
                    if (provision.service.local)
                        provision.connectLocal(this.#localKeyedRegistry);
                    else
                        provision.connectRemote(this.provider);
                }
            }
        }
        catch (error) {
            const abortErrors = await this.#abort(previous);
            throw new AggregateError([error, ...abortErrors], "Facet reload failed after cutover");
        }
        this.#phase = "active";
    }
    async dispose() {
        if (this.#phase === "dead")
            return;
        if (this.#phase !== "active")
            throw new Error(`Facet host cannot be disposed while ${this.#phase}`);
        const errors = await this.#terminate();
        if (errors.length === 1)
            throw errors[0];
        if (errors.length > 1)
            throw new AggregateError(errors, "Failed to dispose facet generation");
    }
    #validateReplacementProvisions(provisions) {
        for (const provision of provisions) {
            if (provision.kind !== "singleton" || provision.service.local)
                continue;
            provision.validateReplacement(this.provider);
        }
    }
    #environment(runtime) {
        const { lifecycle, provisions } = runtime;
        return {
            provide: (service, implementation) => {
                lifecycle.assertSettingUp("provide services");
                if (typeof implementation !== "object" || implementation === null || Array.isArray(implementation)) {
                    throw new TypeError(`Service ${service.id} implementation must be an object`);
                }
                recordServiceReference(runtime.provides, service, "singleton");
                provisions.push({
                    kind: "singleton",
                    service,
                    implementation,
                    install: (provider) => provider.provide(service, implementation),
                    validateReplacement: (provider) => provider.validateReplacement(service, implementation),
                    replace: (provider) => provider.replace(service, implementation),
                });
            },
            provideMany: (service) => {
                lifecycle.assertSettingUp("provide service instances");
                recordServiceReference(runtime.provides, service, "keyed");
                const instances = new StagedServiceSpawner(lifecycle, (key, implementation) => {
                    if (key.length === 0)
                        throw new TypeError("Facet service instance key must not be empty");
                    if (typeof implementation !== "object" || implementation === null || Array.isArray(implementation)) {
                        throw new TypeError(`Facet service ${service.id} implementation must be an object`);
                    }
                    if (!service.local)
                        validateRemoteServiceImplementation(service.id, implementation);
                });
                provisions.push({
                    kind: "keyed",
                    service,
                    connectLocal: (registry) => instances.connect((key, implementation) => registry.spawn(service, key, implementation)),
                    connectRemote: (provider) => instances.connect((key, implementation) => provider.spawn(service, key, implementation)),
                });
                return instances;
            },
            use: (service) => {
                lifecycle.assertSettingUp("acquire services");
                recordServiceReference(runtime.requires, service, "singleton");
                let view = runtime.singletonViews.get(service.id);
                if (view === undefined) {
                    view = this.#serviceSlots.getSingleton(service, () => lifecycle.assertServiceAccess());
                    runtime.singletonViews.set(service.id, view);
                }
                return view;
            },
            observe: (service, handler) => {
                lifecycle.assertSettingUp("observe services");
                recordServiceReference(runtime.requires, service, "keyed");
                lifecycle.observe(() => this.#serviceSlots.observe(service, () => lifecycle.assertServiceAccess(), handler));
            },
            replicatedState: (initial) => {
                lifecycle.assertRunning("create replicated state");
                return new MutableReplicatedStateImpl(initial);
            },
            own: (disposal) => lifecycle.own(disposal),
            onActivate: (callback) => lifecycle.onActivate(callback),
            onDeactivate: (callback) => lifecycle.own(callback),
        };
    }
    async #resolveExternalServices(records) {
        const catalogues = await Promise.all(this.#serviceSources.map(async (source) => ({
            source,
            entries: await source.catalogue(BACKGROUND_CONTEXT),
        })));
        const offered = new Map();
        for (const { source, entries } of catalogues) {
            for (const { serviceId, mode } of entries) {
                if (offered.has(serviceId)) {
                    throw new Error(`Facet host service ${serviceId} is offered by more than one source`);
                }
                offered.set(serviceId, { mode, source });
            }
        }
        const local = new Set(records.flatMap(({ provides }) => provides.map(({ serviceId }) => serviceId)));
        const external = new Map();
        for (const { requires } of records) {
            for (const requirement of requires) {
                if (local.has(requirement.serviceId) || external.has(requirement.serviceId))
                    continue;
                let source = offered.get(requirement.serviceId);
                if (source === undefined) {
                    const deferred = this.#serviceSources.filter(({ acceptsUnavailableServices }) => acceptsUnavailableServices);
                    if (deferred.length > 1) {
                        throw new Error(`Facet host service ${requirement.serviceId} has more than one deferred source`);
                    }
                    if (deferred.length === 1)
                        source = { mode: requirement.mode, source: deferred[0] };
                }
                if (source !== undefined) {
                    external.set(requirement.serviceId, { ...source, service: requirement.service });
                }
            }
        }
        const serviceIdsBySource = new Map();
        for (const [serviceId, { source }] of external) {
            let serviceIds = serviceIdsBySource.get(source);
            if (serviceIds === undefined) {
                serviceIds = [];
                serviceIdsBySource.set(source, serviceIds);
            }
            serviceIds.push(serviceId);
        }
        for (const [source, serviceIds] of serviceIdsBySource) {
            this.#sourceBindings.set(source, source.open({
                services: serviceIds.map((id) => ({ id })),
                assertAccess: () => this.#assertServiceTargetAccess(),
                onError: this.#onError,
            }));
        }
        return external;
    }
    #assembleProviders() {
        const provisions = this.#provisions();
        const remoteProvisions = provisions.filter(({ service }) => !service.local);
        const provider = new RemoteServiceProvider(remoteProvisions.map(({ service, kind }) => ({ service, mode: kind })));
        const internalServices = new RemoteServiceBindingImpl({
            services: remoteProvisions.map(({ service }) => service),
            transport: createLoopbackServiceTransport(provider),
            assertAccess: () => this.#assertServiceTargetAccess(),
            onError: this.#onError,
        });
        const localKeyedServices = new LocalKeyedServiceRegistry(provisions.flatMap((provision) => provision.kind === "keyed" && provision.service.local ? [provision.service] : []), this.#onError);
        this.#provider = provider;
        this.#internalServices = internalServices;
        this.#localKeyedServices = localKeyedServices;
        for (const provision of provisions) {
            if (provision.kind === "singleton") {
                if (!provision.service.local)
                    provision.install(provider);
            }
            else if (provision.service.local) {
                provision.connectLocal(localKeyedServices);
            }
            else {
                provision.connectRemote(provider);
            }
        }
    }
    #bindServices(externalServices) {
        for (const provision of this.#provisions()) {
            if (provision.kind === "singleton") {
                if (!this.#serviceSlots.hasSingleton(provision.service.id))
                    continue;
                const target = provision.service.local
                    ? provision.implementation
                    : this.#internalServiceBinding.use(provision.service);
                this.#serviceSlots.bindSingleton(provision.service.id, target);
            }
            else {
                this.#serviceSlots.bindKeyed(provision.service.id, provision.service.local ? this.#localKeyedRegistry : this.#internalServiceBinding);
            }
        }
        for (const [serviceId, { service, mode, source }] of externalServices) {
            const services = this.#sourceBindings.get(source);
            if (services === undefined)
                throw new Error(`Service source for ${serviceId} is not open`);
            if (mode === "singleton") {
                this.#serviceSlots.bindSingleton(serviceId, services.use(service));
            }
            else {
                this.#serviceSlots.bindKeyed(serviceId, services);
            }
        }
    }
    #provisions() {
        return [...this.#facets.values()].flatMap(({ provisions }) => provisions);
    }
    get #localKeyedRegistry() {
        if (this.#localKeyedServices === undefined)
            throw new Error("Facet keyed services are not assembled");
        return this.#localKeyedServices;
    }
    get #internalServiceBinding() {
        if (this.#internalServices === undefined)
            throw new Error("Facet remote services are not assembled");
        return this.#internalServices;
    }
    async #disposeServiceBindings() {
        const bindings = [...this.#sourceBindings.values()];
        this.#sourceBindings.clear();
        if (this.#internalServices !== undefined)
            bindings.push(this.#internalServices);
        this.#internalServices = undefined;
        const results = await Promise.allSettled(bindings.map((services) => services.dispose(BACKGROUND_CONTEXT)));
        return results.flatMap((result) => (result.status === "rejected" ? [result.reason] : []));
    }
    #assertServiceTargetAccess() {
        if (this.#phase !== "activating" &&
            this.#phase !== "active" &&
            this.#phase !== "reloading" &&
            this.#phase !== "disposing") {
            throw new Error(`Facet service targets cannot be used during ${this.#phase}`);
        }
    }
    async #abort(extraRecords = []) {
        for (const record of this.#facets.values())
            record.lifecycle.revoke();
        for (const record of extraRecords)
            record.lifecycle.revoke();
        return this.#terminate(extraRecords);
    }
    async #terminate(extraRecords = []) {
        this.#phase = "disposing";
        const errors = await this.#disposeLifecycles();
        errors.push(...(await disposeFacetRecords([...extraRecords].reverse())));
        try {
            this.#localKeyedServices?.dispose();
        }
        catch (error) {
            errors.push(error);
        }
        this.#localKeyedServices = undefined;
        errors.push(...(await this.#disposeServiceBindings()));
        try {
            this.#serviceSlots.dispose();
        }
        catch (error) {
            errors.push(error);
        }
        try {
            this.#provider?.dispose();
        }
        catch (error) {
            errors.push(error);
        }
        this.#phase = "dead";
        return errors;
    }
    async #disposeLifecycles() {
        const errors = [];
        const order = this.#activationOrder.length > 0 ? [...this.#activationOrder].reverse() : [...this.#facets.keys()].reverse();
        for (const id of order) {
            const record = this.#facets.get(id);
            if (record === undefined)
                continue;
            this.#facets.delete(id);
            try {
                await record.lifecycle.dispose();
            }
            catch (error) {
                errors.push(error);
            }
        }
        return errors;
    }
}
async function disposeFacetRecords(records) {
    const errors = [];
    for (const record of records) {
        try {
            await record.lifecycle.dispose();
        }
        catch (error) {
            errors.push(error);
        }
    }
    return errors;
}
function validateFacets(records, externalServices) {
    const providers = new Map();
    for (const [serviceId, { mode }] of externalServices)
        providers.set(serviceId, { facetId: undefined, mode });
    for (const record of records) {
        for (const provision of record.provides) {
            const existing = providers.get(provision.serviceId);
            if (existing !== undefined) {
                if (existing.mode !== undefined && existing.mode !== provision.mode) {
                    throw new Error(`Service ${provision.serviceId} is provided as both singleton and keyed`);
                }
                if (existing.facetId === undefined) {
                    throw new Error(`Service ${provision.serviceId} is provided by both the host and ${record.facetId}`);
                }
                throw new Error(`Service ${provision.serviceId} is provided by both ${existing.facetId} and ${record.facetId}`);
            }
            providers.set(provision.serviceId, { facetId: record.facetId, mode: provision.mode });
        }
    }
    const dependencies = new Map(records.map((record) => [record.facetId, new Set()]));
    const dependents = new Map(records.map((record) => [record.facetId, new Set()]));
    for (const record of records) {
        for (const requirement of record.requires) {
            const provider = providers.get(requirement.serviceId);
            if (provider === undefined) {
                throw new Error(`Facet ${record.facetId} requires local/${requirement.serviceId}/${requirement.mode}, but no facet provides it`);
            }
            if (provider.mode !== undefined && provider.mode !== requirement.mode) {
                throw new Error(`Facet ${record.facetId} requires ${requirement.serviceId} as ${requirement.mode}, but ${provider.facetId ?? "the host"} provides it as ${provider.mode}`);
            }
            if (provider.facetId === undefined || provider.facetId === record.facetId)
                continue;
            dependencies.get(record.facetId).add(provider.facetId);
            dependents.get(provider.facetId).add(record.facetId);
        }
    }
    return topologicalOrder(records, dependencies, dependents);
}
function topologicalOrder(records, dependencies, dependents) {
    const remaining = new Map([...dependencies].map(([id, values]) => [id, values.size]));
    const ready = records.map((record) => record.facetId).filter((id) => remaining.get(id) === 0);
    const order = [];
    while (ready.length > 0) {
        const id = ready.shift();
        order.push(id);
        for (const dependent of dependents.get(id) ?? []) {
            const count = remaining.get(dependent) - 1;
            remaining.set(dependent, count);
            if (count === 0)
                ready.push(dependent);
        }
    }
    if (order.length !== records.length) {
        const cycle = records.map((record) => record.facetId).filter((id) => remaining.get(id) > 0);
        throw new Error(`Facet dependency cycle: ${cycle.join(", ")}`);
    }
    return order;
}
function recordServiceReference(target, service, mode) {
    if (target.some((reference) => reference.serviceId === service.id && reference.mode === mode))
        return;
    target.push({ serviceId: service.id, service: service, mode });
}
function sameFacetShape(left, right) {
    return sameReferences(left.requires, right.requires) && sameReferences(left.provides, right.provides);
}
function sameReferences(left, right) {
    return (left.length === right.length &&
        left.every((reference) => right.some((other) => other.serviceId === reference.serviceId && other.mode === reference.mode)));
}
function isPromiseLike(value) {
    return typeof value === "object" && value !== null && "then" in value && typeof value.then === "function";
}
//# sourceMappingURL=host.js.map