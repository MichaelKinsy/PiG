const { CodemodeSandbox } = await import(process.argv[2]);
const mk = () => new CodemodeSandbox({ timeoutMs: 60000, memoryLimitBytes: 16 << 20 });
let t = performance.now(); let s = mk(); await s.execute("return 1"); await s.close(); console.log("node first execute (incl. wasm compile)", (performance.now() - t).toFixed(1), "ms");
t = performance.now(); for (let i = 0; i < 50; i++) { const x = mk(); await x.execute("return 1"); await x.close(); } console.log("node per-execution", ((performance.now() - t) / 50).toFixed(2), "ms");
t = performance.now(); s = mk(); const r = await s.execute("let s=0; for(let i=0;i<50000000;i++) s+=i; return s"); await s.close(); console.log("node cpu-heavy 5e7", ((performance.now() - t)/1000).toFixed(2), "s", JSON.stringify(r).slice(0,80));
