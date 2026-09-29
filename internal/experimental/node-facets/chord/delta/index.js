// ─── Classification ──────────────────────────────────────────────────────────
export const isReplace = (op) => op[0] === "r";
/**
 * A batch begins with a replacement. Flush guarantees `r` is at index 0 or absent,
 * so this is exact rather than a heuristic.
 */
export const isBase = (ops) => ops.length > 0 && ops[0][0] === "r";
// ─── Overlap ─────────────────────────────────────────────────────────────────
/**
 * Longest suffix of `a` that is a prefix of `b`. Probes with indexOf and verifies
 * exact substring equality, so the hot loops are native. A hand-written KMP is
 * asymptotically equivalent and much slower in practice.
 *
 * Always correct: the returned n satisfies a.slice(a.length - n) === b.slice(0, n).
 */
export function overlap(a, b, scan, probe = 64, maxCandidates = 8) {
    if (a.length === 0 || b.length === 0 || scan === 0)
        return 0;
    const tail = a.length > scan ? a.slice(a.length - scan) : a;
    // A probe of length h can only find overlaps of at least h — the head must
    // actually occur in `a`. So try a long head first (few candidates, and it
    // catches the large overlaps a rolling window produces), then fall back to one
    // character, which finds any overlap at the cost of more candidates.
    //
    // Candidates are bounded because repetitive output — a build log, or any run of
    // one character — makes a long head match at thousands of positions. Giving up
    // returns 0, which emits a set: larger, never wrong.
    for (const h of [Math.min(probe, b.length), 1]) {
        const head = b.slice(0, h);
        let tried = 0;
        for (let k = tail.indexOf(head); k !== -1; k = tail.indexOf(head, k + 1)) {
            if (++tried > maxCandidates)
                break;
            const n = tail.length - k;
            if (n <= b.length && tail.slice(k) === b.slice(0, n))
                return n;
        }
        if (h === 1)
            break;
    }
    return 0;
}
const isObj = (value) => value !== null && typeof value === "object";
const cloneJson = (value) => {
    if (!isObj(value))
        return value;
    if (Array.isArray(value))
        return value.map((item) => cloneJson(item));
    // Spread preserves compact object layouts instead of reserving extra slots
    // while growing an empty object. Both branches create own writable properties.
    const result = (Object.getPrototypeOf(value) === null ? Object.assign(Object.create(null), value) : { ...value });
    for (const key of Object.keys(result)) {
        const child = result[key];
        if (isObj(child))
            result[key] = cloneJson(child);
    }
    return result;
};
const INDEX = /^(?:0|[1-9]\d*)$/;
const norm = (target, key) => typeof key === "symbol" ? key : Array.isArray(target) && INDEX.test(key) ? Number(key) : key;
const MUTATORS = new Set(["push", "pop", "shift", "unshift", "splice", "sort", "reverse", "fill", "copyWithin"]);
const MISSING = Symbol("missing");
const spliceItems = (target, index, remove, items) => {
    const removed = Reflect.apply(Array.prototype.splice, target, [index, remove]);
    const chunkSize = 10_000;
    for (let offset = 0; offset < items.length; offset += chunkSize) {
        Reflect.apply(Array.prototype.splice, target, [index + offset, 0, ...items.slice(offset, offset + chunkSize)]);
    }
    return removed;
};
const jsonEqual = (left, right) => {
    if (left === right)
        return true;
    if (!isObj(left) || !isObj(right) || Array.isArray(left) !== Array.isArray(right))
        return false;
    if (Array.isArray(left) && Array.isArray(right)) {
        if (left.length !== right.length)
            return false;
        for (let index = 0; index < left.length; index++) {
            if (!jsonEqual(left[index], right[index]))
                return false;
        }
        return true;
    }
    const leftObject = left;
    const rightObject = right;
    const leftKeys = Object.keys(leftObject);
    const rightKeys = Object.keys(rightObject);
    if (leftKeys.length !== rightKeys.length)
        return false;
    for (const key of leftKeys) {
        if (!Object.hasOwn(rightObject, key) || !jsonEqual(leftObject[key], rightObject[key]))
            return false;
    }
    return true;
};
const emitSet = (path, value, out) => {
    const snapshot = cloneJson(value);
    if (path.length === 0)
        out.push(["r", snapshot]);
    else
        out.push(["s", [...path], snapshot]);
};
const emitDelete = (path, out) => {
    if (path.length === 0)
        throw new TypeError("the tracked root cannot be deleted");
    out.push(["d", [...path]]);
};
const diffString = (before, after, path, scan, out) => {
    if (before === after)
        return;
    if (path.length === 0) {
        emitSet(path, after, out);
        return;
    }
    const at = [...path];
    // NOT `after.startsWith(before)`. `after` is usually a cons string — the
    // producer just did `s += chunk` — and V8's startsWith walks a cons char by
    // char. `slice(...) === before` flattens once and compares with memcmp.
    // Measured on a 200 KB string growing by 8 bytes per flush: 845 us -> 42 us.
    if (after.length > before.length && after.slice(0, before.length) === before) {
        out.push(["a", at, after.slice(before.length)]);
        return;
    }
    const shared = overlap(before, after, scan);
    if (shared === 0) {
        out.push(["s", at, after]);
        return;
    }
    out.push(["t", at, before.length - shared]);
    if (after.length > shared)
        out.push(["a", at, after.slice(shared)]);
};
const diffValue = (before, after, path, scan, out) => {
    if (before === MISSING) {
        if (after !== MISSING)
            emitSet(path, after, out);
        return;
    }
    if (after === MISSING) {
        emitDelete(path, out);
        return;
    }
    if (before === after)
        return;
    if (typeof before === "string" && typeof after === "string") {
        diffString(before, after, path, scan, out);
        return;
    }
    if (Array.isArray(before) && Array.isArray(after)) {
        diffArray(before, after, path, scan, out);
        return;
    }
    if (isObj(before) && isObj(after) && !Array.isArray(before) && !Array.isArray(after)) {
        diffObject(before, after, path, scan, out);
        return;
    }
    emitSet(path, after, out);
};
function diffObject(before, after, path, scan, out) {
    if ([...Object.keys(before), ...Object.keys(after)].some((key) => RESERVED_SEGMENTS.has(key))) {
        emitSet(path, after, out);
        return;
    }
    for (const key of Object.keys(after)) {
        diffValue(Object.hasOwn(before, key) ? before[key] : MISSING, after[key], [...path, key], scan, out);
    }
    for (const key of Object.keys(before)) {
        if (!Object.hasOwn(after, key))
            emitDelete([...path, key], out);
    }
}
function diffArray(before, after, path, scan, out) {
    if (before.length === after.length) {
        for (let index = 0; index < after.length; index++) {
            diffValue(before[index], after[index], [...path, index], scan, out);
        }
        return;
    }
    let prefix = 0;
    while (prefix < before.length && prefix < after.length && jsonEqual(before[prefix], after[prefix]))
        prefix++;
    let suffix = 0;
    while (suffix < before.length - prefix &&
        suffix < after.length - prefix &&
        jsonEqual(before[before.length - 1 - suffix], after[after.length - 1 - suffix])) {
        suffix++;
    }
    const shorter = Math.min(before.length, after.length);
    if (prefix + suffix === shorter) {
        const remove = before.length - prefix - suffix;
        const items = after.slice(prefix, after.length - suffix);
        if (prefix === 0 && remove === before.length)
            emitSet(path, after, out);
        else
            out.push(["p", [...path], prefix, remove, cloneJson(items)]);
        return;
    }
    // Structural movement combined with retained-index edits has no unique
    // alignment. Preserve the retained index deltas and express only the tail
    // length change structurally. It may be broader than the producer's intent,
    // but never degrades those edits to a whole-array replacement.
    for (let index = 0; index < shorter; index++) {
        diffValue(before[index], after[index], [...path, index], scan, out);
    }
    if (after.length > before.length) {
        out.push(["p", [...path], before.length, 0, cloneJson(after.slice(before.length))]);
    }
    else if (before.length > after.length) {
        if (after.length === 0)
            emitSet(path, after, out);
        else
            out.push(["p", [...path], after.length, before.length - after.length, []]);
    }
}
export function track(root, options = {}) {
    const scan = options.maxOverlapScan ?? 65_536;
    let log = [];
    let trie = {};
    let nextOrder = 0;
    let tombstones = 0;
    let liveSlots = 0;
    let nodeCount = 1;
    let lastAddedSlot;
    let hasPending = false;
    let forceBase = true;
    const clearPending = () => {
        log = [];
        trie = {};
        nextOrder = 0;
        tombstones = 0;
        liveSlots = 0;
        nodeCount = 1;
        lastAddedSlot = undefined;
        hasPending = false;
    };
    const logNode = (path) => {
        let at = trie;
        for (const segment of path) {
            if (!at.kids)
                at.kids = new Map();
            let next = at.kids.get(segment);
            if (next === undefined) {
                next = {};
                at.kids.set(segment, next);
                nodeCount++;
            }
            at = next;
        }
        return at;
    };
    const findLogNode = (path) => {
        let at = trie;
        for (const segment of path) {
            const next = at.kids?.get(segment);
            if (next === undefined)
                return undefined;
            at = next;
        }
        return at;
    };
    const compactLog = () => {
        if (tombstones < 1_024 || tombstones * 2 < log.length)
            return;
        const compacted = [];
        for (const slot of log) {
            if (slot === undefined)
                continue;
            slot.index = compacted.length;
            compacted.push(slot);
        }
        log = compacted;
        tombstones = 0;
    };
    const killSlot = (slot) => {
        if (slot.dead)
            return;
        slot.dead = true;
        liveSlots--;
        if (log[slot.index] === slot) {
            log[slot.index] = undefined;
            tombstones++;
        }
    };
    const liveSlot = (at) => {
        const slots = at.slots;
        if (slots === undefined)
            return undefined;
        while (slots.length > 0 && slots[slots.length - 1].dead)
            slots.pop();
        if (slots.length === 0) {
            at.slots = undefined;
            return undefined;
        }
        return slots[slots.length - 1];
    };
    const killHere = (at) => {
        if (at.slots === undefined)
            return;
        for (const slot of at.slots)
            killSlot(slot);
        at.slots = undefined;
    };
    const addSlot = (at, slot) => {
        compactLog();
        slot.order = nextOrder++;
        slot.index = log.length;
        at.lastOrder = slot.order;
        if (at.slots === undefined)
            at.slots = [];
        at.slots.push(slot);
        log.push(slot);
        liveSlots++;
        lastAddedSlot = slot;
    };
    const collapsePending = () => {
        // Long mutation windows can otherwise retain operations and retired path
        // generations indefinitely. Fall back to a complete snapshot once either
        // history exceeds a bounded coalescing window. Payload bytes and transient
        // allocation remain workload-dependent.
        if (forceBase || (liveSlots <= 4_096 && nodeCount <= 4_096))
            return;
        const value = cloneJson(root);
        clearPending();
        hasPending = true;
        addSlot(trie, { op: ["r", value], dead: false, order: 0, index: 0 });
    };
    const killSubtree = (at) => {
        killHere(at);
        if (at.kids !== undefined) {
            for (const child of at.kids.values())
                killSubtree(child);
            at.kids = undefined;
        }
        if (at.retiredKids !== undefined) {
            for (const generation of at.retiredKids) {
                for (const child of generation.values())
                    killSubtree(child);
            }
            at.retiredKids = undefined;
        }
    };
    const retireKids = (at) => {
        if (at.kids === undefined)
            return;
        if (at.retiredKids === undefined)
            at.retiredKids = [];
        at.retiredKids.push(at.kids);
        at.kids = undefined;
    };
    // Deepest live ancestor op carrying a payload we can fold a later write into.
    // `s`/`r` carry the whole subtree; `p` carries the items it inserted, so a
    // write to one of those indices belongs inside the payload rather than after it.
    const foldTarget = (path) => {
        let at = trie;
        let found;
        let ancestorMax = -1;
        for (let depth = 0; depth < path.length; depth++) {
            if (found !== undefined && at.lastOrder !== undefined && at.lastOrder > found.slot.order) {
                found = undefined;
            }
            const slot = liveSlot(at);
            // A fold is sound only if nothing has been recorded at this path or above it
            // since: a later op there (a splice on the same array, a replacement of an
            // ancestor) would have to apply after this write, not before it. Writes to
            // other branches are irrelevant, which is why this is not "the most recent op".
            if (slot !== undefined && slot.order >= ancestorMax && at.lastOrder === slot.order) {
                if (slot.op[0] === "s" || slot.op[0] === "r")
                    found = { slot, depth };
                else if (slot.op[0] === "p") {
                    const index = path[depth];
                    const items = slot.op[4];
                    if (typeof index === "number" && index >= slot.op[2] && index < slot.op[2] + items.length) {
                        found = { slot, depth: depth + 1, item: index - slot.op[2] };
                    }
                }
            }
            if (at.lastOrder !== undefined && at.lastOrder > ancestorMax)
                ancestorMax = at.lastOrder;
            const next = at.kids?.get(path[depth]);
            if (next === undefined)
                break;
            at = next;
        }
        if (found === undefined)
            return undefined;
        return { slot: found.slot, rest: path.slice(found.depth), item: found.item };
    };
    const foldInto = (container, rest, op) => {
        if (rest.length === 0)
            return false;
        let target = container;
        for (let index = 0; index < rest.length - 1; index++) {
            if (!isObj(target))
                return false;
            target = target[rest[index]];
        }
        if (!isObj(target))
            return false;
        const key = rest[rest.length - 1];
        const holder = target;
        const write = (value) => {
            Object.defineProperty(holder, key, { value, writable: true, enumerable: true, configurable: true });
        };
        switch (op[0]) {
            case "s":
                if (key === "__proto__")
                    return false;
                write(cloneJson(op[2]));
                return true;
            case "d":
                delete holder[key];
                return true;
            case "a": {
                const value = holder[key];
                if (typeof value !== "string")
                    return false;
                write(value + op[2]);
                return true;
            }
            case "t": {
                const value = holder[key];
                if (typeof value !== "string")
                    return false;
                write(value.slice(op[2]));
                return true;
            }
            case "p": {
                const value = holder[key];
                if (!Array.isArray(value))
                    return false;
                spliceItems(value, op[2], op[3], cloneJson(op[4]));
                return true;
            }
            default:
                return false;
        }
    };
    const recordString = (path, previous, value) => {
        if (forceBase)
            return;
        hasPending = true;
        // a string inside a pending payload belongs in that payload, as for any write
        const fold = foldTarget(path);
        if (fold !== undefined) {
            const op = ["s", path, value];
            if (fold.item !== undefined) {
                const items = fold.slot.op[4];
                if (fold.rest.length === 0) {
                    items[fold.item] = value;
                    return;
                }
                if (foldInto(items[fold.item], fold.rest, op))
                    return;
            }
            else {
                const payload = fold.slot.op[0] === "r" ? fold.slot.op[1] : fold.slot.op[2];
                if (foldInto(payload, fold.rest, op))
                    return;
            }
        }
        const at = logNode(path);
        const live = liveSlot(at);
        if (live?.str !== undefined) {
            live.str.value = value;
            return;
        }
        if (live !== undefined) {
            // a pending set or delete at this path already replaced the value; keep that
            // op and carry the new value in it rather than anchoring to it
            if (live.op[0] === "s" || live.op[0] === "r") {
                live.op = live.op[0] === "r" ? ["r", value] : ["s", live.op[1], value];
                return;
            }
            if (live.op[0] === "d") {
                killHere(at);
                addSlot(at, {
                    op: ["s", path, value],
                    dead: false,
                    order: 0,
                    index: 0,
                });
                return;
            }
            // a truncate/append pair from an earlier string diff: both must go
            killHere(at);
        }
        killSubtree(at);
        addSlot(at, {
            op: ["s", path, value],
            dead: false,
            order: 0,
            index: 0,
            str: { anchor: previous, value },
        });
    };
    const record = (op) => {
        if (forceBase)
            return;
        hasPending = true;
        const path = (op[0] === "r" ? [] : op[1]);
        const existing = findLogNode(path);
        const anchored = existing === undefined ? undefined : liveSlot(existing);
        if (anchored?.str !== undefined) {
            switch (op[0]) {
                case "a":
                    anchored.str.value += op[2];
                    return;
                case "t":
                    anchored.str.value = anchored.str.value.slice(op[2]);
                    return;
                case "s":
                    if (typeof op[2] === "string") {
                        anchored.str.value = op[2];
                        return;
                    }
            }
            if (existing !== undefined)
                killSubtree(existing);
        }
        else if (existing !== undefined && (op[0] === "s" || op[0] === "d" || op[0] === "r")) {
            // A replacement absorbed into an ancestor payload must still invalidate
            // operations already recorded at and below its destination.
            killSubtree(existing);
        }
        if (path.length > 0) {
            const fold = foldTarget(path);
            if (fold !== undefined) {
                if (fold.item !== undefined) {
                    const items = fold.slot.op[4];
                    if (fold.rest.length === 0) {
                        if (op[0] === "s") {
                            items[fold.item] = cloneJson(op[2]);
                            return;
                        }
                    }
                    else if (foldInto(items[fold.item], fold.rest, op))
                        return;
                }
                else {
                    const payload = fold.slot.op[0] === "r" ? fold.slot.op[1] : fold.slot.op[2];
                    if (foldInto(payload, fold.rest, op))
                        return;
                }
            }
        }
        const at = logNode(path);
        const live = liveSlot(at);
        if (live !== undefined) {
            const previous = live.op;
            if (op[0] === "a" && previous[0] === "a") {
                live.op = ["a", previous[1], previous[2] + op[2]];
                return;
            }
            if (op[0] === "a" && (previous[0] === "s" || previous[0] === "r")) {
                const value = previous[0] === "r" ? previous[1] : previous[2];
                if (typeof value === "string") {
                    live.op = previous[0] === "r" ? ["r", value + op[2]] : ["s", previous[1], value + op[2]];
                    return;
                }
            }
            if (op[0] === "t" && (previous[0] === "s" || previous[0] === "r")) {
                const value = previous[0] === "r" ? previous[1] : previous[2];
                if (typeof value === "string") {
                    const cut = value.slice(op[2]);
                    live.op = previous[0] === "r" ? ["r", cut] : ["s", previous[1], cut];
                    return;
                }
            }
            if (op[0] === "p" && previous[0] === "p") {
                const previousItems = previous[4];
                // These rewrites are sound only for adjacent recorded operations. A
                // tombstoned operation remains a barrier through lastAddedSlot.
                if (previous[3] === 0 &&
                    op[3] === 0 &&
                    previous[2] + previousItems.length === op[2] &&
                    lastAddedSlot === live) {
                    const items = op[4];
                    for (let index = 0; index < items.length; index++)
                        previousItems.push(items[index]);
                    return;
                }
                if (previous[3] === 0 &&
                    lastAddedSlot === live &&
                    op[2] >= previous[2] &&
                    op[2] + op[3] <= previous[2] + previousItems.length) {
                    spliceItems(previousItems, op[2] - previous[2], op[3], op[4]);
                    if (previousItems.length === 0 && previous[3] === 0)
                        killSlot(live);
                    return;
                }
                if (op[3] > 0 &&
                    op[4].length === 0 &&
                    previousItems.length > 0 &&
                    lastAddedSlot === live) {
                    const from = op[2] - previous[2];
                    if (from >= 0 && from + op[3] === previousItems.length) {
                        previousItems.length = from;
                        if (previousItems.length === 0 && previous[3] === 0)
                            killSlot(live);
                        return;
                    }
                }
            }
            if (op[0] === "s" || op[0] === "d" || op[0] === "r")
                killHere(at);
        }
        if (op[0] === "s" || op[0] === "r" || op[0] === "d") {
            // Replacements dominate all earlier descendants, including generations
            // detached by array splices.
            killSubtree(at);
        }
        else if (op[0] === "p") {
            // Splices preserve earlier writes but form a barrier for later folding.
            retireKids(at);
        }
        addSlot(at, { op, dead: false, order: 0, index: 0 });
    };
    // Local diff, used when a whole container is assigned: keeps op quality
    // without a baseline by comparing the outgoing value with the incoming one at
    // the moment of the write. String leaves go through recordString so every
    // string path keeps a single anchored slot; otherwise a later write to the
    // same string would have to supersede ops whose starting value it no longer
    // knows.
    const diffInto = (before, after, at) => {
        if (forceBase)
            return;
        hasPending = true;
        if (before === after)
            return;
        if (typeof before === "string" && typeof after === "string") {
            recordString(at.slice(), before, after);
            return;
        }
        if (Array.isArray(before) && Array.isArray(after) && before.length === after.length) {
            for (let index = 0; index < after.length; index++) {
                at.push(index);
                diffInto(before[index], after[index], at);
                at.pop();
            }
            return;
        }
        if (isObj(before) && isObj(after) && !Array.isArray(before) && !Array.isArray(after)) {
            const beforeObject = before;
            const afterObject = after;
            const beforeKeys = Object.keys(beforeObject);
            const afterKeys = Object.keys(afterObject);
            if ([...beforeKeys, ...afterKeys].some((key) => RESERVED_SEGMENTS.has(key))) {
                record(["s", at.slice(), cloneJson(after)]);
                return;
            }
            for (const key of afterKeys) {
                at.push(key);
                if (Object.hasOwn(beforeObject, key))
                    diffInto(beforeObject[key], afterObject[key], at);
                else
                    record(["s", at.slice(), cloneJson(afterObject[key])]);
                at.pop();
            }
            for (const key of beforeKeys) {
                if (!Object.hasOwn(afterObject, key))
                    record(["d", [...at, key]]);
            }
            return;
        }
        // arrays of differing length, and everything else: chord's own diff
        const out = [];
        diffValue(before, after, at, scan, out);
        for (const op of out)
            record(op);
    };
    const guard = (segment) => {
        if (typeof segment === "symbol")
            throw new UnsafePathError(String(segment));
        if (typeof segment === "string" && RESERVED_SEGMENTS.has(segment))
            throw new UnsafePathError(segment);
        return segment;
    };
    const integer = (value) => {
        const number = Number(value);
        if (Number.isNaN(number) || number === 0)
            return 0;
        return Number.isFinite(number) ? Math.trunc(number) : number;
    };
    const spliceRange = (length, args) => {
        const rawStart = args.length === 0 ? 0 : integer(args[0]);
        const index = rawStart < 0 ? Math.max(0, length + rawStart) : Math.min(rawStart, length);
        const remove = args.length === 0
            ? 0
            : args.length === 1
                ? length - index
                : Math.max(0, Math.min(integer(args[1]), length - index));
        return { index, remove };
    };
    // Paths are walked from placement cells rather than baked into proxies. A held
    // descendant keeps its cells and their owner entries alive, so an ancestor can
    // be re-created after GC without losing array-renumbering metadata. Entries keep
    // only weak references to public proxies; parent caches keep only weak cells.
    let shape = 0;
    // Ordinary tree entries are weak values. Explicit aliases are sparse and stay
    // strong while their raw weak key is alive so alias locations survive a period
    // in which no public proxy exists.
    const entries = new WeakMap();
    const aliases = new WeakMap();
    // A live public proxy retains its entry through this ephemeron. Raw proxy RHS
    // values can therefore be unwrapped before they are written into plain targets.
    const proxyEntries = new WeakMap();
    // WeakRef targets already stay alive through the current JavaScript job. These
    // strong job-local caches avoid repeated deref bookkeeping without extending
    // that lifetime; one microtask drops both maps before the next job.
    let jobCells = new WeakMap();
    let jobProxies = new WeakMap();
    let jobCleanupScheduled = false;
    const scheduleJobCleanup = () => {
        if (jobCleanupScheduled)
            return;
        jobCleanupScheduled = true;
        queueMicrotask(() => {
            jobCells = new WeakMap();
            jobProxies = new WeakMap();
            jobCleanupScheduled = false;
        });
    };
    const keepCellForJob = (ref, cell) => {
        jobCells.set(ref, cell);
        scheduleJobCleanup();
    };
    const derefCell = (ref) => {
        const cached = jobCells.get(ref);
        if (cached !== undefined)
            return cached;
        const cell = ref.deref();
        if (cell !== undefined)
            keepCellForJob(ref, cell);
        return cell;
    };
    const entryFinalizer = new FinalizationRegistry(({ target, token }) => {
        const raw = target.deref();
        if (raw !== undefined && entries.get(raw)?.token === token)
            entries.delete(raw);
    });
    const cellFinalizer = new FinalizationRegistry(({ owner, key, ref }) => {
        const entry = owner.deref();
        if (entry === undefined)
            return;
        if (key !== undefined && entry.childProxies?.get(key) === ref)
            entry.childProxies.delete(key);
        entry.childCells?.delete(ref);
    });
    // Almost no document ever puts one object at two positions. Until one does,
    // every write has exactly one path, so the per-write cell walk is skipped.
    let aliased = false;
    const isDetached = (cell) => {
        for (let c = cell; c !== undefined; c = c.parent)
            if (c.dead)
                return true;
        return false;
    };
    const pathOf = (cell) => {
        if (cell.at === shape && cell.cached !== undefined)
            return cell.cached;
        const out = [];
        for (let c = cell; c !== undefined && c.parent !== undefined; c = c.parent)
            out.push(c.seg);
        out.reverse();
        cell.at = shape;
        cell.cached = out;
        return out;
    };
    const liveCells = (entry) => {
        const out = [];
        for (const cell of entry.cells)
            if (!isDetached(cell))
                out.push(cell);
        return out;
    };
    const primary = (entry) => (aliased ? (liveCells(entry)[0] ?? entry.fallback) : entry.fallback);
    const pathNow = (entry) => pathOf(primary(entry));
    const findEntry = (target) => {
        const alias = aliases.get(target);
        if (alias !== undefined)
            return alias;
        const slot = entries.get(target);
        const entry = slot?.ref.deref();
        if (slot !== undefined && entry === undefined)
            entries.delete(target);
        return entry;
    };
    const indexEntry = (entry) => {
        const token = {};
        entries.set(entry.target, { ref: new WeakRef(entry), token });
        entryFinalizer.register(entry, { target: new WeakRef(entry.target), token });
    };
    const addPlacement = (entry, cell) => {
        if (entry.cells.has(cell))
            return;
        let hasLive = false;
        for (const existing of entry.cells) {
            if (!isDetached(existing)) {
                hasLive = true;
                break;
            }
        }
        cell.entry = entry;
        cell.target = entry.target;
        entry.cells.add(cell);
        if (!hasLive) {
            const previous = entry.fallback;
            entry.fallback = cell;
            if (previous !== cell) {
                shape++;
                // Reattaching a held container moves every known immediate child to
                // its new placement. Array registrations survive cache clears.
                const children = new Set();
                for (const ref of entry.childProxies?.values() ?? []) {
                    const child = derefCell(ref);
                    if (child !== undefined)
                        children.add(child);
                }
                for (const ref of entry.childCells ?? []) {
                    const child = derefCell(ref);
                    if (child !== undefined)
                        children.add(child);
                }
                for (const child of children) {
                    if (!child.dead && child.parent === previous)
                        child.parent = cell;
                }
            }
        }
        else if (entry.blocked === undefined) {
            aliased = true;
            aliases.set(entry.target, entry);
        }
    };
    const cachePlacement = (owner, key, cell) => {
        let cleanup = cell.cleanup;
        if (cleanup === undefined) {
            cleanup = { owner: new WeakRef(owner), key: undefined, ref: new WeakRef(cell) };
            cell.cleanup = cleanup;
            cellFinalizer.register(cell, cleanup);
        }
        keepCellForJob(cleanup.ref, cell);
        if (cleanup.key !== undefined && cleanup.key !== key && owner.childProxies?.get(cleanup.key) === cleanup.ref) {
            owner.childProxies.delete(cleanup.key);
        }
        if (owner.childProxies === undefined)
            owner.childProxies = new Map();
        owner.childProxies.set(key, cleanup.ref);
        cleanup.key = key;
        if (Array.isArray(owner.target)) {
            if (owner.childCells === undefined)
                owner.childCells = new Set();
            owner.childCells.add(cleanup.ref);
        }
    };
    const findPlacement = (owner, parent, segment, target, blocked) => {
        if (blocked === undefined) {
            const known = findEntry(target);
            if (known !== undefined) {
                for (const cell of known.cells) {
                    if (!cell.dead && cell.owner === owner && cell.parent === parent && cell.seg === segment)
                        return cell;
                }
            }
            return undefined;
        }
        // Blocked views are intentionally absent from the ordinary raw-entry index.
        for (const ref of owner.childCells ?? []) {
            const cell = derefCell(ref);
            if (cell !== undefined &&
                !cell.dead &&
                cell.target === target &&
                cell.entry?.blocked === blocked &&
                cell.parent === parent &&
                cell.seg === segment) {
                return cell;
            }
        }
        return undefined;
    };
    const unwrap = (value) => (isObj(value) ? (proxyEntries.get(value)?.target ?? value) : value);
    const adoptItems = (values) => values.map(unwrap);
    // Every operation is emitted once per live location of an explicitly aliased
    // object. Alias cells are independent of public proxy lifetimes.
    const emit = (entry, op) => {
        record(op);
        if (!aliased)
            return;
        const live = liveCells(entry);
        if (live.length <= 1)
            return;
        const base = pathNow(entry);
        const head = primary(entry);
        for (const cell of live) {
            if (cell === head || op[0] === "r")
                continue;
            const rest = op[1].slice(base.length);
            const cloned = [...op];
            cloned[1] = [...pathOf(cell), ...rest];
            record(cloned);
        }
    };
    // Structural mutation updates every still-live placement cell, including cells
    // retained only by held descendants. Dead weak registrations are pruned lazily;
    // finalizers keep idle live parents from accumulating them indefinitely.
    const renumber = (entry, key, before, after, spliceAt, spliceRemove, spliceInsert) => {
        shape++;
        const childCells = entry.childCells;
        if (key === "push" || !Array.isArray(entry.target) || childCells === undefined || childCells.size === 0)
            return;
        let index;
        let remove = 0;
        let insert = 0;
        if (key === "pop") {
            index = before - 1;
            remove = before > 0 ? 1 : 0;
        }
        else if (key === "shift") {
            index = 0;
            remove = before > 0 ? 1 : 0;
        }
        else if (key === "unshift") {
            index = 0;
            insert = after - before;
        }
        else if (key === "splice") {
            index = spliceAt;
            remove = spliceRemove;
            insert = spliceInsert;
        }
        else {
            for (const ref of [...childCells]) {
                const cell = derefCell(ref);
                if (cell === undefined) {
                    childCells.delete(ref);
                    continue;
                }
                const at = entry.target.indexOf(cell.target);
                if (at < 0) {
                    cell.dead = true;
                    childCells.delete(ref);
                }
                else
                    cell.seg = at;
            }
            entry.childProxies?.clear();
            return;
        }
        const delta = insert - remove;
        for (const ref of [...childCells]) {
            const cell = derefCell(ref);
            if (cell === undefined) {
                childCells.delete(ref);
                continue;
            }
            const at = cell.seg;
            if (typeof at !== "number")
                continue;
            if (at >= index && at < index + remove) {
                cell.dead = true;
                childCells.delete(ref);
            }
            else if (at >= index + remove)
                cell.seg = at + delta;
        }
        entry.childProxies?.clear();
    };
    const wrap = (object, cell, blockedSegment) => {
        const placed = cell.entry;
        if (placed !== undefined && placed.target === object && placed.blocked === blockedSegment) {
            return proxyFor(placed);
        }
        const existing = blockedSegment === undefined ? findEntry(object) : undefined;
        if (existing !== undefined) {
            addPlacement(existing, cell);
            return proxyFor(existing);
        }
        const entry = {
            target: object,
            cells: new Set(),
            fallback: cell,
            blocked: blockedSegment,
            proxy: undefined,
            childProxies: undefined,
            childCells: undefined,
        };
        addPlacement(entry, cell);
        if (blockedSegment === undefined)
            indexEntry(entry);
        return proxyFor(entry);
    };
    const handlerPrototype = {
        get(target, key, receiver) {
            const entry = this.entry;
            if (Array.isArray(target) && typeof key === "string" && MUTATORS.has(key)) {
                return (...args) => {
                    if (entry.blocked !== undefined)
                        throw new UnsafePathError(entry.blocked);
                    const detached = aliased ? liveCells(entry).length === 0 : isDetached(entry.fallback);
                    if (detached) {
                        return Reflect.apply(Array.prototype[key], target, args.map(unwrap));
                    }
                    const before = target.length;
                    let spliceAt = -1;
                    let spliceRemove = 0;
                    let spliceInsert = 0;
                    let insertAt = -1;
                    let insertCount = 0;
                    let result;
                    switch (key) {
                        case "push": {
                            const items = adoptItems(args);
                            if (items.length > 0)
                                emit(entry, ["p", [...pathNow(entry)], before, 0, cloneJson(items)]);
                            insertAt = before;
                            insertCount = items.length;
                            spliceItems(target, before, 0, items);
                            result = target.length;
                            break;
                        }
                        case "unshift": {
                            const items = adoptItems(args);
                            if (items.length > 0)
                                emit(entry, ["p", [...pathNow(entry)], 0, 0, cloneJson(items)]);
                            insertAt = 0;
                            insertCount = items.length;
                            spliceItems(target, 0, 0, items);
                            result = target.length;
                            break;
                        }
                        case "pop":
                            if (before > 0)
                                emit(entry, ["p", [...pathNow(entry)], before - 1, 1, []]);
                            result = Reflect.apply(Array.prototype.pop, target, args);
                            break;
                        case "shift":
                            if (before > 0)
                                emit(entry, ["p", [...pathNow(entry)], 0, 1, []]);
                            result = Reflect.apply(Array.prototype.shift, target, args);
                            break;
                        case "splice": {
                            const items = adoptItems(args.slice(2));
                            const { index, remove } = spliceRange(before, args);
                            spliceAt = index;
                            spliceRemove = remove;
                            spliceInsert = items.length;
                            insertAt = index;
                            insertCount = items.length;
                            if (remove > 0 || items.length > 0) {
                                if (index === 0 && remove === before) {
                                    if (pathNow(entry).length === 0)
                                        emit(entry, ["r", cloneJson(items)]);
                                    else
                                        emit(entry, [
                                            "s",
                                            [...pathNow(entry)],
                                            cloneJson(items),
                                        ]);
                                }
                                else
                                    emit(entry, ["p", [...pathNow(entry)], index, remove, cloneJson(items)]);
                            }
                            result = spliceItems(target, index, remove, items);
                            break;
                        }
                        default: {
                            result = Reflect.apply(Array.prototype[key], target, args.map(unwrap));
                            if (pathNow(entry).length === 0)
                                emit(entry, ["r", cloneJson(target)]);
                            else
                                emit(entry, [
                                    "s",
                                    [...pathNow(entry)],
                                    cloneJson(target),
                                ]);
                        }
                    }
                    collapsePending();
                    renumber(entry, key, before, target.length, spliceAt, spliceRemove, spliceInsert);
                    for (let index = insertAt; insertCount > 0 && index < insertAt + insertCount; index++) {
                        const item = target[index];
                        if (!isObj(item))
                            continue;
                        const known = findEntry(item);
                        if (known === undefined || known.target === entry.target)
                            continue;
                        const parent = primary(entry);
                        let seen = false;
                        for (const existingCell of known.cells) {
                            if (!existingCell.dead && existingCell.parent === parent && existingCell.seg === index)
                                seen = true;
                        }
                        if (!seen) {
                            const inserted = {
                                parent,
                                owner: entry,
                                entry: undefined,
                                target: item,
                                seg: index,
                                dead: false,
                            };
                            addPlacement(known, inserted);
                            cachePlacement(entry, String(index), inserted);
                        }
                    }
                    return key === "sort" || key === "reverse" || key === "fill" || key === "copyWithin" ? receiver : result;
                };
            }
            const value = Reflect.get(target, key, receiver);
            if (!isObj(value))
                return value;
            const cachedRef = entry.childProxies?.get(key);
            const cached = cachedRef === undefined ? undefined : derefCell(cachedRef);
            if (cached !== undefined && cached.target === value && cached.entry !== undefined) {
                return proxyFor(cached.entry);
            }
            if (cachedRef !== undefined && cached === undefined && entry.childProxies?.get(key) === cachedRef) {
                entry.childProxies?.delete(key);
            }
            const rawSegment = norm(target, key);
            let segment;
            let childBlocked = entry.blocked;
            if (entry.blocked !== undefined) {
                if (typeof rawSegment === "symbol")
                    throw new UnsafePathError(String(rawSegment));
                segment = rawSegment;
            }
            else if (typeof rawSegment === "string" && RESERVED_SEGMENTS.has(rawSegment) && Object.hasOwn(target, key)) {
                segment = rawSegment;
                childBlocked = rawSegment;
            }
            else
                segment = guard(rawSegment);
            const parent = primary(entry);
            const existingCell = findPlacement(entry, parent, segment, value, childBlocked);
            if (existingCell?.entry !== undefined) {
                cachePlacement(entry, key, existingCell);
                return proxyFor(existingCell.entry);
            }
            const childCell = {
                parent,
                owner: entry,
                entry: undefined,
                target: value,
                seg: segment,
                dead: false,
            };
            const child = wrap(value, childCell, childBlocked);
            cachePlacement(entry, key, childCell);
            return child;
        },
        set(target, key, value) {
            const entry = this.entry;
            if (entry.blocked !== undefined)
                throw new UnsafePathError(entry.blocked);
            const rawValue = unwrap(value);
            if (aliased ? liveCells(entry).length === 0 : isDetached(entry.fallback)) {
                return Reflect.set(target, key, rawValue);
            }
            if (Array.isArray(target) && key === "length") {
                const before = target.length;
                const next = Number(rawValue);
                if (!Number.isSafeInteger(next) || next < 0 || next > 4_294_967_295) {
                    return Reflect.set(target, key, rawValue);
                }
                if (next < before) {
                    if (next === 0) {
                        if (pathNow(entry).length === 0)
                            emit(entry, ["r", []]);
                        else
                            emit(entry, ["s", [...pathNow(entry)], []]);
                    }
                    else
                        emit(entry, ["p", [...pathNow(entry)], next, before - next, []]);
                    Reflect.set(target, key, next);
                    renumber(entry, "splice", before, next, next, before - next, 0);
                    entry.childProxies?.clear();
                }
                else if (next > before) {
                    target.length = next;
                    target.fill(null, before);
                    const grown = new Array(next - before).fill(null);
                    emit(entry, ["p", [...pathNow(entry)], before, 0, grown]);
                }
                collapsePending();
                return true;
            }
            const segment = guard(norm(target, key));
            if (Array.isArray(target)) {
                if (typeof segment !== "number")
                    throw new UnsafePathError(segment);
                if (segment > target.length)
                    throw new UnsafePathError(segment);
            }
            const at = [...pathNow(entry), segment];
            if (rawValue === undefined) {
                if (Array.isArray(target))
                    throw new TypeError("undefined would create a sparse array; use splice instead");
                if (Object.hasOwn(target, key))
                    emit(entry, ["d", at]);
                entry.childProxies?.delete(key);
                const deleted = Reflect.deleteProperty(target, key);
                if (deleted)
                    collapsePending();
                return deleted;
            }
            const previous = target[key];
            if (previous === rawValue)
                return true;
            if (Array.isArray(target) && segment === target.length) {
                emit(entry, ["p", [...pathNow(entry)], target.length, 0, [cloneJson(rawValue)]]);
            }
            else if (isObj(previous) && isObj(rawValue)) {
                diffInto(previous, rawValue, [...pathNow(entry), segment]);
            }
            else if (typeof previous === "string" && typeof rawValue === "string") {
                if (!aliased)
                    recordString([...pathNow(entry), segment], previous, rawValue);
                else
                    for (const cell of liveCells(entry))
                        recordString([...pathOf(cell), segment], previous, rawValue);
            }
            else {
                emit(entry, ["s", at, cloneJson(rawValue)]);
            }
            entry.childProxies?.delete(key);
            const updated = Reflect.set(target, key, rawValue);
            if (updated) {
                if (isObj(rawValue)) {
                    const known = findEntry(rawValue);
                    if (known !== undefined) {
                        const placement = {
                            parent: primary(entry),
                            owner: entry,
                            entry: undefined,
                            target: rawValue,
                            seg: segment,
                            dead: false,
                        };
                        addPlacement(known, placement);
                        cachePlacement(entry, key, placement);
                    }
                }
                collapsePending();
            }
            return updated;
        },
        deleteProperty(target, key) {
            const entry = this.entry;
            if (entry.blocked !== undefined)
                throw new UnsafePathError(entry.blocked);
            if (aliased ? liveCells(entry).length === 0 : isDetached(entry.fallback)) {
                return Reflect.deleteProperty(target, key);
            }
            const segment = guard(norm(target, key));
            if (Array.isArray(target)) {
                if (typeof segment !== "number")
                    throw new UnsafePathError(segment);
                throw new TypeError("delete would create a sparse array; use splice instead");
            }
            if (Object.hasOwn(target, key))
                emit(entry, ["d", [...pathNow(entry), segment]]);
            entry.childProxies?.delete(key);
            const deleted = Reflect.deleteProperty(target, key);
            if (deleted)
                collapsePending();
            return deleted;
        },
        defineProperty() {
            throw new TypeError("defineProperty is not supported on tracked state; use assignment");
        },
        setPrototypeOf() {
            throw new TypeError("setPrototypeOf is not supported on tracked state");
        },
        preventExtensions() {
            throw new TypeError("preventExtensions is not supported on tracked state");
        },
    };
    function proxyFor(entry) {
        const cached = jobProxies.get(entry);
        if (cached !== undefined)
            return cached;
        const existing = entry.proxy?.deref();
        if (existing !== undefined) {
            jobProxies.set(entry, existing);
            scheduleJobCleanup();
            return existing;
        }
        const handler = Object.create(handlerPrototype);
        handler.entry = entry;
        const proxy = new Proxy(entry.target, handler);
        entry.proxy = new WeakRef(proxy);
        jobProxies.set(entry, proxy);
        scheduleJobCleanup();
        proxyEntries.set(proxy, entry);
        return proxy;
    }
    const rootCell = {
        parent: undefined,
        owner: undefined,
        entry: undefined,
        target: root,
        seg: "",
        dead: false,
    };
    let state = wrap(root, rootCell);
    return {
        get state() {
            return state;
        },
        get target() {
            return root;
        },
        set state(next) {
            const rawNext = unwrap(next);
            if (rawNext === root) {
                clearPending();
                forceBase = true;
                return;
            }
            clearPending();
            root = rawNext;
            state = wrap(root, {
                parent: undefined,
                owner: undefined,
                entry: undefined,
                target: root,
                seg: "",
                dead: false,
            });
            forceBase = true;
        },
        rebase() {
            clearPending();
            forceBase = true;
        },
        get dirty() {
            // conservative: true if anything was written since the last flush, even
            // if the writes cancelled out
            return forceBase || hasPending;
        },
        discard() {
            clearPending();
        },
        flush() {
            if (forceBase) {
                const value = cloneJson(root);
                forceBase = false;
                clearPending();
                return [["r", value]];
            }
            if (!hasPending)
                return [];
            const out = [];
            for (const slot of log) {
                if (slot === undefined || slot.dead)
                    continue;
                if (slot.str !== undefined) {
                    diffValue(slot.str.anchor, slot.str.value, slot.op[1], scan, out);
                    continue;
                }
                out.push(slot.op);
            }
            clearPending();
            return out;
        },
    };
}
// ─── Path safety ─────────────────────────────────────────────────────────────
/**
 * Segments that reach the prototype chain.
 *
 * `JSON.parse` is safe on its own — it makes `__proto__` an own property. What is
 * not safe is `parent[key] = value`, which is exactly what an applier does, and
 * paths are data: `["s", ["__proto__", "isAdmin"], true]` pollutes
 * `Object.prototype` for the whole process.
 *
 * Ops arrive from a facet, a plugin compartment, or a tool whose details may echo
 * model output, so none of it is trusted input.
 */
