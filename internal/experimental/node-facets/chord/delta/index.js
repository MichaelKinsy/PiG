const isObj = (value) => value !== null && typeof value === "object";
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
// ─── Immutable revision tracking ─────────────────────────────────────────────
export { diffRevisions } from "./diff.js";
export { track } from "./tracker.js";
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
/** Apply one decoded operation batch without mutating the previous immutable value. */
export function applyImmutable(target, ops) {
    return applyImmutableBatches(target, [ops]);
}
/**
 * Apply decoded operation batches as one final-result-only replay.
 *
 * Containers copied for an earlier batch may be mutated while applying a later
 * batch, so this deliberately exposes no intermediate revisions.
 */
export function applyImmutableBatches(target, batches) {
    let root = target;
    const owned = new WeakSet();
    for (const ops of batches) {
        for (const op of ops) {
            assertValidOp(op);
            if (op[0] === "r") {
                root = op[1];
                continue;
            }
            root = copyContainers(root, op[0] === "p" || op[0] === "m" ? op[1] : op[1].slice(0, -1), owned);
            root = applyOps(root, [op]);
        }
    }
    return root;
}
function copyContainers(root, path, owned) {
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
    if (!isObj(root))
        throw new PathError(path);
    let copiedRoot;
    if (owned.has(root))
        copiedRoot = root;
    else {
        copiedRoot = copy(root);
        owned.add(copiedRoot);
    }
    let destination = copiedRoot;
    for (const segment of path) {
        if (!Object.hasOwn(destination, segment))
            throw new PathError(path);
        if (Array.isArray(destination) && typeof segment !== "number")
            throw new UnsafePathError(segment);
        const child = destination[segment];
        if (!isObj(child))
            throw new PathError(path);
        if (owned.has(child)) {
            destination = child;
            continue;
        }
        const copiedChild = copy(child);
        Object.defineProperty(destination, segment, {
            value: copiedChild,
            writable: true,
            enumerable: true,
            configurable: true,
        });
        owned.add(copiedChild);
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