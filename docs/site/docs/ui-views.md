# Extension views

An extension view is a GUI that an extension describes once, as data. The extension mounts the view with `ctx.ui.mountView`, and every client attached to the session draws it with its own controls: the terminal draws rows, and a web page draws HTML. A view is a tree of nodes (text, lists, inputs, buttons, a grid), the intents a user can raise, and optionally alternatives for different clients, key bindings and a drawing. A view fills a slot such as a panel, a widget row or a dialog. No client runs extension code.

> **Differs from Pi.** Pi has no views: its `ctx.ui.custom` components draw in the terminal only. Views are the PiG-additive feature D614. Stock PiG attaches no view client, so `ctx.ui.viewAbilities()` returns `null` and `ctx.ui.mountView` rejects with code `unsupported` until a Piglet attaches one.

This page uses the Node SDK, because a `.mjs` extension loads without a build. [The calls in each SDK](#the-calls-in-each-sdk) names the same calls for Go, Python, Rust and Lisp. [What the clients draw](#what-the-clients-draw) says what each client does with a view at this version.

## Try it

The terminal draws views when the active Piglet sets `viewClient: terminal`. Save this Piglet in the directory of the extension file and start it there with `pig --piglet ./views.yaml`.

```yaml
name: views
description: "Draw extension views in the terminal and serve them to other clients."
viewClient: terminal
viewEndpoint: socket
extensions:
  - name: todo
    origins:
      - local:./todo.mjs
```

A Piglet's `extensions` and the `-e` option load a `.mjs` file. The extensions folders load only `.ts` and `.js` files ([Extensions](extensions.md)). After you edit the extension, type `/reload`.

`viewEndpoint: socket` serves the session's views on the Unix socket `<agent directory>/view-endpoint/<pid>.sock`, for example under `~/.pig/agent/view-endpoint/`. A client program of the same user attaches there. The `pig` binary contains no web page and no web server. The reference web client is in a checkout of the PiG repository, in `examples/ui-view-web`, and needs Node:

```bash
node examples/ui-view-web/bridge.mjs --connect ~/.pig/agent/view-endpoint
node examples/ui-view-web/bridge.mjs -- -e ./todo.mjs
```

The first command attaches a web page to the running terminal session. The second command starts a `pig` of its own for each page and needs no Piglet. Both print the page's address, `http://127.0.0.1:<port>/?token=<token>`.

## A complete extension

This extension mounts a todo list in the `panel` slot. A terminal draws the plain view. A web page draws the alternative. Both show the same state.

```js
// todo.mjs: one todo list for the terminal and a web page.
export default function todo(pi) {
  const items = [
    { id: "milk", label: "Buy milk", done: false },
    { id: "letter", label: "Send the letter", done: true },
    { id: "plants", label: "Water the plants", done: false },
  ];
  let mount = null;

  const open = () => items.filter((item) => !item.done).length;
  const list = () => ({
    kind: "select-list",
    id: "items",
    items: items.map((item) => ({ value: item.id, label: `${item.done ? "[x]" : "[ ]"} ${item.label}` })),
  });
  const plain = () => ({ root: { kind: "vstack", children: [{ kind: "text", text: `Todo: ${open()} open. Press c to clear the done items.` }, list()] } });
  const rich = () => ({ root: { kind: "vstack", gap: 1, children: [{ kind: "markdown", text: `# Todo\n\n**${open()}** of ${items.length} open.` }, list()] } });
  const content = () => ({
    title: "Todo",
    view: plain(),
    alternatives: [{ when: { any: [{ all: [{ claim: "client.kind", op: "equals", value: "web" }] }] }, view: rich() }],
    intents: [
      { name: "todo.toggle", nodeKey: "items", arg: { type: "object", properties: { value: { type: "string" } }, required: ["value"] } },
      { name: "todo.clear", arg: { type: "object" }, answer: { type: "object", properties: { removed: { type: "integer" } }, required: ["removed"] } },
    ],
    bindings: [{ key: "c", intent: "todo.clear", name: "Clear done" }],
    a11y: [{ node: "items", name: "Todo items", role: "list" }],
    focus: [{ node: "items", request: "mount" }],
    fallback: { kind: "text", text: "The todo list does not fit this client." },
  });

  // Mount once, when a client that draws panels is attached.
  const show = async (abilities, ctx) => {
    if (!abilities || mount || !abilities.slots.includes("panel")) return;
    mount = (await ctx.ui.mountView({ key: "todo", slot: "panel", ...content() })).mount;
  };
  pi.onViewAbilities(show);
  pi.on("session_start", (_event, ctx) => show(ctx.ui.viewAbilities(), ctx));

  pi.onViewIntent(async (inv, ctx) => {
    let answer;
    if (inv.intent === "todo.toggle") {
      const item = items.find((entry) => entry.id === inv.arg.value);
      if (!item) return { outcome: "refused", error: "no such item" };
      item.done = !item.done;
    } else if (inv.intent === "todo.clear") {
      const before = items.length;
      items.splice(0, items.length, ...items.filter((item) => !item.done));
      answer = { removed: before - items.length };
    } else return { outcome: "refused", error: "unknown intent" };
    mount = (await ctx.ui.updateView(mount, content())).mount;
    return { outcome: "done", answer };
  });
}
```

## Mount, update and end

- `ctx.ui.viewAbilities()` returns what the attached clients draw, or `null` when no client is attached: `{ slots, nodes, markdown, imageFormats, limits, input, claims, slotClaims, drawing }`. `slots` and `nodes` are arrays of strings. `drawing` is absent when no client draws a profile. With several clients the record is their merge: the union of the slots, node kinds and drawing profiles, the largest limits, and the claims that every client states with the same value.
- `pi.onViewAbilities((abilities, ctx) => ...)` runs when the first client attaches, when the abilities change and when the last client leaves (`null`). It does not run for the state the extension starts in, so also read `ctx.ui.viewAbilities()` at `session_start`. A mount stays live while no client is attached, and a client that attaches later is sent it.
- `ctx.ui.mountView({ key, slot, target, title, view, intents, bindings, alternatives, drawing, a11y, focus, fallback })` mounts the view. Only `key`, `slot` and `view` are required. Omit a member that you do not use. `key` is 1 to 128 bytes and names the mount inside your extension. `view` is `{ root: node }`. `title` is the label of the mount in a tab or a list of views: at most 64 bytes on one line. `target` is required for three slots only: `{ toolCallId }` for `tool-card`, `{ messageType }` for `message` and `{ entryType }` for `entry`. `fallback` is one node: a client that cannot draw the content is sent it, marked `degraded`, when another attached client can draw the content.
- Every operation returns a promise of `{ mount, state }`. `mount` is the ref `{ key, epoch, rev }`. Pass the newest ref to the next operation: an older ref rejects with code `stale`.
- `ctx.ui.updateView(mount, content)` replaces the whole content. `content` has the members of a mount without `key`, `slot` and `target`. Send every member again, `title` included: an update without a member clears it.
- `ctx.ui.hideView(mount)` and `ctx.ui.showView(mount)` hide and show a mount without ending it.
- `ctx.ui.finishView(mount, result)` ends a mount as completed, and `result` is any JSON value of at most 65536 bytes. `ctx.ui.removeView(mount)` ends it as withdrawn. After either, an operation on the mount rejects with code `terminal`, and you can mount the same key again.
- `ctx.ui.viewState(key)` returns `{ mount, state, reason, result }`. `state` is `live`, `hidden`, `finished`, `removed` or `broken`. The host breaks a mount with `reason` `session_replaced`, `owner_gone` or `shutdown`.
- `ctx.ui.viewDiagnostics(key)` returns the notices of a mount: which update the host rejected, which intent it ended, what a client did not draw (`undrawn`) and why a client did not give the focus (`unfocused`). The host keeps 32 notices for a mount.
- `/reload` loads the extension as a new instance. The mounts of the old instance are `broken` with reason `owner_gone`. The new instance holds no mount and mounts again.

## Nodes

A node is a JSON object with a `kind`. Give a node an `id` when an intent, an `a11y` record, a `focus` record or a drawing names it. Omit a member that you do not set. A kind that no attached client lists in `abilities.nodes` rejects the mount with code `invalid`, and so does an unknown member. The sizes of a node are terminal cells.

| Kind | What it is | Members |
|---|---|---|
| `vstack`, `hstack` | children in a column or a row | `children`, `gap`; a child's `stack: { basis, grow, shrink, minSize, maxSize }` |
| `container` | children in a column | `children` |
| `box` | padded children on a background | `children`, `paddingX`, `paddingY` (1 when absent), `bg` |
| `text` | wrapped text | `text`, `paddingX`, `paddingY` (1 when absent), `bg` |
| `truncated-text` | one row of text, cut at the width | `text` |
| `markdown` | rendered markdown | `text`, `defaultTextStyle: { color, bgColor, bold, italic, underline, strikethrough }` |
| `lines` | rows that your extension drew, shown verbatim | `content` (an array of strings) |
| `spacer` | empty rows | `lines` |
| `dynamic-border` | a horizontal rule | `color` |
| `select-list` | a list with one highlighted item; the user chooses an item | `items: [{ value, label, description }]`, `selectedIndex`, `maxVisible` |
| `settings-list` | settings that step through their values | `items: [{ id, label, currentValue, values }]` |
| `input` | one line of text; Enter submits | `value`, `placeholder` |
| `editor` | several lines of text | `value` |
| `mouse-region` | a button: a click activates it. It needs a pointer (`abilities.input.pointer`), which the terminal has only in fullscreen mode, so also give its action a binding | `children` (an array of one node, for example a `text` with the label), `region: { types: ["press"] }` |
| `scroll-view` | one child that scrolls | `children` (an array of one node), `follow: "end"` |
| `grid` | a calendar-style grid of equal columns; the user chooses a cell | `id` (required), `columns` (1 to 16), `head` (one label for each column), `chipRows` (1 to 8, default 3), `selected` (a cell's `value`), `cells: [{ value, label, mark: "current", dim, color, chips: [{ text, color }], more }]` |

A `grid` has at most 128 cells in at most 32 rows, and a cell has at most 8 chips. A cell without a `value` cannot be chosen. `more` counts the events that you left out. A `bg`, a `color` and the colors of a `defaultTextStyle` name theme tokens ([Themes](themes.md)); a grid `color` is a theme token, `#rrggbb` or `#rrggbbaa`.

