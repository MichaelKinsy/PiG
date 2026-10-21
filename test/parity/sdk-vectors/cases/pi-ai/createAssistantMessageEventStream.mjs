// The stream times its final message: durationMs is added unless the message has one or its timestamp predates the stream. Each case names how the final message is
// delivered and what it carries; the result says whether durationMs ended up on it, whether a preset one was kept, and whether a message that arrives after the stream is done was timed.
export const cases = [
  { name: "done now none", args: [{"kind": "done", "timestamp": "now", "preset": null}] },
  { name: "done now 0", args: [{"kind": "done", "timestamp": "now", "preset": 0}] },
  { name: "done now 7", args: [{"kind": "done", "timestamp": "now", "preset": 7}] },
  { name: "done old none", args: [{"kind": "done", "timestamp": "old", "preset": null}] },
  { name: "done old 0", args: [{"kind": "done", "timestamp": "old", "preset": 0}] },
  { name: "done old 7", args: [{"kind": "done", "timestamp": "old", "preset": 7}] },
  { name: "done absent none", args: [{"kind": "done", "timestamp": "absent", "preset": null}] },
  { name: "done absent 0", args: [{"kind": "done", "timestamp": "absent", "preset": 0}] },
  { name: "done absent 7", args: [{"kind": "done", "timestamp": "absent", "preset": 7}] },
  { name: "error now none", args: [{"kind": "error", "timestamp": "now", "preset": null}] },
  { name: "error now 0", args: [{"kind": "error", "timestamp": "now", "preset": 0}] },
  { name: "error now 7", args: [{"kind": "error", "timestamp": "now", "preset": 7}] },
  { name: "error old none", args: [{"kind": "error", "timestamp": "old", "preset": null}] },
  { name: "error old 0", args: [{"kind": "error", "timestamp": "old", "preset": 0}] },
  { name: "error old 7", args: [{"kind": "error", "timestamp": "old", "preset": 7}] },
  { name: "error absent none", args: [{"kind": "error", "timestamp": "absent", "preset": null}] },
  { name: "error absent 0", args: [{"kind": "error", "timestamp": "absent", "preset": 0}] },
  { name: "error absent 7", args: [{"kind": "error", "timestamp": "absent", "preset": 7}] },
  { name: "end now none", args: [{"kind": "end", "timestamp": "now", "preset": null}] },
  { name: "end now 0", args: [{"kind": "end", "timestamp": "now", "preset": 0}] },
  { name: "end now 7", args: [{"kind": "end", "timestamp": "now", "preset": 7}] },
  { name: "end old none", args: [{"kind": "end", "timestamp": "old", "preset": null}] },
  { name: "end old 0", args: [{"kind": "end", "timestamp": "old", "preset": 0}] },
  { name: "end old 7", args: [{"kind": "end", "timestamp": "old", "preset": 7}] },
  { name: "end absent none", args: [{"kind": "end", "timestamp": "absent", "preset": null}] },
  { name: "end absent 0", args: [{"kind": "end", "timestamp": "absent", "preset": 0}] },
  { name: "end absent 7", args: [{"kind": "end", "timestamp": "absent", "preset": 7}] },
  { name: "doneThenEnd now none", args: [{"kind": "doneThenEnd", "timestamp": "now", "preset": null}] },
  { name: "doneThenEnd now 0", args: [{"kind": "doneThenEnd", "timestamp": "now", "preset": 0}] },
  { name: "doneThenEnd now 7", args: [{"kind": "doneThenEnd", "timestamp": "now", "preset": 7}] },
  { name: "doneThenEnd old none", args: [{"kind": "doneThenEnd", "timestamp": "old", "preset": null}] },
  { name: "doneThenEnd old 0", args: [{"kind": "doneThenEnd", "timestamp": "old", "preset": 0}] },
  { name: "doneThenEnd old 7", args: [{"kind": "doneThenEnd", "timestamp": "old", "preset": 7}] },
  { name: "doneThenEnd absent none", args: [{"kind": "doneThenEnd", "timestamp": "absent", "preset": null}] },
  { name: "doneThenEnd absent 0", args: [{"kind": "doneThenEnd", "timestamp": "absent", "preset": 0}] },
  { name: "doneThenEnd absent 7", args: [{"kind": "doneThenEnd", "timestamp": "absent", "preset": 7}] },
];
export const call = (fn, { kind, timestamp, preset }) => {
  const stream = fn();
  const message = (isLater) => ({ role: "assistant", content: [], stopReason: kind === "error" ? "error" : "stop", ...(isLater || timestamp === "now" ? { timestamp: Date.now() } : timestamp === "old" ? { timestamp: 1 } : {}), ...(preset !== null && !isLater ? { durationMs: preset } : {}) });
  const final = message(false);
  const later = message(true);
  if (kind === "done") stream.push({ type: "done", reason: "stop", message: final });
  else if (kind === "error") stream.push({ type: "error", reason: "error", error: final });
  else if (kind === "end") stream.end(final);
  else { stream.push({ type: "done", reason: "stop", message: final }); stream.end(later); }
  return { present: final.durationMs !== undefined, kept: preset !== null && final.durationMs === preset, laterTimed: later.durationMs !== undefined };
};
