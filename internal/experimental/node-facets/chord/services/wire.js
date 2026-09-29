import { assertValidOp, assertValidWireOp } from "../delta/index.js";
const SERVICE_CONTROL_ID = "$chord.service";
const SERVICE_CATALOGUE_MEMBER = "catalogue";
const SERVICE_SUBSCRIBE_MEMBER = "subscribe";
const SERVICE_UNSUBSCRIBE_MEMBER = "unsubscribe";
export function createServiceCatalogueCall() {
    return { serviceId: SERVICE_CONTROL_ID, member: SERVICE_CATALOGUE_MEMBER, args: [] };
}
export function createServiceSubscribeCall(subscriptionId, serviceId, mode) {
    return { serviceId: SERVICE_CONTROL_ID, member: SERVICE_SUBSCRIBE_MEMBER, args: [subscriptionId, serviceId, mode] };
}
export function createServiceUnsubscribeCall(subscriptionId) {
    return { serviceId: SERVICE_CONTROL_ID, member: SERVICE_UNSUBSCRIBE_MEMBER, args: [subscriptionId] };
}
export function decodeServiceControlCall(call) {
    if (call.serviceId !== SERVICE_CONTROL_ID || call.instance !== undefined)
        return undefined;
    if (call.member === SERVICE_CATALOGUE_MEMBER && call.args.length === 0)
        return { type: "catalogue" };
    if (call.member === SERVICE_SUBSCRIBE_MEMBER &&
        call.args.length === 3 &&
        isId(call.args[0]) &&
        isId(call.args[1]) &&
        (call.args[2] === "singleton" || call.args[2] === "keyed")) {
        return {
            type: "subscribe",
            subscriptionId: call.args[0],
            serviceId: call.args[1],
            mode: call.args[2],
        };
    }
    if (call.member === SERVICE_UNSUBSCRIBE_MEMBER && call.args.length === 1 && isId(call.args[0])) {
        return { type: "unsubscribe", subscriptionId: call.args[0] };
    }
    return undefined;
}
export function parseServiceCall(value) {
    const call = record(value, "service call");
    assertKeys(call, ["serviceId", "member", "args"], ["instance"], "service call");
    if (!isId(call.serviceId) || !isId(call.member) || !Array.isArray(call.args)) {
        throw new TypeError("Invalid service call");
    }
    if (call.instance !== undefined)
        assertAddress(call.instance);
    return value;
}
export function parseServiceCatalogue(value) {
    if (!Array.isArray(value))
        throw new TypeError("Invalid service catalogue");
    const ids = new Set();
    for (const candidate of value) {
        const entry = record(candidate, "service catalogue entry");
        assertKeys(entry, ["serviceId", "mode"], [], "service catalogue entry");
        if (!isId(entry.serviceId) || !isMode(entry.mode) || ids.has(entry.serviceId)) {
            throw new TypeError("Invalid service catalogue");
        }
        ids.add(entry.serviceId);
    }
    return value;
}
export function parseServiceSubscriptionSnapshot(value) {
    assertSubscriptionSnapshot(value, assertValidOp);
    return value;
}
export function parseWireServiceSubscriptionSnapshot(value) {
    assertSubscriptionSnapshot(value, assertValidWireOp);
    return value;
}
export function parseServiceProviderUpdate(value) {
    assertProviderUpdate(value, assertValidOp);
    return value;
}
export function parseWireServiceProviderUpdate(value) {
    assertProviderUpdate(value, assertValidWireOp);
    return value;
}
function assertSubscriptionSnapshot(value, assertOp) {
    const snapshot = record(value, "service subscription snapshot");
    assertKeys(snapshot, ["serviceId", "mode", "instances"], [], "service subscription snapshot");
    if (!isId(snapshot.serviceId) || !isMode(snapshot.mode) || !Array.isArray(snapshot.instances)) {
        throw new TypeError("Invalid service subscription snapshot");
    }
    for (const instance of snapshot.instances)
        assertInstance(instance, assertOp);
}
function assertProviderUpdate(value, assertOp) {
    const update = record(value, "service provider update");
    switch (update.type) {
        case "state":
            assertKeys(update, ["type", "member", "sequence", "ops"], ["instance"], "state update");
            if (!isId(update.member) || !isInteger(update.sequence, 1) || !Array.isArray(update.ops)) {
                throw new TypeError("Invalid service state update");
            }
            if (update.instance !== undefined)
                assertAddress(update.instance);
            for (const op of update.ops)
                assertOp(op);
            return;
        case "unavailable":
            assertKeys(update, ["type"], [], "unavailable update");
            return;
        case "replaced":
            assertKeys(update, ["type", "snapshot"], [], "replacement update");
            assertInstance(update.snapshot, assertOp);
            return;
        case "spawned":
            assertKeys(update, ["type", "instance"], [], "spawn update");
            assertInstance(update.instance, assertOp);
            return;
        case "closed":
            assertKeys(update, ["type", "instance"], [], "close update");
            assertAddress(update.instance);
            return;
        default:
            throw new TypeError("Invalid service provider update");
    }
}
function assertInstance(value, assertOp) {
    const instance = record(value, "service instance snapshot");
    assertKeys(instance, ["members"], ["instance"], "service instance snapshot");
    if (instance.instance !== undefined)
        assertAddress(instance.instance);
    if (!Array.isArray(instance.members))
        throw new TypeError("Invalid service instance snapshot");
    for (const candidate of instance.members) {
        const member = record(candidate, "service member snapshot");
        if (member.kind === "method") {
            assertKeys(member, ["name", "kind"], [], "service method snapshot");
            if (!isId(member.name))
                throw new TypeError("Invalid service method snapshot");
            continue;
        }
        if (member.kind === "state") {
            assertKeys(member, ["name", "kind", "sequence", "ops"], [], "service state snapshot");
            if (!isId(member.name) || !isInteger(member.sequence, 0) || !Array.isArray(member.ops)) {
                throw new TypeError("Invalid service state snapshot");
            }
            for (const op of member.ops)
                assertOp(op);
            continue;
        }
        throw new TypeError("Invalid service member snapshot");
    }
}
function assertAddress(value) {
    const address = record(value, "service instance address");
    assertKeys(address, ["key", "generation"], [], "service instance address");
    if (!isId(address.key) || !isInteger(address.generation, 1)) {
        throw new TypeError("Invalid service instance address");
    }
}
function record(value, description) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
        throw new TypeError(`Invalid ${description}`);
    }
    return value;
}
function assertKeys(value, required, optional, description) {
    const allowed = new Set([...required, ...optional]);
    if (required.some((key) => !Object.hasOwn(value, key)) || Object.keys(value).some((key) => !allowed.has(key))) {
        throw new TypeError(`Invalid ${description}`);
    }
}
function isId(value) {
    return typeof value === "string" && value.length > 0;
}
function isMode(value) {
    return value === "singleton" || value === "keyed";
}
function isInteger(value, minimum) {
    return typeof value === "number" && Number.isInteger(value) && value >= minimum;
}
//# sourceMappingURL=wire.js.map