export const RESERVED_SEGMENTS = new Set(["__proto__", "constructor", "prototype"]);
export class UnsafePathError extends Error {
    // Not a parameter property: Node's --experimental-strip-types rejects those,
    // and these files are meant to run under it directly.
    segment;
    constructor(segment) {
        super(`unsafe path segment: ${String(segment)}`);
        this.segment = segment;
        this.name = "UnsafePathError";
    }
}
/**
 * Verb, arity and payload shape for a **decoded** op: paths inline, no `#`, no
 * short forms. `apply` uses this.
 *
 * Validating `Op` against the wire grammar would be laxer than the type: a
 * two-element `["s", value]` would pass, and `apply` would then read the value as
 * a path. Each vocabulary gets the validator that matches it.
 */
export function assertValidOp(op) {
    if (!Array.isArray(op) || op.length === 0)
        throw new TypeError("op is not a tuple");
    switch (op[0]) {
        case "r":
            if (op.length !== 2)
                throw new TypeError("r arity");
            return;
        case "s":
            if (op.length !== 3)
                throw new TypeError("s arity");
            assertPathArg(op[1], true);
            return;
        case "d":
            if (op.length !== 2)
                throw new TypeError("d arity");
            assertPathArg(op[1], true);
            return;
        case "a":
            if (op.length !== 3 || typeof op[2] !== "string")
                throw new TypeError("a shape");
            assertPathArg(op[1], true);
            return;
        case "t":
            if (op.length !== 3 || !Number.isInteger(op[2]) || op[2] < 0)
                throw new TypeError("t shape");
            assertPathArg(op[1], true);
            return;
        case "p": {
            if (op.length !== 5)
                throw new TypeError("p arity");
            assertPathArg(op[1]);
            if (!Number.isInteger(op[2]) || op[2] < 0)
                throw new TypeError("p index");
            if (!Number.isInteger(op[3]) || op[3] < 0)
                throw new TypeError("p remove");
            if (!Array.isArray(op[4]))
                throw new TypeError("p items");
            return;
        }
        case "m":
            if (op.length !== 3)
                throw new TypeError("m arity");
            assertPathArg(op[1]);
            assertPermutation(op[2]);
            return;
        // Silently skipping an unknown verb is how a newer producer's op vanishes.
        default:
            throw new TypeError(`unknown op verb: ${String(op[0])}`);
    }
}
function assertPathArg(p, nonEmpty = false) {
    if (!Array.isArray(p))
        throw new TypeError("path is not an array");
    if (nonEmpty && p.length === 0)
        throw new TypeError("path is empty");
    assertSafePath(p);
}
function assertPermutation(value) {
    if (!Array.isArray(value))
        throw new TypeError("m permutation is not an array");
    const seen = new Uint8Array(value.length);
    for (const index of value) {
        if (!Number.isInteger(index) || index < 0 || index >= value.length || seen[index] !== 0) {
            throw new TypeError("m permutation is not a bijection");
        }
        seen[index] = 1;
    }
}
/** The same, for the wire grammar: ids and short forms are legal here. */
export function assertValidWireOp(op) {
    if (!Array.isArray(op) || op.length === 0)
        throw new TypeError("op is not a tuple");
    const [verb] = op;
    const okRef = (r) => {
        if (typeof r === "number") {
            if (!Number.isInteger(r) || r < 0)
                throw new TypeError("bad path id");
            return;
        }
        // A string is not a path. Unchecked, `"a".slice(0, -1)` is `""`, so it
        // resolves to the ROOT and writes there — a path that is not a path, accepted.
        if (!Array.isArray(r))
            throw new TypeError("path is not an array");
        assertSafePath(r);
    };
    switch (verb) {
        case "r":
            if (op.length !== 2)
                throw new TypeError("r arity");
            return;
        case "s":
            if (op.length === 3)
                okRef(op[1]);
            else if (op.length !== 2)
                throw new TypeError("s arity");
            return;
        case "d":
            if (op.length === 2)
                okRef(op[1]);
            else if (op.length !== 1)
                throw new TypeError("d arity");
            return;
        case "a":
            if (op.length === 3) {
                okRef(op[1]);
                if (typeof op[2] !== "string")
                    throw new TypeError("a value");
            }
            else if (op.length === 2) {
                if (typeof op[1] !== "string")
                    throw new TypeError("a value");
            }
            else
                throw new TypeError("a arity");
            return;
        case "t":
            if (op.length === 3) {
                okRef(op[1]);
                if (!Number.isInteger(op[2]) || op[2] < 0)
                    throw new TypeError("t count");
            }
            else if (op.length === 2) {
                if (!Number.isInteger(op[1]) || op[1] < 0)
                    throw new TypeError("t count");
            }
            else
                throw new TypeError("t arity");
            return;
        case "p": {
            const [i, r, items] = op.length === 5 ? [op[2], op[3], op[4]] : op.length === 4 ? [op[1], op[2], op[3]] : [];
            if (items === undefined)
                throw new TypeError("p arity");
            if (op.length === 5)
                okRef(op[1]);
            if (!Number.isInteger(i) || i < 0)
                throw new TypeError("p index");
            if (!Number.isInteger(r) || r < 0)
                throw new TypeError("p remove");
            if (!Array.isArray(items))
                throw new TypeError("p items");
            return;
        }
        case "m":
            if (op.length === 3)
                okRef(op[1]);
            else if (op.length !== 2)
                throw new TypeError("m arity");
            assertPermutation(op[op.length - 1]);
            return;
        case "#": {
            if (op.length !== 3 || !Number.isInteger(op[1]) || op[1] < 0 || !Array.isArray(op[2])) {
                throw new TypeError("# shape");
            }
            assertSafePath(op[2]);
            return;
        }
        // Silently skipping an unknown verb is how a newer producer's op vanishes.
        default:
            throw new TypeError(`unknown op verb: ${String(verb)}`);
    }
}
export function assertSafePath(path) {
    for (const seg of path) {
        if (typeof seg === "string") {
            if (RESERVED_SEGMENTS.has(seg))
                throw new UnsafePathError(seg);
        }
        else if (!Number.isInteger(seg) || seg < 0) {
            throw new UnsafePathError(seg);
        }
    }
}
/**
 * An index may address an existing element or append exactly one past the end.
 *
 * This is not an arbitrary cap — it is what keeps the value a `JsonValue`. A
 * sparse array does not survive a JSON round trip: holes serialise to `null` and
 * return as real properties, so `arr[7] = x` on a length-3 array already produces
 * state a replica cannot match. Rejecting the write is more honest than silently
 * diverging.
 *
 * It also removes the denial of service it would otherwise permit:
 * `["s", ["xs", 4294967290], 1]` allocates a 4.29-billion-entry array from one op.
 * Growth stays possible and stays proportional — the tracker already emits
 * `arr.length = n` as a splice of explicit nulls, whose op size grows with the
 * gap, so a large growth costs a large op rather than a small one.
 */
