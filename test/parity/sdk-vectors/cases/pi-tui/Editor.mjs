import { builders, fakeTui, fg, renderCase } from "../../../../../automation/gen/sdk-vector-lib/kit-build.mjs";

// The wire node an SDK sends for an editor: Editor's constructor options and setters as fields, with the keys and mouse
// events delivered to the focused node. `rows` is the terminal's height, which sets the editor's visible lines
// (max(5, floor(rows * 0.3))). `changes` of a frame are "<text>@<line>,<col>" after each step; `events` the callbacks Pi
// fired. A `settle` case waits, after each step, for the autocomplete query it started.
const node = (extra) => ({ kind: "container", children: [{ kind: "editor", id: "e", ...extra }] });
const keys = (...list) => list.map((k) => ({ keys: k }));
const typed = (text) => [...text].map((k) => ({ keys: k }));
const click = (x, y) => ({ mouse: { type: "click", button: "left", x, y } });
const paste = (text) => ({ keys: `\x1b[200~${text}\x1b[201~` });
const focus = "e";
const ENTER = "\r";
const SHIFT_ENTER = "\x1b[13;2u";
const UP = "\x1b[A";
const DOWN = "\x1b[B";
const LEFT = "\x1b[D";
const RIGHT = "\x1b[C";
const lines = (n, prefix = "line") => Array.from({ length: n }, (_, i) => `${prefix} ${i + 1}`).join("\n");
const commands = [
  { name: "help", description: "Show help" },
  { name: "model", description: "Switch the model", argumentHint: "<name>" },
  { name: "quit", description: "Exit" },
  { name: "session", description: "Session info" },
  { name: "settings", description: "Open settings" },
  { name: "share" },
  { name: "skills", description: "List skills" },
  { name: "compact", description: "Compact the context" },
];
const autocomplete = { commands };
const slash = (extra, steps) => ({ root: node({ autocomplete, ...extra }), focus, steps, settle: true });

