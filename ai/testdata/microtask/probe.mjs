// Microtask tick-order probe for the ai package's Promise, await and async generator model (ai/js_promise.go, ai/js_async_generator.go).
// Run `node ai/testdata/microtask/probe.mjs` from the repository root; stdout is ai/testdata/microtask/ticks.json.
// Each scenario runs alone in a fresh macrotask. A ticker chain (t0, t1, ...) is queued first, so the position of a scenario's own entries between ticker entries is its exact microtask depth.
// ai/js_microtask_test.go replays every scenario against the Go model and must produce identical entry lists. Scenario names and bodies mirror one to one.
// Node 24.19.0 (.node-version) and 26.7.0 print identical output.

let logs = [];
const log = (value) => logs.push(String(value));

function ticker(count = 16) {
  let chain = Promise.resolve();
  for (let i = 0; i < count; i++) chain = chain.then(() => log(`t${i}`));
}

// atTick runs fn after `count` reactions, the same depth as ticker entry t(count-1) plus one.
function atTick(count, fn) {
  let chain = Promise.resolve();
  for (let i = 0; i < count; i++) chain = chain.then(() => {});
  chain.then(fn);
}

const thenable = (value, tag = 'thenable') => ({ then(resolve) { log(`${tag} then called`); resolve(value); } });
const lateThenable = (value, count) => ({ then(resolve) { log('late then called'); atTick(count, () => resolve(value)); } });
const throwingThenable = () => ({ then() { throw new Error('then threw'); } });
const pendingAt = (value, count) => new Promise((resolve) => atTick(count, () => resolve(value)));
const showResult = (result) => `${result.value === undefined ? 'undefined' : result.value}:${result.done}`;
const logResult = (tag) => (result) => log(`${tag} ${showResult(result)}`);
const logError = (tag) => (error) => log(`${tag} caught ${error.message}`);

const scenarios = [];
const scenario = (name, fn) => scenarios.push([name, fn]);

// Primitive 1: Promise, resolve, then, thenable adoption.
scenario('then-settled', () => {
  Promise.resolve(1).then((v) => log(`a ${v}`));
  log('sync');
});
scenario('then-chain', () => {
  Promise.resolve(1).then((v) => { log(`a ${v}`); return v + 1; }).then((v) => { log(`b ${v}`); return v + 1; }).then((v) => log(`c ${v}`));
});
scenario('then-returns-promise', () => {
  Promise.resolve().then(() => Promise.resolve(1)).then((v) => log(`after ${v}`));
});
scenario('then-returns-pending-promise', () => {
  Promise.resolve().then(() => pendingAt(2, 3)).then((v) => log(`after ${v}`));
});
scenario('then-returns-thenable', () => {
  Promise.resolve().then(() => thenable(3)).then((v) => log(`after ${v}`));
});
scenario('then-returns-late-thenable', () => {
  Promise.resolve().then(() => lateThenable(4, 2)).then((v) => log(`after ${v}`));
});
scenario('resolve-with-promise', () => {
  new Promise((resolve) => resolve(Promise.resolve(1))).then((v) => log(`after ${v}`));
});
scenario('resolve-with-thenable', () => {
  new Promise((resolve) => resolve(thenable(2))).then((v) => log(`after ${v}`));
  log('sync');
});
scenario('resolve-with-thenable-of-thenable', () => {
  new Promise((resolve) => resolve(thenable(thenable(5, 'inner'), 'outer'))).then((v) => log(`after ${v}`));
});
scenario('resolve-with-promise-then-reject-ignored', () => {
  new Promise((resolve, reject) => { resolve(Promise.resolve(1)); reject(new Error('ignored')); resolve(2); }).then((v) => log(`after ${v}`), logError('rejected'));
});
scenario('resolve-with-pending-promise', () => {
  new Promise((resolve) => resolve(pendingAt(6, 2))).then((v) => log(`after ${v}`));
});
scenario('resolve-first-wins', () => {
  new Promise((resolve) => { resolve(1); resolve(2); }).then((v) => log(`after ${v}`));
});
scenario('resolve-self-cycle', () => {
  let resolve;
  const promise = new Promise((r) => { resolve = r; });
  resolve(promise);
  promise.then(() => log('never'), (e) => log(`caught ${e.constructor.name}: ${e.message}`));
});
scenario('reject-passthrough', () => {
  Promise.reject(new Error('boom')).then(() => log('never')).then(() => log('never')).catch(logError('h'));
});
scenario('reject-handled-then-continues', () => {
  Promise.reject(new Error('boom')).then(() => log('never'), (e) => { log(`r ${e.message}`); return 1; }).then((v) => log(`after ${v}`));
});
scenario('handler-throws', () => {
  Promise.resolve(1).then(() => { throw new Error('thrown'); }).then(() => log('never')).catch(logError('h'));
});
scenario('thenable-then-throws', () => {
  Promise.resolve().then(() => throwingThenable()).then(() => log('never'), logError('h'));
});
scenario('thenable-throws-after-resolve', () => {
  const t = { then(resolve) { resolve(8); throw new Error('ignored'); } };
  new Promise((resolve) => resolve(t)).then((v) => log(`after ${v}`), logError('h'));
});
scenario('reactions-in-registration-order', () => {
  const p = pendingAt(1, 3);
  p.then((v) => log(`a ${v}`));
  p.then((v) => log(`b ${v}`));
  Promise.resolve().then(() => p.then((v) => log(`c ${v}`)));
});
scenario('then-on-settled-inside-reaction', () => {
  Promise.resolve().then(() => {
    log('outer');
    Promise.resolve().then(() => log('inner'));
  }).then(() => log('outer-next'));
});

