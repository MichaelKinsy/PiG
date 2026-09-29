const isContainer = (value) => typeof value === "object" && value !== null;
/** Owns and freezes immutable JSON revisions while reusing already-owned subtrees. */
export class JsonRevisionStore {
    #owned = new WeakSet();
    import(value) {
        return this.#clone(value, new Set());
    }
    commit(value, transactionOwned) {
        return this.#finish(value, transactionOwned, new Set(), new WeakSet());
    }
    adopt(value) {
        return this.#adopt(value, new Set(), new WeakSet());
    }
    #clone(value, ancestors) {
        if (!isContainer(value))
            return assertPrimitive(value);
        if (ancestors.has(value))
            throw new TypeError("Replicated state cannot contain cycles");
        ancestors.add(value);
        try {
            if (Array.isArray(value)) {
                assertDenseArray(value);
                const result = new Array(value.length);
                for (let index = 0; index < value.length; index++) {
                    Object.defineProperty(result, index, {
                        value: this.#clone(value[index], ancestors),
                        writable: true,
                        enumerable: true,
                        configurable: true,
                    });
                }
                this.#owned.add(result);
                return Object.freeze(result);
            }
            assertPlainObject(value);
            const result = Object.create(Object.getPrototypeOf(value));
            for (const key of Reflect.ownKeys(value)) {
                if (typeof key === "symbol")
                    throw new TypeError("Replicated state cannot contain symbol properties");
                const descriptor = Object.getOwnPropertyDescriptor(value, key);
                if (descriptor === undefined || !descriptor.enumerable || !("value" in descriptor)) {
                    throw new TypeError("Replicated state objects must contain enumerable data properties");
                }
                Object.defineProperty(result, key, {
                    value: this.#clone(descriptor.value, ancestors),
                    writable: true,
                    enumerable: true,
                    configurable: true,
                });
            }
            this.#owned.add(result);
            return Object.freeze(result);
        }
        finally {
            ancestors.delete(value);
        }
    }
    #adopt(value, ancestors, finished) {
        if (!isContainer(value) || this.#owned.has(value))
            return isContainer(value) ? value : assertPrimitive(value);
        if (finished.has(value))
            return value;
        if (ancestors.has(value))
            throw new TypeError("Replicated state cannot contain cycles");
        ancestors.add(value);
        try {
            if (Array.isArray(value)) {
                assertDenseArray(value);
                for (let index = 0; index < value.length; index++) {
                    const child = this.#adopt(value[index], ancestors, finished);
                    if (child !== value[index])
                        defineValue(value, String(index), child);
                }
            }
            else {
                assertPlainObject(value);
                for (const key of Reflect.ownKeys(value)) {
                    if (typeof key === "symbol")
                        throw new TypeError("Replicated state cannot contain symbol properties");
                    const descriptor = Object.getOwnPropertyDescriptor(value, key);
                    if (descriptor === undefined || !descriptor.enumerable || !("value" in descriptor)) {
                        throw new TypeError("Replicated state objects must contain enumerable data properties");
                    }
                    const child = this.#adopt(descriptor.value, ancestors, finished);
                    if (child !== descriptor.value && isContainer(child))
                        defineValue(value, key, child);
                }
            }
            finished.add(value);
            this.#owned.add(value);
            return Object.freeze(value);
        }
        finally {
            ancestors.delete(value);
        }
    }
    #finish(value, transactionOwned, ancestors, placements) {
        if (ancestors.has(value))
            throw new TypeError("Replicated state cannot contain cycles");
        if (placements.has(value))
            return this.#clone(value, new Set());
        placements.add(value);
        if (this.#owned.has(value))
            return value;
        if (!transactionOwned.has(value))
            return this.#clone(value, ancestors);
        ancestors.add(value);
        try {
            if (Array.isArray(value)) {
                assertDenseArray(value);
                for (let index = 0; index < value.length; index++) {
                    const child = value[index];
                    if (!isContainer(child)) {
                        assertPrimitive(child);
                        continue;
                    }
                    const next = this.#finish(child, transactionOwned, ancestors, placements);
                    if (next !== child)
                        defineValue(value, String(index), next);
                }
            }
            else {
                assertPlainObject(value);
                for (const key of Reflect.ownKeys(value)) {
                    if (typeof key === "symbol")
                        throw new TypeError("Replicated state cannot contain symbol properties");
                    const descriptor = Object.getOwnPropertyDescriptor(value, key);
                    if (descriptor === undefined || !descriptor.enumerable || !("value" in descriptor)) {
                        throw new TypeError("Replicated state objects must contain enumerable data properties");
                    }
                    if (!isContainer(descriptor.value)) {
                        assertPrimitive(descriptor.value);
                        continue;
                    }
                    const next = this.#finish(descriptor.value, transactionOwned, ancestors, placements);
                    if (next !== descriptor.value)
                        defineValue(value, key, next);
                }
            }
            this.#owned.add(value);
            return Object.freeze(value);
        }
        finally {
            ancestors.delete(value);
        }
    }
}
function defineValue(target, key, value) {
    Object.defineProperty(target, key, { value, writable: true, enumerable: true, configurable: true });
}
function assertPrimitive(value) {
    if (value === null || typeof value === "string" || typeof value === "boolean")
        return value;
    if (typeof value === "number" && Number.isFinite(value))
        return value;
    throw new TypeError("Replicated state values must be strict JSON");
}
function assertPlainObject(value) {
    const prototype = Object.getPrototypeOf(value);
    if (prototype !== Object.prototype && prototype !== null) {
        throw new TypeError("Replicated state containers must be plain objects or arrays");
    }
}
function assertDenseArray(value) {
    const keys = Reflect.ownKeys(value);
    if (keys.length !== value.length + 1 || keys.some((key) => typeof key !== "string")) {
        throw new TypeError("Replicated state arrays must be dense and contain only indexed entries");
    }
    for (let index = 0; index < value.length; index++) {
        if (!Object.hasOwn(value, index) || value[index] === undefined) {
            throw new TypeError("Replicated state arrays cannot contain holes or undefined entries");
        }
    }
}
//# sourceMappingURL=value.js.map