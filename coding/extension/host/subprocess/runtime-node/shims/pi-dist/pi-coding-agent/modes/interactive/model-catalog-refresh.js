import { raceWithAbortSignal } from "../../utils/abort.js";
class ModelCatalogRefreshCoordinator {
    activeByRuntime = new WeakMap();
    refresh(modelRuntime, signal) {
        signal.throwIfAborted();
        let active = this.activeByRuntime.get(modelRuntime);
        if (!active) {
            const controller = new AbortController();
            let created;
            const operation = modelRuntime.refresh({ signal: controller.signal });
            const promise = raceWithAbortSignal(operation, controller.signal).finally(() => {
                if (this.activeByRuntime.get(modelRuntime) === created) {
                    this.activeByRuntime.delete(modelRuntime);
                }
            });
            created = { controller, promise, waiters: 0 };
            active = created;
            this.activeByRuntime.set(modelRuntime, active);
        }
        active.waiters++;
        return raceWithAbortSignal(active.promise, signal).finally(() => {
            active.waiters--;
            if (active.waiters === 0 && this.activeByRuntime.get(modelRuntime) === active) {
                active.controller.abort();
            }
        });
    }
}
const modelCatalogRefreshCoordinator = new ModelCatalogRefreshCoordinator();
/** Share concurrent interactive all-catalog refreshes while keeping each caller's cancellation independent. */
export function refreshModelCatalogs(modelRuntime, signal) {
    return modelCatalogRefreshCoordinator.refresh(modelRuntime, signal);
}
//# sourceMappingURL=model-catalog-refresh.js.map