// Primitive 2: await.
scenario('await-plain', () => {
  (async () => { log('start'); await 1; log('after'); })();
  log('sync');
});
scenario('await-settled-promise', () => {
  (async () => { const v = await Promise.resolve(2); log(`after ${v}`); })();
});
scenario('await-pending-promise', () => {
  (async () => { const v = await pendingAt(3, 3); log(`after ${v}`); })();
});
scenario('await-thenable', () => {
  (async () => { const v = await thenable(4); log(`after ${v}`); })();
});
scenario('await-late-thenable', () => {
  (async () => { const v = await lateThenable(4, 2); log(`after ${v}`); })();
});
scenario('await-rejected-caught', () => {
  (async () => { try { await Promise.reject(new Error('x')); log('never'); } catch (e) { log(`caught ${e.message}`); } })();
});
scenario('await-thenable-throws', () => {
  (async () => { try { await throwingThenable(); log('never'); } catch (e) { log(`caught ${e.message}`); } })();
});
scenario('await-sequence', () => {
  (async () => { await 1; log('one'); await Promise.resolve(); log('two'); await thenable(0); log('three'); })();
});
scenario('await-two-functions-interleave', () => {
  (async () => { await 1; log('a1'); await 1; log('a2'); await 1; log('a3'); })();
  (async () => { await 1; log('b1'); await Promise.resolve(); log('b2'); })();
});
scenario('async-return-value', () => {
  (async () => 1)().then((v) => log(`after ${v}`));
});
scenario('async-return-promise', () => {
  (async () => Promise.resolve(1))().then((v) => log(`after ${v}`));
});
scenario('async-return-await-promise', () => {
  (async () => await Promise.resolve(1))().then((v) => log(`after ${v}`));
});
scenario('async-return-pending-promise', () => {
  (async () => pendingAt(1, 2))().then((v) => log(`after ${v}`));
});
scenario('async-return-thenable', () => {
  (async () => thenable(1))().then((v) => log(`after ${v}`));
});
scenario('async-throw', () => {
  (async () => { throw new Error('sync throw'); })().catch(logError('h'));
});
scenario('async-throw-after-await', () => {
  (async () => { await 1; throw new Error('late throw'); })().catch(logError('h'));
});
scenario('async-await-async', () => {
  const inner = async () => { await 1; return 5; };
  (async () => { const v = await inner(); log(`outer ${v}`); })();
});
scenario('async-return-async', () => {
  const inner = async () => { await 1; return 5; };
  (async () => inner())().then((v) => log(`after ${v}`));
});
scenario('async-await-rejected-async', () => {
  const inner = async () => { await 1; throw new Error('inner'); };
  (async () => { try { await inner(); } catch (e) { log(`caught ${e.message}`); } })();
});

