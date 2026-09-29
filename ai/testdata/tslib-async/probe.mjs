// Node oracle for ai/tslib_async.go. It prints, for every scenario, the ordered list of `label@round` events. A round is one turn of a self-rescheduling microtask chain started before the scenario body, so it counts microtask generations exactly as the Go executor's FIFO reaction queue does. Go replays each scenario and must produce the identical list (ai/tslib_async_test.go).
//
// Generators are created with `(void 0, void 0, ...)` instead of `(this, arguments, ...)` because scenarios run in arrow functions; the SDK passes its own `this` and arguments, which tslib forwards to the body without effect on ordering.
//
// __await, __asyncGenerator, __asyncValues and __values are copied verbatim from @google/genai 2.21.0 dist/node/index.mjs:7415-7452 (tslib 2.8.1 as bundled there). The scenario bodies use the exact compiled shapes of the SDK's generators: index.mjs:7729-7754 (Chat.processStreamResponse), 13780-13861 (ApiClient.processStreamResponse), 15789-15812 (Models.generateContentStreamInternal map step) and 15626-15690 (Models.generateContentStream automatic function calling loop). Pi consumes the outermost generator with a native `for await` (packages/ai/src/api/google-generative-ai.ts:106).

function __values(o) {
    var s = typeof Symbol === "function" && Symbol.iterator, m = s && o[s], i = 0;
    if (m) return m.call(o);
    if (o && typeof o.length === "number") return {
        next: function () {
            if (o && i >= o.length) o = void 0;
            return { value: o && o[i++], done: !o };
        }
    };
    throw new TypeError(s ? "Object is not iterable." : "Symbol.iterator is not defined.");
}

function __await(v) {
    return this instanceof __await ? (this.v = v, this) : new __await(v);
}

function __asyncGenerator(thisArg, _arguments, generator) {
    if (!Symbol.asyncIterator) throw new TypeError("Symbol.asyncIterator is not defined.");
    var g = generator.apply(thisArg, _arguments || []), i, q = [];
    return i = Object.create((typeof AsyncIterator === "function" ? AsyncIterator : Object).prototype), verb("next"), verb("throw"), verb("return", awaitReturn), i[Symbol.asyncIterator] = function () { return this; }, i;
    function awaitReturn(f) { return function (v) { return Promise.resolve(v).then(f, reject); }; }
    function verb(n, f) { if (g[n]) { i[n] = function (v) { return new Promise(function (a, b) { q.push([n, v, a, b]) > 1 || resume(n, v); }); }; if (f) i[n] = f(i[n]); } }
    function resume(n, v) { try { step(g[n](v)); } catch (e) { settle(q[0][3], e); } }
    function step(r) { r.value instanceof __await ? Promise.resolve(r.value.v).then(fulfill, reject) : settle(q[0][2], r); }
    function fulfill(value) { resume("next", value); }
    function reject(value) { resume("throw", value); }
    function settle(f, v) { if (f(v), q.shift(), q.length) resume(q[0][0], q[0][1]); }
}

function __asyncValues(o) {
    if (!Symbol.asyncIterator) throw new TypeError("Symbol.asyncIterator is not defined.");
    var m = o[Symbol.asyncIterator], i;
    return m ? m.call(o) : (o = typeof __values === "function" ? __values(o) : o[Symbol.iterator](), i = {}, verb("next"), verb("throw"), verb("return"), i[Symbol.asyncIterator] = function () { return this; }, i);
    function verb(n) { i[n] = o[n] && function (v) { return new Promise(function (resolve, reject) { v = o[n](v), settle(resolve, reject, v.done, v.value); }); }; }
    function settle(resolve, reject, d, v) { Promise.resolve(v).then(function(v) { resolve({ value: v, done: d }); }, reject); }
}

let round = 0;
let events = [];
let timers = new Map();
const log = (s) => events.push(`${s}@${round}`);
// at(n, f) runs f in the loop turn n rounds after the current round.
const at = (n, f) => timers.set(round + n, [...(timers.get(round + n) ?? []), f]);
function loop() {
    round++;
    for (const f of timers.get(round) ?? []) f();
    if (round < 60) Promise.resolve().then(loop);
}
const fmtv = (v) => (v === undefined ? "undefined" : JSON.stringify(v));
const fmtr = (r) => `${fmtv(r.value)}/${r.done}`;
const pendingAt = (n, v) => new Promise((res) => at(n, () => res(v)));
const rejectedAt = (n, message) => new Promise((_, rej) => at(n, () => rej(new Error(message))));
const results = {};
async function run(name, body) {
    round = 0;
    events = [];
    timers = new Map();
    Promise.resolve().then(loop);
    body();
    await new Promise((r) => setTimeout(r, 15));
    results[name] = events;
}