```js
// week.mjs: a grid, and a view that the user hides and opens again.
export default function week(pi) {
  const days = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  let picked = "Wed";
  let mount = null;

  const content = () => ({
    title: "Week",
    view: {
      root: {
        kind: "grid",
        id: "days",
        columns: 7,
        head: days,
        selected: picked,
        cells: days.map((day, i) => ({ value: day, label: String(i + 10), mark: day === "Wed" ? "current" : undefined, chips: day === "Fri" ? [{ text: "Demo", color: "#3366ff" }] : [] })),
      },
    },
    intents: [
      { name: "week.pick", nodeKey: "days", arg: { type: "object", properties: { value: { type: "string" } }, required: ["value"] } },
      { name: "week.hide", arg: { type: "object" } },
      { name: "week.open", arg: { type: "object" }, whenHidden: true },
    ],
    bindings: [{ key: "h", intent: "week.hide", name: "Hide" }],
    focus: [{ node: "days", request: "mount" }],
  });

  const show = async (abilities, ctx) => {
    if (!abilities || mount || !abilities.slots.includes("panel") || !abilities.nodes.includes("grid")) return;
    mount = (await ctx.ui.mountView({ key: "week", slot: "panel", ...content() })).mount;
  };
  pi.onViewAbilities(show);
  pi.on("session_start", (_event, ctx) => show(ctx.ui.viewAbilities(), ctx));

  pi.onViewIntent(async (inv, ctx) => {
    if (inv.intent === "week.hide") mount = (await ctx.ui.hideView(mount)).mount;
    else if (inv.intent === "week.open") mount = (await ctx.ui.showView(mount)).mount;
    else {
      picked = inv.arg.value;
      mount = (await ctx.ui.updateView(mount, content())).mount;
    }
    return { outcome: "done" };
  });
}
```