function assertIndexInRange(parent, index) {
    if (index > parent.length)
        throw new UnsafePathError(index);
}
// ─── Applier ─────────────────────────────────────────────────────────────────
export class PathError extends Error {
    path;
    constructor(path) {
        super(`unresolvable path: ${JSON.stringify(path)}`);
        this.path = path;
        this.name = "PathError";
    }
}
/**
 * Apply ops to a plain mutable value. Returns the value, because `r` replaces it
 * outright and cannot be done in place.
 *
 * Takes decoded ops. Path ids and omitted paths are a wire concern — run
 * `decode` first if the ops came from a boundary.
 */
export function apply(target, ops) {
    return applyOps(target, ops);
}
function applyOps(target, ops) {
    let root = target;
    for (const op of ops) {
        assertValidOp(op);
        if (op[0] === "r") {
            // Adopted, not copied. The consumer owns the batch it was handed.
            //
            // Fanning one batch out to several consumers in-process therefore makes
            // their replicas alias each other. That is an ownership rule, not a
            // defect: copy the batch at the fan-out point, or let each consumer
            // decode its own. A batch that crosses a real boundary is already
            // distinct, because serialisation produces fresh objects.
            root = op[1];
            continue;
        }
        const path = op[1];
        assertSafePath(path);
        if (op[0] === "p") {
            const target_ = path.length === 0 ? root : resolve(root, path);
            if (!Array.isArray(target_))
                throw new PathError(path);
            target_.splice(op[2], op[3]);
            const chunkSize = 10_000;
            for (let offset = 0; offset < op[4].length; offset += chunkSize) {
                target_.splice(op[2] + offset, 0, ...op[4].slice(offset, offset + chunkSize));
            }
            continue;
        }
        if (op[0] === "m") {
            const target_ = path.length === 0 ? root : resolve(root, path);
            if (!Array.isArray(target_) || target_.length !== op[2].length)
                throw new PathError(path);
            const previous = target_.slice();
            for (let index = 0; index < op[2].length; index++)
                target_[index] = previous[op[2][index]];
            continue;
        }
        // s/d/a/t can never target the root — the type forbids it.
        const parent = resolve(root, path.slice(0, -1));
        const key = path[path.length - 1];
        if (Array.isArray(parent)) {
            if (typeof key !== "number")
                throw new UnsafePathError(key);
            assertIndexInRange(parent, key);
        }
        // defineProperty rather than assignment: a setter inherited from the prototype
        // chain would otherwise run on write.
        const write = (value) => {
            Object.defineProperty(parent, key, { value, writable: true, enumerable: true, configurable: true });
        };
        const read = () => (Object.hasOwn(parent, key) ? parent[key] : undefined);
        switch (op[0]) {
            case "s":
                write(op[2]);
                break;
            case "d":
                if (Array.isArray(parent)) {
                    if (typeof key !== "number" || key >= parent.length)
                        throw new PathError(path);
                    parent.splice(key, 1);
                }
                else
                    delete parent[key];
                break;
            case "a": {
                const current = read();
                if (typeof current !== "string")
                    throw new PathError(path);
                write(`${current}${op[2]}`);
                break;
            }
            case "t": {
                const current = read();
                if (typeof current !== "string")
                    throw new PathError(path);
                write(current.slice(op[2]));
                break;
            }
        }
    }
    return root;
}
/** Apply decoded operations without mutating the previous immutable value. */
export function applyImmutable(target, ops) {
    let root = target;
    for (const op of ops) {
        if (op[0] === "r") {
            assertValidOp(op);
            root = op[1];
            continue;
        }
        root = copyContainers(root, op[0] === "p" || op[0] === "m" ? op[1] : op[1].slice(0, -1));
        root = applyOps(root, [op]);
    }
    return root;
}
function copyContainers(root, path) {
    const copy = (value) => {
        if (Array.isArray(value))
            return value.slice();
        if (!isObj(value))
            throw new PathError(path);
        const result = Object.create(Object.getPrototypeOf(value) === null ? null : Object.prototype);
        for (const key of Object.keys(value)) {
            Object.defineProperty(result, key, {
                value: value[key],
                writable: true,
                enumerable: true,
                configurable: true,
            });
        }
        return result;
    };
    const copiedRoot = copy(root);
    let source = root;
    let destination = copiedRoot;
    for (const segment of path) {
        if (!isObj(source) || !Object.hasOwn(source, segment))
            throw new PathError(path);
        if (Array.isArray(source) && typeof segment !== "number")
            throw new UnsafePathError(segment);
        const child = source[segment];
        const copiedChild = copy(child);
        Object.defineProperty(destination, segment, {
            value: copiedChild,
            writable: true,
            enumerable: true,
            configurable: true,
        });
        source = child;
        destination = copiedChild;
    }
    return copiedRoot;
}
function resolveValue(root, path) {
    let node = root;
    for (const seg of path) {
        if (!isObj(node))
            throw new PathError(path);
        if (Array.isArray(node) && typeof seg !== "number")
            throw new UnsafePathError(seg);
        // Own properties only: an inherited getter must not run, and a walk must not
        // escape the value into the prototype chain.
        if (!Object.hasOwn(node, seg))
            throw new PathError(path);
        node = node[seg];
    }
    return node;
}
function resolve(root, path) {
    const node = resolveValue(root, path);
    if (!isObj(node))
        throw new PathError(path);
    return node;
}
// ─── Codec ───────────────────────────────────────────────────────────────────
//
// Path interning and arity omission live between the tracker and a boundary;
// `Op` and `apply` know nothing about them.
//
// ONE PAIR PER INDEPENDENT STATE STREAM. Every decoder must observe exactly the
// batches encoded by its matching encoder, beginning with that state's base.
// Sharing a transport connection does not make separately hydrated states one
// stream.
const pathKey = (path) => JSON.stringify(path);
/**
 * Intern on SECOND use. A definition costs more than the path it replaces, so
 * interning on first use loses on the many paths written exactly once.
 */
