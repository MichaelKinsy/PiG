import { FacetKernel } from "./facets/host.js";
import { disposeLoadedFacets } from "./facets/loader.js";
import { RemoteServiceBindingImpl } from "./services/consumer.js";
import { MutableReplicatedStateImpl } from "./services/state.js";
/** Create an active host for one complete set of facets. */
export async function createFacetHost(options) {
    const kernel = new FacetKernel(options);
    await kernel.activate();
    return Object.freeze({
        services: kernel.provider,
        reload: (facets) => kernel.reload(facets),
        dispose: () => kernel.dispose(),
    });
}
export function createStaticFacetLoader(facets) {
    const loadedFacets = Object.freeze([...facets]);
    return {
        async load() {
            return { facets: loadedFacets, async dispose() { } };
        },
    };
}
export function combineFacetLoaders(loaders) {
    return {
        async load() {
            const loaded = [];
            try {
                for (const loader of loaders)
                    loaded.push(await loader.load());
            }
            catch (error) {
                const cleanupErrors = await disposeLoadedFacets(loaded.reverse());
                if (cleanupErrors.length > 0) {
                    throw new AggregateError([error, ...cleanupErrors], "Facet loading and cleanup failed");
                }
                throw error;
            }
            let disposed = false;
            return {
                facets: Object.freeze(loaded.flatMap(({ facets }) => facets)),
                async dispose() {
                    if (disposed)
                        return;
                    disposed = true;
                    const errors = await disposeLoadedFacets([...loaded].reverse());
                    if (errors.length === 1)
                        throw errors[0];
                    if (errors.length > 1)
                        throw new AggregateError(errors, "Failed to dispose loaded facets");
                },
            };
        },
    };
}
export function defineFacet(facet) {
    return facet;
}
export function defineService(id, options) {
    if (id.length === 0)
        throw new TypeError("Service ID must not be empty");
    // TODO: check if the reserved namespace should be part of Chord.
    if (id.startsWith("$chord."))
        throw new TypeError("Service IDs beginning with $chord. are reserved");
    return Object.freeze({ id, local: options?.local ?? false });
}
export function createRemoteServiceBinding(options) {
    return new RemoteServiceBindingImpl(options);
}
export function replicatedState(initial) {
    return new MutableReplicatedStateImpl(initial);
}
//# sourceMappingURL=api.js.map