## Intents

An intent is an action that the user raises on a mount. Declare every intent in `intents`, as the examples do.

- `name` is unique in the mount. Write it as `<extension>.<verb>`.
- `nodeKey` binds the intent to the node with that `id`. Choosing a `select-list` item or a `grid` cell, changing a `settings-list` value, submitting an `input` or an `editor`, and clicking a `mouse-region` raise the intent. The host refuses an intent that has a `nodeKey` when a client raises it on another node or on none.
- An intent without `nodeKey` belongs to the whole mount. A binding, a drawing region and `whenHidden` need such an intent.
- `arg` is the shape of the argument. The host checks the argument against it before your handler runs. Declare an object with a string `value` for a `select-list` (the item's `value`), a `grid` (the cell's `value`), an `input` and an `editor` (the text). Declare an object with strings `item` and `value` for a `settings-list` (the setting's `id` and its new value). Declare `{ type: "object" }` for a `mouse-region`: it accepts only `{}`.
- A shape is `{ type }`, where `type` is `boolean`, `string`, `integer`, `number`, `enum` (with `enum`: an array of at most 32 strings), `object` (with `properties` and `required`; at most 32 properties, and the value may hold no other property) or `array` (with `items` and `maxItems`; at most 64 items). A shape nests at most 4 deep.
- `answer` is the optional shape of the handler's answer. An answer outside it settles the intent `failed`.
- `timeoutMs` is 100 to 300000 and defaults to 30000. `cancellable: true` lets the host abort `inv.signal` at the timeout.
- `whenHidden: true` lets a client raise the intent while the mount is hidden. A client lists the hidden mount by its title, and when the user chooses it your handler shows the mount. At most one intent of a mount has it. It has no `nodeKey`, and its `arg` accepts `{}`.

One handler answers every intent: `pi.onViewIntent(async (inv, ctx) => ...)`. `inv` has `intent`, `arg`, `nodeKey`, `mount`, `rev`, `ticket` and `signal`. Return `{ outcome: "done", answer }`, `{ outcome: "refused", error }` or `{ outcome: "failed", error }`. Change your state, call `ctx.ui.updateView` through the handler's `ctx` with the ref that you keep, wait for it, then answer. The client is sent the new view before it is told that the intent is done. A hide, a show, a finish and a remove in the handler are also part of the answer. An update from outside the handler while it answers settles the intent `stale`.

## Bindings

`bindings` (at most 32) ties a key or a gesture to an intent without `nodeKey`: `{ key, intent, arg, name }` or `{ gesture, intent, arg, name }`.

- `key` is a key id such as `d`, `shift+left` or `ctrl+shift+p`. `gesture` is `swipe-left`, `swipe-right`, `swipe-up` or `swipe-down`. A key or a gesture is bound once.
- `arg` is the constant argument (`{}` when absent), so several bindings can raise one intent with different arguments. `name` is the label that a client shows.
- A binding works only while the mount holds the focus, and after the focused node had the key. An `input` or an `editor` takes every key, so give such a view another way to its actions.
- The keys `escape`, `tab`, `f6` and `ctrl` with `c`, `d` or `z` are reserved: a binding of one rejects with code `invalid`.
- A client that does not honour a binding leaves an `unsupported` notice with the path `bindings[i]`.

## Alternatives

A client states facts about itself as claims. `alternatives` (at most 8) gives a client another view when its claims match. The host sends each client the first alternative whose `when` holds, otherwise `view`. One mount and one state serve every client.

| Client | Claims |
|---|---|
| terminal | `client.kind` = `"terminal"`, `size.columns` (integer), `size.rows` (integer; the rows of the mount's slot) |
| reference web page | `client.kind` = `"web"` |

`when` is `{ any: [{ all: [clause, ...] }, ...] }`: it holds when every clause of one group holds. A `when` has at most 8 groups of 8 clauses. A clause is `{ claim, op, value }`, and `op` is one of:

- `present`: the client states the claim. It takes no `value`.
- `equals`: the claim equals `value`, a boolean, an integer or a string.
- `contains`: the claimed list holds every string of the array `value`.
- `atLeast`: the claimed integer is at least `value`.

```js
const wideTerminal = { any: [{ all: [{ claim: "client.kind", op: "equals", value: "terminal" }, { claim: "size.columns", op: "atLeast", value: 100 }] }] };
```

An alternative is `{ when, view, drawing }`. It shares the mount's `intents`, `bindings`, `a11y` and `focus`, so use the same node ids in every view. A client is sent the `a11y` and `focus` entries of the nodes that its view has. The mount's `drawing` belongs to `view`: an alternative does not inherit it.

## Accessibility and focus

- `a11y: [{ node, name, description, role, selected, checked, expanded, busy, disabled, readOnly, required, invalid, relations }]` describes the node with id `node`. Only `node` is required. `role` is one of `alert`, `button`, `checkbox`, `dialog`, `group`, `heading`, `image`, `link`, `list`, `listitem`, `menu`, `menuitem`, `progressbar`, `radio`, `region`, `status`, `switch`, `tab`, `tablist`, `tabpanel`, `textbox`, `toolbar`, `tree` and `treeitem`. A relation is `{ kind, target }` with `labelledBy`, `describedBy`, `controls` or `owns`.
- `focus: [{ node, request }]` makes nodes focusable. The order of the entries is the focus order. `request: "mount"` asks for the focus when the mount is first drawn. `request: "update"` asks when the revision that carries it is first drawn, so send it only with the update that moves the focus. At most one entry requests the focus. The client decides: when it does not give the focus, it leaves an `unfocused` notice.
- Each list has at most 256 entries. A `node` that no view of the mount has rejects the mount with code `invalid`.

## Drawings

A drawing adds vector shapes or a pixel grid to a view. Send a drawing only when `abilities.drawing.profiles` lists its profile: without a client that draws the profile, the mount rejects with code `unsupported`.

```js
// meter.mjs: a level meter. A client that draws the scene profile is sent a drawing. Every other client is sent text.
export default function meter(pi) {
  let level = 3;
  let mount = null;
  let scene = false;

  const drawing = () => ({
    profile: "scene",
    node: "canvas",
    viewport: { x: 0, y: 0, width: 100, height: 20, fit: "contain" },
    shapes: [
      { kind: "rect", x: 0, y: 0, width: 100, height: 20, radius: 4, fill: "#20242c" },
      { kind: "rect", key: "bar", x: 0, y: 0, width: level * 10, height: 20, radius: 4, fill: "accent" },
      { kind: "text", x: 50, y: 14, size: 10, align: "center", text: `${level * 10}%`, fill: "#ffffff" },
    ],
    regions: [{ key: "raise", shape: { kind: "rect", x: 0, y: 0, width: 100, height: 20 }, gestures: ["activate"], intent: "meter.raise", name: "Raise the level" }],
  });
  const text = () => ({ kind: "text", text: `Level ${level} of 10. Press r to raise it.` });
  // The region and the binding raise the intent, so the intent has no nodeKey.
  const content = () => ({
    view: { root: scene ? { kind: "box", id: "canvas" } : text() },
    ...(scene ? { drawing: drawing() } : {}),
    intents: [{ name: "meter.raise", arg: { type: "object" }, answer: { type: "object", properties: { level: { type: "integer" } }, required: ["level"] } }],
    bindings: [{ key: "r", intent: "meter.raise", name: "Raise" }],
    fallback: text(),
  });

  const show = async (abilities, ctx) => {
    if (!abilities || mount || !abilities.slots.includes("panel")) return;
    scene = abilities.drawing?.profiles.includes("scene") ?? false;
    try {
      mount = (await ctx.ui.mountView({ key: "meter", slot: "panel", ...content() })).mount;
    } catch (err) {
      ctx.ui.notify(`meter: ${err.code ?? err.message}`, "error");
    }
  };
  pi.onViewAbilities(show);
  pi.on("session_start", (_event, ctx) => show(ctx.ui.viewAbilities(), ctx));

  pi.onViewIntent(async (inv, ctx) => {
    if (inv.intent !== "meter.raise") return { outcome: "refused", error: "unknown intent" };
    level = Math.min(10, level + 1);
    mount = (await ctx.ui.updateView(mount, content())).mount;
    return { outcome: "done", answer: { level } };
  });
}
```

- `profile` is `scene` (vector `shapes`) or `pixels` (`pixels: { width, height, asset, scaling: "nearest" | "smooth" }` over an image asset declared in `assets`).
- `node` is the id of the view node that the drawing fills. Without `node`, the drawing follows the view.
- `viewport` is optional: `{ x, y, width, height, fit }` is the rectangle of drawing coordinates that the client shows. `width` and `height` are positive, and `fit` is `contain`, `cover` or `stretch`.
- A shape has a `kind` and the members of that kind. A member of another kind rejects the drawing.

| Shape | Members |
|---|---|
| `rect` | `x`, `y`, `width`, `height`, `radius` (optional) |
| `ellipse` | `cx`, `cy`, `rx`, `ry` |
| `line` | `x1`, `y1`, `x2`, `y2` |
| `polyline` | `points: [{ x, y }]` (2 to 1024), `closed` |
| `path` | `commands: [{ op, ... }]` (1 to 1024, the first is `move`); `op` is `move` or `line` (`x`, `y`), `quad` (`x1`, `y1`, `x`, `y`), `cubic` (`x1`, `y1`, `x2`, `y2`, `x`, `y`) or `close` |
| `text` | `x`, `y`, `text`, `size`, `align: "start" | "center" | "end"` |
| `image` | `x`, `y`, `width`, `height`, `asset` |
| `group` | `children` |

- Every shape also takes the optional `key`, `fill`, `stroke: { color, width }`, `opacity` and `transform` (six numbers). A color is `#rrggbb`, `#rrggbbaa` or a theme color token.
- `regions: [{ key, shape, gestures, intent, name, role }]` turns a gesture inside `shape` into a declared intent. `key` is unique among the regions. `shape` is a `rect`, an `ellipse` or a closed `polyline`. `gestures` lists `activate`, `press`, `release` or `drag`. `name` is required, and `role` is optional. The client tests which region a pointer is in, and it raises the intent with an argument of the intent's `arg` shape. Declare a region's intent without `nodeKey`.
- A drawing has at most 4096 shapes nested 8 deep, 1048576 pixels and 64 regions, and never more shapes or pixels than `abilities.drawing.shapes` and `abilities.drawing.pixels` (code `limit`).

## Slots

`slot` is one of `notify`, `status`, `widget-above`, `widget-below`, `header`, `footer`, `dialog`, `overlay`, `panel`, `tool-card`, `message` and `entry`. A mount has one slot. To show the same state in a panel and in a dialog, make two mounts with two keys. Mount only in a slot that `abilities.slots` lists: another slot rejects with code `unsupported`.

## Errors

A rejected operation rejects its promise with an `Error` whose `code` is one of:

| Code | Meaning |
|---|---|
| `invalid` | The record is malformed or past a bound of the host: an unknown member, node kind, slot or operator, a key that is already mounted, or a list that is too long. Nothing changes. |
| `unsupported` | No attached client can draw the mount: no client is attached, no client draws the slot or the drawing profile, or the mount declares intents and the client delivers no input. |
| `limit` | The content exceeds what the client states in `abilities.limits` (`nodes`, `depth`, `snapshotBytes`, `textBytes`, `mounts`) or in `abilities.drawing`. |
| `stale` | The ref is not the newest ref of the mount. |
| `terminal` | The mount has ended. |
| `not_found` | The extension has no mount under this key. |

## The calls in each SDK

Every SDK has every call, and its mount takes the same members. The Go, Python, Rust and Lisp SDKs build the `view` with their component kit.

| Call | Node | Go | Python and Rust | Lisp |
|---|---|---|---|---|
| read the abilities | `ctx.ui.viewAbilities()` | `ctx.ViewAbilities()` | `ctx.view_abilities()` | `ctx-view-abilities` |
| abilities changed | `pi.onViewAbilities` | `ext.OnViewAbilities` | `ext.on_view_abilities` | `on-view-abilities` |
| mount | `ctx.ui.mountView` | `ctx.MountView` | `ctx.mount_view` | `ctx-mount-view` |
| update | `ctx.ui.updateView` | `ctx.UpdateView` | `ctx.update_view` | `ctx-update-view` |
| hide, show | `hideView`, `showView` | `HideView`, `ShowView` | `hide_view`, `show_view` | `ctx-hide-view`, `ctx-show-view` |
| finish | `finishView(mount, result)` | `FinishView`, `FinishViewWith` | Python `finish_view(mount, result=...)`; Rust `finish_view`, `finish_view_with` | `ctx-finish-view` (`:result`) |
| remove | `removeView` | `RemoveView` | `remove_view` | `ctx-remove-view` |
| state | `viewState` | `ViewState` | `view_state` | `ctx-view-state` |
| diagnostics | `viewDiagnostics` | `ViewDiagnostics` | `view_diagnostics` | `ctx-view-diagnostics` |
| answer intents | `pi.onViewIntent` | `ext.OnViewIntent` | `ext.on_view_intent` | `on-view-intent` |

## What the clients draw

| Capability | Terminal (`viewClient: terminal`) | Reference web page (`examples/ui-view-web`) |
|---|---|---|
| Slots | `widget-above` and `widget-below` (at most 10 rows each), `panel` (at most half the terminal's rows); every other slot is not drawn at this version | all twelve slots |
| Node kinds | every kind of [Nodes](#nodes) | every kind of [Nodes](#nodes) except `grid`, which is not drawn at this version |
| Limits of one mount | 512 nodes, depth 16, 262144 snapshot bytes, 65536 text bytes, 16 mounts, 4096 intent argument bytes | 2048 nodes, depth 32, 1048576 snapshot bytes, 65536 text bytes, 32 mounts, 65536 intent argument bytes |
| `title` | panels are tabs, labelled by `title`, or by `key` without one | not used at this version: a tab is labelled by `key` |
| Intents with `nodeKey` | raised by the keys and clicks of the node | raised by the keys and clicks of the node |
| Intents without `nodeKey` | raised by a binding only | a button on the mount, labelled with the intent's `name` |
| `bindings` | keys work in a focused panel, and the named bindings are listed under it; gestures are not offered | not used at this version |
| Hidden mount with `whenHidden` | a dim tab; Enter raises the intent | not offered at this version |
| Focus | F6 walks from the editor through the tabs and back, Escape returns to the editor | the `focus` order is the tab order |
| `a11y` | not used at this version | `name`, `description`, `role`, the states and the relations become ARIA attributes |
| Drawings | not drawn at this version | not drawn at this version |

<!-- site-only:begin -->
[Terminal UI](tui.md#extension-views) lists the terminal's rows and keys. [Piglets](piglets.md#view-client) describes `viewClient` and `viewEndpoint`.
<!-- site-only:end -->