// A generator that awaits, yields, and returns.
function basic() {
    return __asyncGenerator(void 0, void 0, function* () {
        log("b:start");
        const a = yield __await(Promise.resolve(1));
        log("b:a=" + fmtv(a));
        const y = yield yield __await(a + 1);
        log("b:y=" + fmtv(y));
        yield "plain";
        log("b:end");
        return "ret";
    });
}

// A reader whose read() results are scheduled: at 0 is already settled, otherwise settled that many rounds after read().
function reader(specs) {
    const list = [...specs];
    return {
        read() {
            const s = list.shift();
            return s.at === 0 ? Promise.resolve(s.r) : pendingAt(s.at, s.r);
        },
    };
}

// ApiClient.processStreamResponse (index.mjs:13780) without parsing: reader.read() loop, `yield yield __await(x)`, finally releases.
function reading(rd) {
    return __asyncGenerator(void 0, void 0, function* () {
        try {
            while (true) {
                const { done, value } = yield __await(rd.read());
                if (done) break;
                yield yield __await({ json: () => Promise.resolve("j:" + value).then((v) => v), raw: value });
            }
        } finally {
            log("reader:release");
        }
    });
}

// Models.generateContentStreamInternal map step (index.mjs:15789), throwing on chunk `boom`.
function mapping(apiResponse, throwOn) {
    return __asyncGenerator(void 0, void 0, function* () {
        var _a, e_2, _b, _c;
        try {
            for (var _d = true, apiResponse_1 = __asyncValues(apiResponse), apiResponse_1_1; apiResponse_1_1 = yield __await(apiResponse_1.next()), _a = apiResponse_1_1.done, !_a; _d = true) {
                _c = apiResponse_1_1.value;
                _d = false;
                const chunk = _c;
                const resp = yield __await(chunk.json());
                if (resp === throwOn) throw new Error("map:" + resp);
                yield yield __await(resp);
            }
        }
        catch (e_2_1) { e_2 = { error: e_2_1 }; }
        finally {
            try {
                if (!_d && !_a && (_b = apiResponse_1.return)) yield __await(_b.call(apiResponse_1));
            }
            finally { if (e_2) throw e_2.error; }
        }
    });
}

// Models.generateContentStream AFC loop (index.mjs:15626) reduced to the awaits and the for-await/yield shape.
function afc(makeResponse) {
    return __asyncGenerator(void 0, void 0, function* () {
        var _a, e_1, _b, _c;
        const transformedParams = yield __await(Promise.resolve("params"));
        const response = yield __await(makeResponse(transformedParams));
        try {
            for (var _f = true, response_1 = (e_1 = void 0, __asyncValues(response)), response_1_1; response_1_1 = yield __await(response_1.next()), _a = response_1_1.done, !_a; _f = true) {
                _c = response_1_1.value;
                _f = false;
                const chunk = _c;
                yield yield __await(chunk);
            }
        }
        catch (e_1_1) { e_1 = { error: e_1_1 }; }
        finally {
            try {
                if (!_f && !_a && (_b = response_1.return)) yield __await(_b.call(response_1));
            }
            finally { if (e_1) throw e_1.error; }
        }
    });
}

function stack(specs, throwOn) {
    return afc((_p) => Promise.resolve(mapping(reading(reader(specs)), throwOn)).then((g) => g));
}
const specs = () => [
    { at: 0, r: { done: false, value: "c1" } },
    { at: 9, r: { done: false, value: "c2" } },
    { at: 0, r: { done: false, value: "c3" } },
    { at: 0, r: { done: true } },
];

