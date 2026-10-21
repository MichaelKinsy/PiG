import { builders, fakeTui, piTheme, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for a bordered loader: cancellable (the default) or not, the spinner frame it starts from,
// and the tui.select.cancel key delivered to the focused node.
const node = (extra, base = { message: "Fetching models" }) => ({ kind: "container", children: [{ kind: "bordered-loader", id: "b", ...base, ...extra }] });
const esc = { keys: "\x1b" };

export const cases = [
  { name: "cancellable", args: [{ root: node({}) }] },
  { name: "not cancellable", args: [{ root: node({ cancellable: false }) }] },
  { name: "default message", args: [{ root: node({}, {}) }] },
  { name: "frame 4", args: [{ root: node({ frame: 4 }) }] },
  { name: "wide message", args: [{ root: node({ message: "Fetching the models of every configured provider, one after another" }) }] },
  { name: "escape aborts", args: [{ root: node({}), focus: "b", steps: [esc] }] },
  { name: "escape twice aborts twice", args: [{ root: node({}), focus: "b", steps: [esc, esc] }] },
  { name: "escape ignored when not cancellable", args: [{ root: node({ cancellable: false }), focus: "b", steps: [esc] }] },
  { name: "other keys are ignored", args: [{ root: node({}), focus: "b", steps: [{ keys: "x" }] }] },
];

const registry = {
  ...builders,
  "bordered-loader": (lib, n) => {
    const loader = new lib.pi.BorderedLoader(fakeTui(), piTheme(), n.message, n.cancellable === undefined ? undefined : { cancellable: n.cancellable });
    const inner = loader.loader;
    inner.stop();
    if (n.frame !== undefined && inner.frames.length > 0) {
      inner.currentFrame = n.frame % inner.frames.length;
      inner.updateDisplay();
    }
    loader.onAbort = () => lib.events.push("abort:");
    lib.ids[n.id] = loader;
    lib.timers.push(() => loader.dispose());
    return loader;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