// Primitive 3: native async generators.
scenario('gen-next-sequential', () => {
  async function* g() { log('body start'); yield 1; log('body 2'); yield 2; log('body end'); }
  const it = g();
  (async () => {
    logResult('r1')(await it.next());
    logResult('r2')(await it.next());
    logResult('r3')(await it.next());
    logResult('r4')(await it.next());
  })();
  log('sync');
});
scenario('gen-next-queued', () => {
  async function* g() { log('body start'); yield 1; log('body 2'); yield 2; log('body end'); }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.next().then(logResult('r3'));
  it.next().then(logResult('r4'));
  log('sync');
});
scenario('gen-yield-promise', () => {
  async function* g() { const sent = yield Promise.resolve(5); log(`sent ${sent}`); }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-yield-pending-promise', () => {
  async function* g() { yield pendingAt(5, 3); log('after yield'); }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-yield-thenable', () => {
  async function* g() { yield thenable(9); }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-yield-rejected-caught', () => {
  async function* g() {
    try { yield Promise.reject(new Error('bad')); log('never'); } catch (e) { log(`body caught ${e.message}`); }
    yield 2;
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-yield-rejected-uncaught', () => {
  async function* g() { yield Promise.reject(new Error('bad')); log('never'); }
  const it = g();
  it.next().then(logResult('r1'), logError('r1'));
  it.next().then(logResult('r2'), logError('r2'));
});
scenario('gen-return-value', () => {
  async function* g() { yield 1; return 7; }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-return-promise', () => {
  async function* g() { return Promise.resolve(7); }
  const it = g();
  it.next().then(logResult('r1'));
});
scenario('gen-return-pending-promise', () => {
  async function* g() { return pendingAt(7, 3); }
  const it = g();
  it.next().then(logResult('r1'));
});
scenario('gen-return-rejected', () => {
  async function* g() { return Promise.reject(new Error('rej')); }
  const it = g();
  it.next().then(logResult('r1'), logError('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-empty', () => {
  async function* g() {}
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-body-throws', () => {
  async function* g() { yield 1; throw new Error('body threw'); }
  const it = g();
  it.next().then(logResult('r1'), logError('r1'));
  it.next().then(logResult('r2'), logError('r2'));
  it.next().then(logResult('r3'), logError('r3'));
});
scenario('gen-return-at-start', () => {
  async function* g() { log('never'); yield 1; }
  const it = g();
  it.return(9).then(logResult('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-return-at-start-promise', () => {
  async function* g() { yield 1; }
  const it = g();
  it.return(Promise.resolve(3)).then(logResult('r1'));
});
scenario('gen-return-at-start-pending-promise', () => {
  async function* g() { yield 1; }
  const it = g();
  it.return(pendingAt(3, 3)).then(logResult('r1'));
});
scenario('gen-return-at-start-rejected', () => {
  async function* g() { yield 1; }
  const it = g();
  it.return(Promise.reject(new Error('rej'))).then(logResult('r1'), logError('r1'));
});
scenario('gen-return-at-start-thenable', () => {
  async function* g() { yield 1; }
  const it = g();
  it.return(thenable(4)).then(logResult('r1'));
});
scenario('gen-return-completed', () => {
  async function* g() {}
  const it = g();
  it.next().then(logResult('r1'));
  it.return(9).then(logResult('r2'));
});
scenario('gen-return-suspended-yield', () => {
  async function* g() {
    try { yield 1; log('never'); } finally { log('finally'); }
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.return(8).then(logResult('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-return-suspended-yield-promise', () => {
  async function* g() { yield 1; }
  const it = g();
  it.next().then(logResult('r1'));
  it.return(Promise.resolve(8)).then(logResult('r2'));
});
scenario('gen-return-suspended-yield-rejected', () => {
  async function* g() { try { yield 1; } catch (e) { log(`body caught ${e.message}`); yield 4; } }
  const it = g();
  it.next().then(logResult('r1'));
  it.return(Promise.reject(new Error('rej'))).then(logResult('r2'), logError('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-return-finally-awaits', () => {
  async function* g() {
    try { yield 1; } finally { log('finally start'); await 1; log('finally end'); }
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.return(8).then(logResult('r2'));
});
scenario('gen-return-finally-yields', () => {
  async function* g() {
    try { yield 1; } finally { log('finally start'); yield 'f'.length; log('finally end'); }
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.return(8).then(logResult('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-throw-at-start', () => {
  async function* g() { log('never'); yield 1; }
  const it = g();
  it.throw(new Error('t')).then(logResult('r1'), logError('r1'));
  it.next().then(logResult('r2'));
});
scenario('gen-throw-suspended-yield-caught', () => {
  async function* g() {
    try { yield 1; } catch (e) { log(`body caught ${e.message}`); yield 2; }
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.throw(new Error('t')).then(logResult('r2'), logError('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-throw-suspended-yield-uncaught', () => {
  async function* g() { yield 1; }
  const it = g();
  it.next().then(logResult('r1'));
  it.throw(new Error('t')).then(logResult('r2'), logError('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-throw-completed', () => {
  async function* g() {}
  const it = g();
  it.next().then(logResult('r1'));
  it.throw(new Error('t')).then(logResult('r2'), logError('r2'));
});
scenario('gen-await-in-body-queued', () => {
  async function* g() {
    log('body start');
    await 1;
    log('body after await');
    yield 1;
    await pendingAt(0, 2);
    log('body after pending');
    yield 2;
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-queued-return-while-executing', () => {
  async function* g() {
    try { await 1; yield 1; log('after yield'); } finally { log('finally'); }
  }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.return(4).then(logResult('r3'));
  it.next().then(logResult('r4'));
});
scenario('gen-queued-throw-while-executing', () => {
  async function* g() { await 1; yield 1; yield 2; }
  const it = g();
  it.next().then(logResult('r1'));
  it.throw(new Error('t')).then(logResult('r2'), logError('r2'));
  it.next().then(logResult('r3'));
});
scenario('gen-next-during-awaiting-return', () => {
  async function* g() { yield 1; }
  const it = g();
  it.return(pendingAt(3, 3)).then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.return(5).then(logResult('r3'));
  it.throw(new Error('t')).then(logResult('r4'), logError('r4'));
  it.next().then(logResult('r5'));
});
scenario('gen-drain-queue-after-body-completes', () => {
  async function* g() { await 1; return 5; }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.throw(new Error('t')).then(logResult('r3'), logError('r3'));
  it.next().then(logResult('r4'));
  it.return(6).then(logResult('r5'));
  it.next().then(logResult('r6'));
});
scenario('gen-yields-continue-without-suspend', () => {
  async function* g() { log('a'); yield 1; log('b'); yield 2; log('c'); yield 3; log('d'); }
  const it = g();
  it.next().then(logResult('r1'));
  it.next().then(logResult('r2'));
  it.next().then(logResult('r3'));
  it.next().then(logResult('r4'));
});
scenario('gen-forawait-consumer', () => {
  async function* g() { yield 1; yield 2; yield 3; }
  (async () => {
    const it = g();
    for (;;) {
      const result = await it.next();
      if (result.done) break;
      log(`item ${result.value}`);
    }
    log('loop done');
  })();
});
scenario('gen-chain', () => {
  async function* inner() { yield 1; yield 2; }
  async function* outer() {
    const it = inner();
    for (;;) {
      const result = await it.next();
      if (result.done) return;
      yield result.value * 10;
    }
  }
  (async () => {
    const it = outer();
    for (;;) {
      const result = await it.next();
      if (result.done) break;
      log(`item ${result.value}`);
    }
    log('loop done');
  })();
});

const output = {};
for (const [name, fn] of scenarios) {
  if (Object.hasOwn(output, name)) throw new Error(`duplicate scenario ${name}`);
  logs = [];
  ticker();
  fn();
  await new Promise((resolve) => setImmediate(resolve));
  output[name] = logs;
}
process.stdout.write(`${JSON.stringify(output, null, 1)}\n`);