export const cases = [
  { name: "empty", args: [{ root: node({}) }] },
  { name: "empty focused", args: [{ root: node({}), focus }] },
  { name: "value", args: [{ root: node({ value: "hello world" }) }] },
  { name: "multiline value focused", args: [{ root: node({ value: "one\ntwo\nthree" }), focus }] },
  { name: "wrapped line", args: [{ root: node({ value: "The quick brown fox jumps over the lazy dog and keeps running through the field" }), focus }] },
  { name: "wide characters", args: [{ root: node({ value: "漢字 and 😀 text" }), focus, steps: keys(LEFT, LEFT, LEFT) }] },
  { name: "typing", args: [{ root: node({}), focus, steps: typed("abc") }] },
  { name: "newline", args: [{ root: node({}), focus, steps: [...typed("ab"), ...keys(SHIFT_ENTER), ...typed("cd")] }] },
  { name: "cursor movement", args: [{ root: node({ value: "one\ntwo\nthree" }), focus, steps: keys(UP, LEFT, "\x01", "X", "\x05", "Y") }] },
  { name: "backspace and delete", args: [{ root: node({}), focus, steps: [...typed("abcd"), ...keys(LEFT, LEFT, "\x7f", "\x1b[3~")] }] },
  { name: "kill word and yank", args: [{ root: node({}), focus, steps: [...typed("one two"), ...keys("\x17", "\x19", "\x19")] }] },
  { name: "undo", args: [{ root: node({}), focus, steps: [...typed("abc"), ...keys("\x1f")] }] },
  { name: "submit", args: [{ root: node({}), focus, steps: [...typed("hi"), ...keys(ENTER)] }] },
  { name: "submit multiline", args: [{ root: node({ value: "one\ntwo" }), focus, steps: keys(ENTER) }] },
  { name: "submit disabled", args: [{ root: node({ disableSubmit: true, value: "keep" }), focus, steps: keys(ENTER) }] },
  { name: "padding", args: [{ root: node({ paddingX: 2, value: "padded text" }), focus }] },
  { name: "padding wrapped", args: [{ root: node({ paddingX: 3, value: "The quick brown fox jumps over the lazy dog and keeps running" }), focus }] },
  { name: "border color", args: [{ root: node({ borderColor: "accent", value: "colored" }), focus }] },
  { name: "history", args: [{ root: node({ history: ["first", "second", "third"] }), focus, steps: keys(UP, UP, DOWN, DOWN) }] },
  { name: "history then edit", args: [{ root: node({ history: ["first", "second"] }), focus, steps: [...keys(UP), ...typed("!"), ...keys(UP)] }] },
  { name: "scrolls past the visible lines", args: [{ root: node({ value: lines(12) }), focus, steps: keys(UP, UP, UP, UP, UP, UP, UP, UP, UP, UP, UP) }] },
  { name: "scroll indicator below", args: [{ root: node({ value: lines(12) }), focus, steps: keys("\x1b[1;5H") }] },
  { name: "more rows show more lines", args: [{ root: node({ value: lines(12) }), focus, rows: 60 }] },
  { name: "few rows show five lines", args: [{ root: node({ value: lines(12) }), focus, rows: 10 }] },
  { name: "page up and down", args: [{ root: node({ value: lines(30) }), focus, rows: 40, steps: keys("\x1b[5~", "\x1b[5~", "\x1b[6~") }] },
  { name: "paste", args: [{ root: node({}), focus, steps: [paste("pasted text")] }] },
  { name: "paste large", args: [{ root: node({}), focus, steps: [paste(lines(14, "pasted"))] }] },
  { name: "click moves the cursor", args: [{ root: node({ value: "hello\nworld" }), focus, steps: [click(3, 2), ...keys("X")] }] },
  { name: "click on the border", args: [{ root: node({ value: "hello\nworld" }), focus, steps: [click(3, 0), ...keys("X")] }] },
  { name: "right click ignored", args: [{ root: node({ value: "hello" }), focus, steps: [{ mouse: { type: "click", button: "right", x: 2, y: 1 } }, ...keys("X")] }] },
  { name: "slash opens the menu", args: [slash({}, [...typed("/")])] },
  { name: "slash narrows", args: [slash({}, [...typed("/s"), ...typed("e")])] },
  { name: "slash no match", args: [slash({}, [...typed("/zzz")])] },
  { name: "slash tab accepts", args: [slash({}, [...typed("/m"), ...keys("\t")])] },
  { name: "slash arrow then tab", args: [slash({}, [...typed("/s"), ...keys(DOWN), ...keys("\t")])] },
  { name: "slash enter accepts", args: [slash({}, [...typed("/s"), ...keys(DOWN), ...keys(ENTER)])] },
  { name: "slash escape closes", args: [slash({}, [...typed("/"), ...keys("\x1b")])] },
  { name: "slash max visible 3", args: [slash({ autocompleteMaxVisible: 3 }, [...typed("/")])] },
  { name: "slash with padding", args: [slash({ paddingX: 2 }, [...typed("/")])] },
  { name: "slash click selects", args: [slash({}, [...typed("/s"), click(3, 4)])] },
  { name: "text after the slash opens nothing", args: [slash({}, [...typed("hello /")])] },
  { name: "autocomplete menu does not follow disabled submit", args: [slash({ disableSubmit: true }, [...typed("/q"), ...keys(ENTER)])] },
];

const registry = {
  ...builders,
  editor: (lib, n) => {
    const options = {};
    if (n.paddingX !== undefined) options.paddingX = n.paddingX;
    if (n.autocompleteMaxVisible !== undefined) options.autocompleteMaxVisible = n.autocompleteMaxVisible;
    // theme.ts getEditorTheme(), which the package does not export.
    const editor = new lib.tui.Editor(fakeTui(lib.rows), { borderColor: fg("borderMuted"), selectList: lib.pi.getSelectListTheme() }, options);
    if (n.borderColor) editor.borderColor = fg(n.borderColor);
    if (n.disableSubmit) editor.disableSubmit = true;
    for (const entry of n.history ?? []) editor.addToHistory(entry);
    if (n.autocomplete) editor.setAutocompleteProvider(new lib.tui.CombinedAutocompleteProvider(n.autocomplete.commands ?? [], n.autocomplete.basePath ?? process.cwd()));
    if (n.value) editor.setText(n.value);
    editor.focused = lib.focus === n.id;
    editor.onSubmit = (text) => lib.events.push(`submit:${text}`);
    lib.ids[n.id] = editor;
    lib.probe = () => {
      const cursor = editor.getCursor();
      return `${editor.getText()}@${cursor.line},${cursor.col}`;
    };
    return editor;
  },
};

export async function call(_fn, spec) {
  return renderCase(this, spec, registry);
}