await run("gen-sequential", () => {
    const it = basic();
    const step = (n) => it.next(n).then((r) => { log(`next(${n}) -> ${fmtr(r)}`); if (!r.done) step(n + 1); });
    step(1);
    log("sync-end");
});
// Oracle-only: the same shape as a native async generator. It documents that tslib's tick counts differ from native ones (the reason tslib_async.go exists), and Go asserts the two traces differ.
await run("oracle-only-native-sequential", () => {
    async function* native() {
        log("b:start");
        const a = await Promise.resolve(1);
        log("b:a=" + fmtv(a));
        const y = yield await (a + 1);
        log("b:y=" + fmtv(y));
        yield "plain";
        log("b:end");
        return "ret";
    }
    const it = native();
    const step = (n) => it.next(n).then((r) => { log(`next(${n}) -> ${fmtr(r)}`); if (!r.done) step(n + 1); });
    step(1);
    log("sync-end");
});
await run("gen-queued", () => {
    const it = basic();
    for (let n = 1; n <= 5; n++) it.next(n).then((r) => log(`next(${n}) -> ${fmtr(r)}`));
    log("sync-end");
});
await run("gen-await-pending", () => {
    const it = __asyncGenerator(void 0, void 0, function* () { const a = yield __await(pendingAt(5, "late")); log("b:a=" + a); yield a; });
    it.next().then((r) => log("next -> " + fmtr(r)));
    it.next().then((r) => log("next -> " + fmtr(r)));
    it.next().then((r) => log("next -> " + fmtr(r)));
});
await run("gen-await-rejected-caught", () => {
    const it = __asyncGenerator(void 0, void 0, function* () {
        try { yield __await(rejectedAt(3, "boom")); } catch (e) { log("b:caught " + e.message); }
        yield "after";
    });
    it.next().then((r) => log("next -> " + fmtr(r)));
    it.next().then((r) => log("next -> " + fmtr(r)));
});
await run("gen-await-rejected-uncaught", () => {
    const it = __asyncGenerator(void 0, void 0, function* () { yield __await(rejectedAt(3, "boom")); yield "never"; });
    it.next().then((r) => log("next -> " + fmtr(r)), (e) => log("next rejected " + e.message));
    it.next().then((r) => log("next -> " + fmtr(r)), (e) => log("next rejected " + e.message));
});
await run("gen-body-throws", () => {
    const it = __asyncGenerator(void 0, void 0, function* () { yield 1; throw new Error("bang"); });
    for (let n = 0; n < 3; n++) it.next().then((r) => log("next -> " + fmtr(r)), (e) => log("next rejected " + e.message));
});
await run("gen-return-before-start", () => {
    const it = basic();
    it.return("early").then((r) => log("return -> " + fmtr(r)));
    it.next().then((r) => log("next -> " + fmtr(r)));
});
await run("gen-throw-before-start", () => {
    const it = basic();
    it.throw(new Error("x")).then((r) => log("throw -> " + fmtr(r)), (e) => log("throw rejected " + e.message));
    it.next().then((r) => log("next -> " + fmtr(r)));
});
await run("gen-return-after-done", () => {
    const it = __asyncGenerator(void 0, void 0, function* () { return "r"; });
    it.next().then((r) => { log("next -> " + fmtr(r)); return it.return("again"); }).then((r) => log("return -> " + fmtr(r)));
});
await run("gen-return-suspended-finally-await", () => {
    const it = __asyncGenerator(void 0, void 0, function* () {
        try { yield "one"; yield "two"; } finally { log("b:finally"); yield __await(Promise.resolve("cleanup")); log("b:finally-done"); }
    });
    it.next().then((r) => { log("next -> " + fmtr(r)); return it.return("bye"); }).then((r) => log("return -> " + fmtr(r)));
});
await run("gen-return-while-awaiting-queues", () => {
    const it = __asyncGenerator(void 0, void 0, function* () {
        try { yield __await(pendingAt(4, "slow")); yield "one"; } finally { log("b:finally"); }
    });
    it.next().then((r) => log("next -> " + fmtr(r)));
    it.return("bye").then((r) => log("return -> " + fmtr(r)));
});
await run("gen-throw-suspended", () => {
    const it = __asyncGenerator(void 0, void 0, function* () {
        try { yield "one"; } catch (e) { log("b:caught " + e.message); yield "recovered"; }
    });
    it.next().then((r) => { log("next -> " + fmtr(r)); return it.throw(new Error("injected")); }).then((r) => log("throw -> " + fmtr(r)));
});
await run("values-sync-array", () => {
    const it = __asyncGenerator(void 0, void 0, function* () {
        var _a, e_1, _b, _c;
        try {
            for (var _d = true, src = __asyncValues([1, 2, 3]), src_1; src_1 = yield __await(src.next()), _a = src_1.done, !_a; _d = true) {
                _c = src_1.value;
                _d = false;
                const v = _c;
                if (v === 2) break;
                yield yield __await(v);
            }
        }
        catch (e_1_1) { e_1 = { error: e_1_1 }; }
        finally {
            try { if (!_d && !_a && (_b = src.return)) yield __await(_b.call(src)); }
            finally { if (e_1) throw e_1.error; }
        }
    });
    const step = () => it.next().then((r) => { log("next -> " + fmtr(r)); if (!r.done) step(); });
    step();
});
await run("values-sync-generator-return", () => {
    function* sync() { try { yield 1; yield 2; } finally { log("sync:finally"); } }
    const it = __asyncGenerator(void 0, void 0, function* () {
        var _a, e_1, _b, _c;
        try {
            for (var _d = true, src = __asyncValues(sync()), src_1; src_1 = yield __await(src.next()), _a = src_1.done, !_a; _d = true) {
                _c = src_1.value;
                _d = false;
                const v = _c;
                yield yield __await(v);
                break;
            }
        }
        catch (e_1_1) { e_1 = { error: e_1_1 }; }
        finally {
            try { if (!_d && !_a && (_b = src.return)) yield __await(_b.call(src)); }
            finally { if (e_1) throw e_1.error; }
        }
    });
    const step = () => it.next().then((r) => { log("next -> " + fmtr(r)); if (!r.done) step(); });
    step();
});