export function encoder() {
    const seen = new Set();
    const ids = new Map();
    let nextId = 0;
    let previous; // last path in THIS batch
    return {
        encode(ops) {
            // Arity omission is scoped to a batch. Letting it span batches would make
            // a batch's first op depend on the previous batch's last one, so a reader
            // that skips or reorders a batch decodes into the wrong path. Ids are the
            // only cross-batch state, and the dictionary makes those explicit.
            previous = undefined;
            const out = [];
            for (const op of ops) {
                if (op[0] === "r") {
                    out.push(op);
                    // A base batch is a RECOVERY POINT: a reader replays from the last one
                    // with a fresh decoder. So everything after it must be self-contained.
                    // Keeping ids across a replacement emits references to definitions the
                    // reader never saw — recovery fails with an unresolvable path id.
                    seen.clear();
                    ids.clear();
                    nextId = 0;
                    previous = undefined;
                    continue;
                }
                const path = op[1];
                const key = pathKey(path);
                // Same path as the previous op: drop the ref entirely.
                if (key === previous) {
                    switch (op[0]) {
                        case "s":
                            out.push(["s", op[2]]);
                            break;
                        case "d":
                            out.push(["d"]);
                            break;
                        case "a":
                            out.push(["a", op[2]]);
                            break;
                        case "t":
                            out.push(["t", op[2]]);
                            break;
                        case "p":
                            out.push(["p", op[2], op[3], op[4]]);
                            break;
                        case "m":
                            out.push(["m", op[2]]);
                            break;
                    }
                    continue;
                }
                let ref = path;
                const existing = ids.get(key);
                if (existing !== undefined) {
                    ref = existing;
                }
                else if (seen.has(key)) {
                    const id = nextId++;
                    ids.set(key, id);
                    out.push(["#", id, path]); // second use: define, then reference
                    ref = id;
                }
                else {
                    seen.add(key); // first use: inline
                }
                switch (op[0]) {
                    case "s":
                        out.push(["s", ref, op[2]]);
                        break;
                    case "d":
                        out.push(["d", ref]);
                        break;
                    case "a":
                        out.push(["a", ref, op[2]]);
                        break;
                    case "t":
                        out.push(["t", ref, op[2]]);
                        break;
                    case "p":
                        out.push(["p", ref, op[2], op[3], op[4]]);
                        break;
                    case "m":
                        out.push(["m", ref, op[2]]);
                        break;
                }
                previous = key;
            }
            return out;
        },
    };
}
export function decoder() {
    const paths = new Map();
    return {
        decode(wire) {
            let previous; // scoped to the batch, as in encode
            const out = [];
            for (const op of wire) {
                assertValidWireOp(op);
                if (op[0] === "#") {
                    assertSafePath(op[2]);
                    paths.set(op[1], op[2]);
                    continue;
                }
                if (op[0] === "r") {
                    out.push(op);
                    paths.clear();
                    previous = undefined;
                    continue;
                }
                // Arity tells us whether a ref is present: the short forms omit it.
                const short = (op[0] === "d" && op.length === 1) ||
                    (op[0] !== "d" && op[0] !== "p" && op.length === 2) ||
                    (op[0] === "p" && op.length === 4);
                let path;
                if (short) {
                    if (previous === undefined)
                        throw new PathError([]);
                    path = previous;
                }
                else {
                    const ref = op[1];
                    if (typeof ref === "number") {
                        const resolved = paths.get(ref);
                        if (resolved === undefined)
                            throw new PathError(ref);
                        path = resolved;
                    }
                    else {
                        path = ref;
                    }
                    previous = path;
                }
                if (op[0] !== "p" && op[0] !== "m" && path.length === 0)
                    throw new PathError(path);
                switch (op[0]) {
                    case "s":
                        out.push(["s", path, (short ? op[1] : op[2])]);
                        break;
                    case "d":
                        out.push(["d", path]);
                        break;
                    case "a":
                        out.push(["a", path, (short ? op[1] : op[2])]);
                        break;
                    case "t":
                        out.push(["t", path, (short ? op[1] : op[2])]);
                        break;
                    case "p": {
                        const [i, r, items] = short
                            ? [op[1], op[2], op[3]]
                            : [op[2], op[3], op[4]];
                        out.push(["p", path, i, r, items]);
                        break;
                    }
                    case "m":
                        out.push(["m", path, (short ? op[1] : op[2])]);
                        break;
                }
            }
            return out;
        },
    };
}
//# sourceMappingURL=index.js.map