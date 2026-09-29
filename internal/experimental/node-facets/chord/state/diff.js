import { overlap, RESERVED_SEGMENTS } from "../delta/index.js";
const DEFAULT_OVERLAP_SCAN = 65_536;
const MAX_DELTA_OPERATIONS = 4_096;
const isContainer = (value) => value !== null && typeof value === "object";
const emitSet = (path, value, operations) => {
    if (path.length === 0)
        operations.push(["r", value]);
    else
        operations.push(["s", path, value]);
};
const equalJson = (left, right) => {
    if (left === right)
        return true;
    if (!isContainer(left) || !isContainer(right) || Array.isArray(left) !== Array.isArray(right))
        return false;
    if (Array.isArray(left)) {
        const other = right;
        if (left.length !== other.length)
            return false;
        for (let index = 0; index < left.length; index++) {
            if (!equalJson(left[index], other[index]))
                return false;
        }
        return true;
    }
    const other = right;
    const keys = Object.keys(left);
    if (keys.length !== Object.keys(other).length)
        return false;
    for (const key of keys) {
        if (!Object.hasOwn(other, key) || !equalJson(left[key], other[key]))
            return false;
    }
    return true;
};
const permutation = (before, after) => {
    if (before.length !== after.length)
        return undefined;
    const positions = new Map();
    for (let index = 0; index < before.length; index++) {
        const value = before[index];
        const entry = positions.get(value);
        if (entry === undefined)
            positions.set(value, { indices: [index], used: 0 });
        else
            entry.indices.push(index);
    }
    const result = new Array(after.length);
    for (let index = 0; index < after.length; index++) {
        const entry = positions.get(after[index]);
        if (entry === undefined || entry.used === entry.indices.length)
            return undefined;
        result[index] = entry.indices[entry.used++];
    }
    return result;
};
const emitString = (before, after, path, operations) => {
    if (before === after)
        return;
    if (after.length > before.length && after.slice(0, before.length) === before) {
        operations.push(["a", path, after.slice(before.length)]);
        return;
    }
    const shared = overlap(before, after, DEFAULT_OVERLAP_SCAN);
    if (shared === 0) {
        operations.push(["s", path, after]);
        return;
    }
    operations.push(["t", path, before.length - shared]);
    if (after.length > shared)
        operations.push(["a", path, after.slice(shared)]);
};
const emitSameLengthShift = (before, after, path, operations) => {
    const length = before.length;
    if (length < 2)
        return false;
    let candidates = 0;
    for (let removed = 1; removed < length && candidates < 16; removed++) {
        if (before[removed] !== after[0])
            continue;
        candidates += 1;
        let matches = true;
        for (let index = removed; index < length; index++) {
            if (before[index] !== after[index - removed]) {
                matches = false;
                break;
            }
        }
        if (!matches)
            continue;
        operations.push(["p", path, 0, removed, []]);
        operations.push(["p", path, length - removed, 0, after.slice(length - removed)]);
        return true;
    }
    candidates = 0;
    for (let inserted = 1; inserted < length && candidates < 16; inserted++) {
        if (after[inserted] !== before[0])
            continue;
        candidates += 1;
        let matches = true;
        for (let index = inserted; index < length; index++) {
            if (after[index] !== before[index - inserted]) {
                matches = false;
                break;
            }
        }
        if (!matches)
            continue;
        operations.push(["p", path, 0, 0, after.slice(0, inserted)]);
        operations.push(["p", path, length, inserted, []]);
        return true;
    }
    return false;
};
const diffArray = (before, after, path, operations) => {
    if (before.length === after.length) {
        let mismatches = 0;
        for (let index = 0; index < after.length; index++) {
            if (before[index] !== after[index])
                mismatches += 1;
        }
        if (mismatches === 0)
            return;
        if (mismatches > 1) {
            const order = permutation(before, after);
            if (order !== undefined) {
                operations.push(["m", path, order]);
                return;
            }
            if (emitSameLengthShift(before, after, path, operations))
                return;
        }
        for (let index = 0; index < after.length; index++) {
            if (operations.length > MAX_DELTA_OPERATIONS)
                return;
            diffValue(before[index], after[index], [...path, index], operations);
        }
        return;
    }
    let prefix = 0;
    const shortest = Math.min(before.length, after.length);
    while (prefix < shortest && equalJson(before[prefix], after[prefix]))
        prefix += 1;
    let suffix = 0;
    while (suffix < shortest - prefix &&
        equalJson(before[before.length - 1 - suffix], after[after.length - 1 - suffix])) {
        suffix += 1;
    }
    operations.push(["p", path, prefix, before.length - prefix - suffix, after.slice(prefix, after.length - suffix)]);
};
const diffObject = (before, after, path, operations) => {
    const beforeKeys = Object.keys(before);
    const afterKeys = Object.keys(after);
    if ([...beforeKeys, ...afterKeys].some((key) => RESERVED_SEGMENTS.has(key))) {
        if (!equalJson(before, after))
            emitSet(path, after, operations);
        return;
    }
    for (const key of afterKeys) {
        if (operations.length > MAX_DELTA_OPERATIONS)
            return;
        if (Object.hasOwn(before, key))
            diffValue(before[key], after[key], [...path, key], operations);
        else
            emitSet([...path, key], after[key], operations);
    }
    for (const key of beforeKeys) {
        if (operations.length > MAX_DELTA_OPERATIONS)
            return;
        if (!Object.hasOwn(after, key))
            operations.push(["d", [...path, key]]);
    }
};
const diffValue = (before, after, path, operations) => {
    if (before === after || operations.length > MAX_DELTA_OPERATIONS)
        return;
    if (typeof before === "string" && typeof after === "string" && path.length > 0) {
        emitString(before, after, path, operations);
        return;
    }
    if (Array.isArray(before) && Array.isArray(after)) {
        diffArray(before, after, path, operations);
        return;
    }
    if (isContainer(before) && isContainer(after) && !Array.isArray(before) && !Array.isArray(after)) {
        diffObject(before, after, path, operations);
        return;
    }
    emitSet(path, after, operations);
};
const jsonCost = (value) => {
    if (value === null)
        return 4;
    if (typeof value === "string")
        return value.length + 2;
    if (typeof value === "number")
        return String(value).length;
    if (typeof value === "boolean")
        return value ? 4 : 5;
    if (Array.isArray(value)) {
        let cost = 2;
        for (let index = 0; index < value.length; index++)
            cost += jsonCost(value[index]) + (index === 0 ? 0 : 1);
        return cost;
    }
    let cost = 2;
    let index = 0;
    for (const key of Object.keys(value)) {
        cost += key.length + 3 + jsonCost(value[key]) + (index++ === 0 ? 0 : 1);
    }
    return cost;
};
const pathCost = (path) => {
    let cost = 2;
    for (let index = 0; index < path.length; index++) {
        const segment = path[index];
        cost += (typeof segment === "string" ? segment.length + 2 : String(segment).length) + (index === 0 ? 0 : 1);
    }
    return cost;
};
const operationCost = (operation) => {
    switch (operation[0]) {
        case "r":
            return 6 + jsonCost(operation[1]);
        case "s":
            return 7 + pathCost(operation[1]) + jsonCost(operation[2]);
        case "d":
            return 6 + pathCost(operation[1]);
        case "a":
            return 7 + pathCost(operation[1]) + operation[2].length + 2;
        case "t":
            return 7 + pathCost(operation[1]) + String(operation[2]).length;
        case "p":
            return (10 +
                pathCost(operation[1]) +
                String(operation[2]).length +
                String(operation[3]).length +
                jsonCost(operation[4]));
        case "m":
            return 7 + pathCost(operation[1]) + jsonCost(operation[2]);
    }
};
/** Compute a compact operation batch from two immutable JSON revisions. */
export function diffRevisions(before, after) {
    const operations = [];
    diffValue(before, after, [], operations);
    if (operations.length > MAX_DELTA_OPERATIONS)
        return [["r", after]];
    if (operations.length === 0 || operations[0]?.[0] === "r")
        return operations;
    let deltaCost = 2;
    for (const operation of operations)
        deltaCost += operationCost(operation) + 1;
    if (deltaCost < 65_536)
        return operations;
    const snapshotCost = jsonCost(after) + 6;
    return deltaCost >= snapshotCost ? [["r", after]] : operations;
}
//# sourceMappingURL=diff.js.map