// A tslib generator looping over a hand-written async iterator, leaving at value 2 by throw or by break.
function looping(src, mode) {
    return __asyncGenerator(void 0, void 0, function* () {
        var _a, e_1, _b, _c;
        try {
            for (var _d = true, src_1 = __asyncValues(src), src_1_1; src_1_1 = yield __await(src_1.next()), _a = src_1_1.done, !_a; _d = true) {
                _c = src_1_1.value;
                _d = false;
                const v = _c;
                log("v:" + fmtv(v));
                if (v === 2) {
                    if (mode === "throw") throw new Error("body-failed");
                    break;
                }
                yield yield __await(v);
            }
        }
        catch (e_1_1) { e_1 = { error: e_1_1 }; }
        finally {
            try {
                if (!_d && !_a && (_b = src_1.return)) yield __await(_b.call(src_1));
            }
            finally { if (e_1) throw e_1.error; }
        }
    });
}
// drainCapped calls next() until the generator reports done, at most 4 times.
function drainCapped(it) {
    let calls = 0;
    const step = () => {
        if (++calls > 4) return;
        it.next().then((r) => { log("next -> " + fmtr(r)); if (!r.done) step(); }, (e) => { log("next rejected " + e.message); step(); });
    };
    step();
}
// Pi's consumer: a native `for await` (google-generative-ai.ts:106), optionally leaving early.
async function consume(it, breakAt) {
    let n = 0;
    try {
        for await (const c of it) {
            log("consumer:" + fmtv(c));
            if (++n === breakAt) break;
        }
        log("consumer:done");
    } catch (e) {
        log("consumer:caught " + e.message);
    }
}
await run("stack-full", () => { consume(stack(specs())); });
await run("stack-break-after-2", () => { consume(stack(specs()), 2); });
await run("stack-map-throws", () => { consume(stack(specs(), "j:c2")); });
await run("stack-reader-immediate", () => {
    consume(stack([{ at: 0, r: { done: false, value: "a" } }, { at: 0, r: { done: false, value: "b" } }, { at: 0, r: { done: true } }]));
});

function hand(items, { returns, nextRejectsAt, returnRejects } = {}) {
    let i = 0;
    const it = {
        [Symbol.asyncIterator]() { return this; },
        next() {
            log("it:next");
            if (i === nextRejectsAt) return Promise.reject(new Error("next-failed"));
            return Promise.resolve(i < items.length ? { value: items[i++], done: false } : { value: undefined, done: true });
        },
    };
    if (returns !== false) {
        it.return = function () {
            log("it:return");
            return returnRejects ? Promise.reject(new Error("return-failed")) : Promise.resolve({ value: undefined, done: true });
        };
    }
    return it;
}
async function nativeLoop(it, action) {
    try {
        for await (const v of it) {
            log("v:" + fmtv(v));
            if (action) action(v);
        }
        log("loop:done");
    } catch (e) {
        log("loop:caught " + e.message);
    }
}
await run("native-full", () => { nativeLoop(hand([1, 2])); });
// A break completion, as opposed to a throw completion.
async function nativeBreak(it, at) {
    for await (const v of it) {
        log("v:" + fmtv(v));
        if (v === at) break;
    }
    log("loop:after");
}
await run("native-break-completion", () => { nativeBreak(hand([1, 2, 3]), 2); });
await run("native-break-no-return-method", () => { nativeBreak(hand([1, 2, 3], { returns: false }), 2); });
await run("native-break-return-rejects", () => { nativeBreak(hand([1, 2, 3], { returnRejects: true }), 2).catch((e) => log("break rejected " + e.message)); });
await run("native-throw-return-rejects", () => { nativeLoop(hand([1, 2, 3], { returnRejects: true }), (v) => { if (v === 2) throw new Error("body-failed"); }); });
await run("native-throw-in-body", () => { nativeLoop(hand([1, 2, 3]), (v) => { if (v === 2) throw new Error("body-failed"); }); });
await run("native-next-rejects", () => { nativeLoop(hand([1, 2, 3], { nextRejectsAt: 1 })); });

await run("gen-loop-throw-return-rejects", () => { drainCapped(looping(hand([1, 2, 3], { returnRejects: true }), "throw")); });
await run("gen-loop-break-return-rejects", () => { drainCapped(looping(hand([1, 2, 3], { returnRejects: true }), "break")); });
await run("gen-loop-throw-return-ok", () => { drainCapped(looping(hand([1, 2, 3]), "throw")); });
await run("gen-loop-break-return-ok", () => { drainCapped(looping(hand([1, 2, 3]), "break")); });
await run("gen-loop-next-rejects", () => { drainCapped(looping(hand([1, 2, 3], { nextRejectsAt: 1 }), "break")); });

console.log(JSON.stringify(results));
