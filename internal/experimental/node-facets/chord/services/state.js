import { BACKGROUND_CONTEXT } from "../context/index.js";
import { applyImmutable, isBase } from "../delta/index.js";
import { diffRevisions } from "../state/diff.js";
import { produceWithMetadata } from "../state/draft.js";
import { JsonRevisionStore } from "../state/value.js";
import { registerReplicatedStateInternals } from "./state-internals.js";
export class MutableReplicatedStateImpl {
    #listeners = new Map();
    #sourceListeners = new Set();
    #store = new JsonRevisionStore();
    #publications = [];
    #value;
    #sequence = 0;
    #changing = false;
    #delivering = false;
    constructor(initial) {
        this.#value = this.#store.import(initial);
        const thisSource = this;
        registerReplicatedStateInternals(this, {
            get sequence() {
                return thisSource.#sequence;
            },
            get value() {
                return thisSource.#value;
            },
            subscribe: (listener) => {
                thisSource.#sourceListeners.add(listener);
                return () => thisSource.#sourceListeners.delete(listener);
            },
        });
    }
    get value() {
        return this.#value;
    }
    change(context, mutate) {
        if (this.#changing)
            throw new Error("Replicated state cannot be changed reentrantly from a change callback");
        this.#changing = true;
        let next;
        try {
            const produced = produceWithMetadata(this.#value, mutate);
            if (produced.value === this.#value)
                return;
            next = this.#store.commit(produced.value, produced.owned);
        }
        finally {
            this.#changing = false;
        }
        this.#commit(next, context);
    }
    replace(context, value) {
        if (this.#changing)
            throw new Error("Replicated state cannot be replaced from a change callback");
        this.#commit(this.#store.import(value), context);
    }
    subscribe(listener) {
        const sequence = this.#sequence;
        const value = this.#value;
        this.#listeners.set(listener, sequence);
        try {
            listener(value, serviceDeliveryContext(), { kind: "hydrate", sequence });
        }
        catch (error) {
            this.#listeners.delete(listener);
            throw error;
        }
        return () => this.#listeners.delete(listener);
    }
    #commit(next, context) {
        const ops = diffRevisions(this.#value, next);
        if (ops.length === 0)
            return;
        this.#value = next;
        this.#sequence += 1;
        this.#publications.push({ value: next, ops, sequence: this.#sequence, context });
        if (this.#delivering)
            return;
        this.#delivering = true;
        const errors = [];
        try {
            for (let publication = this.#publications.shift(); publication !== undefined; publication = this.#publications.shift()) {
                for (const listener of [...this.#sourceListeners]) {
                    try {
                        listener(publication.ops, publication.sequence, publication.context);
                    }
                    catch (error) {
                        errors.push(error);
                    }
                }
                const delivery = { kind: "update", sequence: publication.sequence };
                for (const [listener, hydratedSequence] of [...this.#listeners]) {
                    if (publication.sequence <= hydratedSequence)
                        continue;
                    try {
                        listener(publication.value, publication.context, delivery);
                    }
                    catch (error) {
                        errors.push(error);
                    }
                }
            }
        }
        finally {
            this.#delivering = false;
        }
        if (errors.length === 1)
            throw errors[0];
        if (errors.length > 1)
            throw new AggregateError(errors, "Replicated state listeners failed");
    }
}
/** A cold read-only state used by service consumers until a complete snapshot arrives. */
export class ReplicatedStateReplica {
    #listeners = new Set();
    #reportError;
    #store = new JsonRevisionStore();
    #value;
    #sequence;
    constructor(reportError) {
        this.#reportError = reportError;
    }
    get value() {
        return this.#value;
    }
    subscribe(listener) {
        this.#listeners.add(listener);
        if (this.#value !== undefined) {
            this.#deliver(listener, this.#value, serviceDeliveryContext(), {
                kind: "hydrate",
                sequence: this.#sequence,
            });
        }
        return () => this.#listeners.delete(listener);
    }
    hydrate(sequence, ops, context) {
        let next;
        try {
            if (!isBase(ops))
                throw new Error("Replicated state snapshot is not a base operation batch");
            next = this.#store.adopt(applyImmutable(undefined, ops));
        }
        catch (error) {
            this.clear();
            throw error;
        }
        this.#sequence = sequence;
        this.#value = next;
        this.#deliverAll(context, { kind: "hydrate", sequence });
    }
    update(sequence, ops, context) {
        if (this.#sequence === undefined || this.#value === undefined) {
            throw new Error("Replicated state received an update before hydration");
        }
        if (sequence !== this.#sequence + 1) {
            this.clear();
            throw new Error("Replicated state update sequence has a gap");
        }
        let next;
        try {
            next = this.#store.adopt(applyImmutable(this.#value, ops));
        }
        catch (error) {
            this.clear();
            throw error;
        }
        this.#sequence = sequence;
        this.#value = next;
        this.#deliverAll(context, { kind: "update", sequence });
    }
    clear() {
        this.#value = undefined;
        this.#sequence = undefined;
    }
    #deliverAll(context, delivery) {
        if (this.#value === undefined)
            return;
        for (const listener of this.#listeners)
            this.#deliver(listener, this.#value, context, delivery);
    }
    #deliver(listener, value, context, delivery) {
        try {
            listener(value, context, delivery);
        }
        catch (error) {
            this.#reportError(toError(error));
        }
    }
}
/** @internal Context for synthetic service deliveries without a caller. */
export function serviceDeliveryContext() {
    // TODO: Add delivery-scoped cancellation or metadata if deliveries gain an owned lifecycle.
    return BACKGROUND_CONTEXT;
}
function toError(error) {
    return error instanceof Error ? error : new Error(String(error));
}
//# sourceMappingURL=state.js.map