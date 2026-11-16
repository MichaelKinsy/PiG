// Preloaded with node --import by node_state_order_test.go: every timer callback of this process starts after PIG_TIMER_STALL_MS milliseconds of a blocked event loop,
// as it does in a process the host starves of CPU. The timers that expired meanwhile run in the order of their expiry, and what a callback creates it creates late,
// so a timer set from a callback that ran late expires after a timer set earlier with a longer delay: the inversion that made the probe's old fixed waits report a
// handler that had not run yet. A run with the stall must report what a run without it does.
const stall = Number(process.env.PIG_TIMER_STALL_MS ?? "0");
const setTimeoutOnTime = globalThis.setTimeout;

globalThis.setTimeout = function setTimeout(callback, ...rest) {
  if (typeof callback !== "function") return setTimeoutOnTime.call(this, callback, ...rest);
  return setTimeoutOnTime.call(this, function late(...args) {
    const end = performance.now() + stall;
    while (performance.now() < end);
    return callback.apply(this, args);
  }, ...rest);
};
