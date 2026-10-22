import { builders, fg, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for an input: its constructor options and value, with the keys and mouse presses delivered to
// the focused node. `changes` of a frame are "<value>@<cursor>" after each step; `events` the callbacks Pi fired.
const node = (extra) => ({ kind: "container", children: [{ kind: "input", id: "i", ...extra }] });
const keys = (...list) => list.map((k) => ({ keys: k }));
const typed = (text) => [...text].map((k) => ({ keys: k }));
const press = (x) => ({ mouse: { type: "press", button: "left", x, y: 0 } });
const focus = "i";
const paste = (text) => ({ keys: `\x1b[200~${text}\x1b[201~` });

export const cases = [
  { name: "empty", args: [{ root: node({}) }] },
  { name: "empty focused", args: [{ root: node({}), focus }] },
  { name: "placeholder", args: [{ root: node({ placeholder: "type here" }) }] },
  { name: "placeholder focused colored", args: [{ root: node({ placeholder: "type here", placeholderColor: "dim" }), focus }] },
  { name: "custom prompt", args: [{ root: node({ prompt: "$ ", value: "ls -la" }), focus }] },
  { name: "value", args: [{ root: node({ value: "hello world" }) }] },
  { name: "value focused", args: [{ root: node({ value: "hello world" }), focus }] },
  { name: "wide characters", args: [{ root: node({ value: "漢字 and 😀 text" }), focus, steps: keys("\x1b[C", "\x1b[C") }] },
  { name: "typing", args: [{ root: node({}), focus, steps: typed("abc") }] },
  { name: "left then insert", args: [{ root: node({}), focus, steps: [...typed("abc"), ...keys("\x1b[D", "X")] }] },
  { name: "backspace and delete", args: [{ root: node({}), focus, steps: [...typed("abcd"), ...keys("\x1b[D", "\x1b[D", "\x7f", "\x1b[3~")] }] },
  { name: "home and end", args: [{ root: node({}), focus, steps: [...typed("abc"), ...keys("\x01", "Z", "\x05", "Y")] }] },
  { name: "kill word and yank", args: [{ root: node({}), focus, steps: [...typed("one two"), ...keys("\x17", "\x19", "\x19")] }] },
  { name: "kill to end", args: [{ root: node({}), focus, steps: [...typed("abcdef"), ...keys("\x01", "\x1b[C", "\x0b")] }] },
  { name: "paste scrolls", args: [{ root: node({}), focus, steps: [paste("0123456789 abcdefghijklmnopqrstuvwxyz 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ")] }] },
  { name: "submit", args: [{ root: node({}), focus, steps: [...typed("ab"), ...keys("\r")] }] },
  { name: "submit newline key", args: [{ root: node({ value: "preset" }), focus, steps: keys("\n") }] },
  { name: "escape", args: [{ root: node({ value: "keep" }), focus, steps: keys("\x1b") }] },
  { name: "click moves the cursor", args: [{ root: node({ value: "hello world" }), focus, steps: [press(7), ...keys("X")] }] },
  { name: "right click ignored", args: [{ root: node({ value: "hello world" }), focus, steps: [{ mouse: { type: "press", button: "right", x: 7, y: 0 } }, ...keys("X")] }] },
];

const registry = {
  ...builders,
  input: (lib, n) => {
    const options = {};
    if (n.prompt !== undefined) options.prompt = n.prompt;
    if (n.placeholder !== undefined) options.placeholder = n.placeholder;
    if (n.placeholderColor !== undefined) options.placeholderStyle = fg(n.placeholderColor);
    const input = new lib.tui.Input(options);
    if (n.value) input.setValue(n.value);
    input.focused = lib.focus === n.id;
    input.onSubmit = (value) => lib.events.push(`submit:${value}`);
    input.onEscape = () => lib.events.push(`escape:${input.getValue()}`);
    lib.ids[n.id] = input;
    lib.probe = () => `${input.getValue()}@${input.cursor}`;
    return input;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
