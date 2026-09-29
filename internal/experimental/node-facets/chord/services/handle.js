/** Host-owned mutable target with consumer-owned guarded views. */
export class ServiceSlot {
    #serviceId;
    #wrapObjects;
    #implementation;
    constructor(serviceId, wrapObjects) {
        this.#serviceId = serviceId;
        this.#wrapObjects = wrapObjects;
    }
    view(assertAccess) {
        return new ServiceView(this, assertAccess, this.#wrapObjects).proxy;
    }
    bind(implementation) {
        this.#implementation = implementation;
    }
    unbind() {
        this.#implementation = undefined;
    }
    resolve(property, assertAccess) {
        assertAccess();
        const implementation = this.#implementation;
        if (implementation === undefined)
            throw new Error(`Service ${this.#serviceId} is disconnected`);
        return {
            value: Reflect.get(implementation, property, implementation),
            receiver: implementation,
        };
    }
}
class ServiceView {
    #slot;
    #assertAccess;
    #wrapObjects;
    #members = new Map();
    proxy;
    constructor(slot, assertAccess, wrapObjects) {
        this.#slot = slot;
        this.#assertAccess = assertAccess;
        this.#wrapObjects = wrapObjects;
        this.proxy = new Proxy(Object.create(null), {
            get: (_target, property) => this.#getMember(property),
        });
    }
    #getMember(property) {
        const resolve = () => this.#slot.resolve(property, this.#assertAccess);
        const current = resolve().value;
        if (!isObject(current) || (typeof current !== "function" && !this.#wrapObjects))
            return current;
        let member = this.#members.get(property);
        if (member === undefined) {
            member = new ValueView(resolve, typeof current === "function");
            this.#members.set(property, member);
        }
        return member.proxy;
    }
}
class ValueView {
    #resolve;
    #children = new Map();
    proxy;
    constructor(resolve, callable) {
        this.#resolve = resolve;
        const target = callable ? () => undefined : Object.create(null);
        this.proxy = new Proxy(target, {
            apply: (_target, _thisArg, args) => this.#invoke(args),
            get: (_target, property) => this.#get(property),
        });
    }
    #invoke(args) {
        const { value, receiver } = this.#resolve();
        if (typeof value !== "function")
            throw new TypeError("Service member is not callable");
        return Reflect.apply(value, receiver, args);
    }
    #get(property) {
        const resolve = () => {
            const parent = this.#resolve();
            if (!isObject(parent.value))
                throw new TypeError("Service member does not have properties");
            return {
                value: Reflect.get(parent.value, property, parent.value),
                receiver: parent.value,
            };
        };
        const current = resolve().value;
        if (typeof current !== "function")
            return current;
        let child = this.#children.get(property);
        if (child === undefined) {
            child = new ValueView(resolve, true);
            this.#children.set(property, child);
        }
        return child.proxy;
    }
}
function isObject(value) {
    return (typeof value === "object" && value !== null) || typeof value === "function";
}
//# sourceMappingURL=handle.js.map