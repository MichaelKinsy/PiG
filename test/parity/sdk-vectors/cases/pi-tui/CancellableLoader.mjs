import { builders, fakeTui, fg, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a cancellable loader: its constructor arguments, the spinner frame it starts from, and
// the tui.select.cancel key delivered to the focused node.
const node = (extra, base = { message: "Working" }) => ({ kind: "container", children: [{ kind: "cancellable-loader", id: "c", ...base, ...extra }] });
const esc = { keys: "\x1b" };

export const cases = [
  { name: "default", args: [{ root: node({}) }] },
  { name: "default message", args: [{ root: node({}, {}) }] },
  { name: "colors", args: [{ root: node({ spinnerColor: "accent", messageColor: "muted" }) }] },
  { name: "frame 3", args: [{ root: node({ frame: 3 }) }] },
  { name: "indicator frames", args: [{ root: node({ indicator: { frames: ["*", "+", "x"], intervalMs: 50 }, frame: 1 }) }] },
  { name: "hidden indicator", args: [{ root: node({ indicator: { frames: [] } }) }] },
  { name: "wide message", args: [{ root: node({ message: "A very long message that wraps around the narrow widths of the table", spinnerColor: "accent" }) }] },
  { name: "escape aborts", args: [{ root: node({}), focus: "c", steps: [esc] }] },
  { name: "escape twice aborts twice", args: [{ root: node({}), focus: "c", steps: [esc, esc] }] },
  { name: "other keys are ignored", args: [{ root: node({}), focus: "c", steps: [{ keys: "x" }, { keys: "\r" }] }] },
];

const registry = {
  ...builders,
  "cancellable-loader": (lib, n) => {
    const indicator = n.indicator ? { ...(n.indicator.frames !== undefined ? { frames: n.indicator.frames } : {}), ...(n.indicator.intervalMs ? { intervalMs: n.indicator.intervalMs } : {}) } : undefined;
    const loader = new lib.tui.CancellableLoader(fakeTui(), fg(n.spinnerColor), fg(n.messageColor), n.message, indicator);
    loader.stop();
    if (n.frame !== undefined && loader.frames.length > 0) {
      loader.currentFrame = n.frame % loader.frames.length;
      loader.updateDisplay();
    }
    loader.onAbort = () => lib.events.push("abort:");
    lib.ids[n.id] = loader;
    lib.timers.push(() => loader.stop());
    return loader;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
