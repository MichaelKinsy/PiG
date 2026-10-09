# Extension component kit (D107)

Status: approved by the owner, Michael Kinsy, on 2026-10-06 ("build now");
the conversation kinds (§2.1) approved on 2026-10-07.
Record: D107 in `docs/additive-features.md`; amends D91.
Background: `ext-structure-REPORT.md` (structure across the subprocess
boundary) and the "Custom TUI components" high-risk gap in
`docs/extension-api-parity.md`.

## 1. Problem and decision

Pi passes in-process `Component` objects to every extension UI surface
(`ctx.ui.custom`, `setWidget`, `setHeader`/`setFooter`, tool `renderCall`/
`renderResult`, message and entry renderers). PiG's subprocess wire carries
only ANSI `lines`. Node authors still get Pi's component library, because they
import the real pi-tui inside the Node runtime. Go, Rust and Python authors can
only draw lines. A D91 frontend sees every extension surface as an opaque
`Lines` blob.

Decision: a **shared component kit rendered by PiG**.

- Go, Rust and Python extensions describe Pi's standard components as a
  declarative tree, the **view**. PiG's host renders the view with its Go `tui`
  ports, so the terminal shows what Pi's components draw.
- The Node runtime reads its real pi-tui components into the same view after
  the render it already performs. The lines Node rendered stay authoritative.
  The host keeps the view only when rendering it reproduces those lines
  exactly.
- A D91 frontend receives the view as optional structure next to the lines,
  and falls back to the lines for anything it does not draw natively.
- A custom `render(width)` component stays an opaque `lines` range in every
  SDK, as in Pi.
- Stock PiG has no frontend. Without one, the terminal output and the cost of
  every existing extension are unchanged: Node sends no view, and the host
  builds no frontend structure.

The kit adds no extension runtime kind and no new process. It is wire data
plus host rendering.

## 2. Node schema

A view is one JSON object:

```text
View {
  root:   Node              // required
  focus?: string            // id of the select-list/settings-list that receives keys (ui.custom only)
  theme?: {token: "#rrggbb"} // per-surface token overrides (§6)
  width?: int               // cells the sender laid `lines` out at; required when the frame also carries lines (§5.2)
  images?: [{ref, mimeType, data}] // image bytes sent with this frame (§7)
}
```

Every node has a `kind`. Field names mirror the upstream constructor
parameters and state 1:1 (camelCase, as on the wire elsewhere). An omitted
field takes the upstream default. When upstream has no default (a required
constructor argument), the omitted value is the zero value given below.

Upstream citations are relative to `.upstream/current/packages/tui/src/`,
unless prefixed `ca:` for `.upstream/current/packages/coding-agent/src/`.

| kind | fields (wire default) | upstream | PiG Go port the host renders with |
|---|---|---|---|
| `container` | `children` | `tui.ts:347-406` `Container` | `tui/tui.go` `NewContainer` |
| `box` | `paddingX` (1), `paddingY` (1), `bg` (none), `children` | `components/box.ts:14-37` `Box(paddingX=1, paddingY=1, bgFn?)` | `tui/box.go` `Box` |
| `text` | `text` (""), `paddingX` (1), `paddingY` (1), `bg` (none) | `components/text.ts:7-23` `Text(text="", paddingX=1, paddingY=1, customBgFn?)` | `tui/text.go` `NewPaddedText` |
| `truncated-text` | `text` (""), `paddingX` (0), `paddingY` (0) | `components/truncated-text.ts:7-16` | `tui/truncated_text.go` |
| `markdown` | `text`, `paddingX` (0), `paddingY` (0), `defaultTextStyle` {`color` fg token, `bgColor` bg token, `bold`, `italic`, `strikethrough`, `underline`}, `renderLatex` (true) | `components/markdown.ts:181-194, 236-268`; theme `ca:modes/interactive/theme/theme.ts:1095-1132` `getMarkdownTheme()` | `tui/markdown.go`, `tui/markdown_style.go` |
| `spacer` | `lines` (1) | `components/spacer.ts:6-13` | `tui/spacer.go` |
| `dynamic-border` | `color` fg token ("border") | `ca:modes/interactive/components/dynamic-border.ts:11-25` | `tui/dynamic_border.go` |
| `select-list` | `id` (required), `items` [{`value`, `label`, `description`?}], `maxVisible` (5), `layout` {`minPrimaryColumnWidth`, `maxPrimaryColumnWidth`}, `selectedIndex`?, `filter`? | `components/select-list.ts:12-60` (state `items`, `filteredItems`, `selectedIndex`; `setFilter`, `setSelectedIndex`); theme `ca:theme.ts:1134-1142` `getSelectListTheme()` | new faithful port `tui/select_list.go` `SelectList` (PiG had only `FilterableList`, which adds a search row and differs in wrap and defaults) |
| `settings-list` | `id` (required), `items` [{`id`, `label`, `description`?, `currentValue`, `values`?, `submenu`?: Node}], `maxVisible` (required, 0 → 1), `enableSearch` (false), `selectedIndex`? (state `selectedIndex`, an index into the shown items), `filter`? (the search input's value) | `components/settings-list.ts:7-75`; theme `ca:theme.ts:1151-1159` `getSettingsListTheme()` | `tui/settings_list.go` `NewSettingsListWithOptions`, given Pi's theme (§6) |
| `image` | `ref` (required), `mimeType` (required), `maxWidthCells`?, `maxHeightCells`?, `filename`?, `imageId`? (Kitty id; absent: the host allocates one as upstream does), `fallbackColor` fg token (none) | `components/image.ts:44-90` `Image(base64, mimeType, theme, options)` | `tui/image.go` `NewImage` |
| `loader` | `message` ("Loading..."), `spinnerColor` fg token (none), `messageColor` fg token (none), `indicator`? {`frames`?, `intervalMs`?}, `frame`? (state `currentFrame`, 0) | `components/loader.ts:17-101` | `tui/loader.go` `Loader`, animated by the host from `frame` (§4.4) |
| `hstack` / `vstack` | `children`, each child optionally carrying the StackEntry options as `stack` {`basis`? (absent = "auto"), `grow`?, `shrink`?, `minSize`?, `maxSize`?}; `gap` (0), `align` ("stretch") | `components/stack.ts:4-74`, `h-stack.ts`, `v-stack.ts` | `tui/stack.go` `NewHStack`/`NewVStack` |
| `lines` | `content` (opaque rows, the name `ui.setWidget` already uses for rows), frontend-only `image`? {`ref`}, `progress`? {`value`, `max`}, `list`? {`items` [{`label`, `detail`?, `columns`?}], `selectedIndex`} | any component with its own `render` | returned verbatim |

Rules that keep the schema closed and faithful:

- **Closures do not cross.** Every style closure upstream takes is replaced
  by a theme token name: Box `bgFn` and Text `customBgFn` become `bg` (`theme.bg(token, …)`),
  DynamicBorder's `color`, Loader's `spinnerColorFn`/`messageColorFn` and Image's
  `fallbackColor` become `theme.fg(token, …)`, and Markdown's
  `DefaultTextStyle.color`/`bgColor` become tokens. A component theme
  (`SelectListTheme`, `SettingsListTheme`, `MarkdownTheme`) is always the one
  coding-agent hands extensions (`getSelectListTheme()`,
  `getSettingsListTheme()`, `getMarkdownTheme()`), built from the surface's
  theme (§6). An absent token is the identity function.
- **Excluded upstream fields**, because they are closures:
  `SelectListLayoutOptions.truncatePrimary`, `MarkdownOptions.transform`,
  `StackEntry.visible`, and `done(…, {navigateTo})` in a settings submenu.
  A Node component that uses one of them becomes a `lines` range (§9).
- **`selectedIndex` and `filter`** are the setter calls `setSelectedIndex` and
  `setFilter`, not constructor arguments. They apply only when they differ
  from the value the same sender sent last time (§4.2).
- **Text stays text.** `text`, labels and descriptions may carry ANSI. The host
  passes them through exactly as Pi would. A frontend shows them as styled
  spans where it can. Pi's theme tokens apply to what the components
  themselves style.
- **`lines` annotations** are PiG-only and frontend-only: `image` says the
  rows depict that image, as pig-music's half-block cover does, `progress`
  says the rows show a position in a range, as a play bar does, and `list`
  says the rows are a list with one item per row, as a track table is
  (`label` the primary text, `detail` a secondary text, `columns` the further
  cells of a table row; `selectedIndex` the highlighted item, or -1). A
  `list` must have exactly as many items as `content` has rows, at most
  10,000, and a `selectedIndex` from -1 to the last item; otherwise the view
  is invalid (§5.2). The terminal always draws the rows. A frontend may draw
  the bitmap, a native bar or a native list in their place. They replace the
  `progress` kind the report proposed: a new kind would have to draw
  something no Pi component draws. A `list` is not interactive: the
  extension that drew the rows keeps handling its keys, as it does for the
  rows.
- **Ids.** `select-list` and `settings-list` require a non-empty `id`, unique
  within the view. Other nodes may carry an `id`, which a frontend may use as a
  stable key. Duplicate ids are invalid.
- **Bounds.** At most 4096 nodes, depth 64, and 10,000 items per list. A
  frame also stays under the connection's `MaxFrameSize`.

### 2.1 Conversation kinds

Pi exports its conversation components to extensions
(`ca:index.ts:435-468`): `UserMessageComponent`, `AssistantMessageComponent`
(which draws thinking), `ToolExecutionComponent`, `BashExecutionComponent` and
`renderDiff`. A Node extension draws a conversation with them at
main-transcript quality, for example a subagent's live conversation in an
overlay. The kit carries them as five kinds. The host renders each with the
Go port the main transcript uses, so the terminal shows the bytes Pi's
component draws, and a frontend gets the nodes the main transcript reports
(§8).

| kind | fields (wire default) | upstream | PiG Go port |
|---|---|---|---|
| `user-message` | `text` (""), `outputPad` (1) | `ca:modes/interactive/components/user-message.ts:19-31` `UserMessageComponent(text, getMarkdownTheme(), outputPad=1, [])` | `tui/user_message_block.go` `NewUserMessageComponent`, `SetOutputPad` |
| `assistant-message` | `message`? {`content` [{`type`: `text` with `text`, `thinking` with `thinking`, or `toolCall`}], `stopReason` ("stop"), `errorMessage` ("")}, `hideThinkingBlock` (false), `hiddenThinkingLabel` ("Thinking..."), `outputPad` (1), `isStreaming` (false) | `ca:modes/interactive/components/assistant-message.ts:26-201` constructor and `updateContent(message, isStreaming)` | `tui/assistant_message_block.go` `AssistantMessageComponent` (`SetContent`, `SetHasToolCalls`, `SetTerminalError`, `SetHideThinkingBlock`, `SetHiddenThinkingLabel`, `SetOutputPad`) |
| `tool-execution` | `toolName` (required), `toolCallId` (""), `args` (any JSON, {}), `toolDefinition` ("builtin"), `cwd` (""), `showImages` (true), `imageWidthCells` (60), state `executionStarted` (false), `argsComplete` (false), `expanded` (false), `result`? {`content` [{`type`: `text` with `text`, or `image` with `ref` and `mimeType`}], `isError` (false), `details` (any JSON)}, `isPartial` (true) | `ca:modes/interactive/components/tool-execution.ts:62-375` constructor, `updateArgs`, `markExecutionStarted`, `setArgsComplete`, `updateResult(result, isPartial)`, `setExpanded`, `setShowImages`, `setImageWidthCells` | `tui/tool_execution.go` and `tool_execution_definition.go` through `internal/codingagent` `NewToolRendererCard` with the built-in renderers of `tool_builtin_renderers.go` |
| `bash-execution` | `command` (""), `excludeFromContext` (false), `output` (""), `complete`? {`exitCode`?, `cancelled` (false), `truncated` (false), `fullOutputPath` ("")}, `expanded` (false), `frame` (0) | `ca:modes/interactive/components/bash-execution.ts:32-205` constructor, `appendOutput`, `setComplete(exitCode, cancelled, truncationResult, fullOutputPath)`, `setExpanded` | `tui/bash_execution.go` `BashExecutionComponent` (`SetCompleteWithOutput` with the output it shows), its spinner animated by the host as a `loader` |
| `diff` | `diff` (""), `filePath` (""), `paddingX` (0), `paddingY` (0) | `ca:modes/interactive/components/diff.ts:84-147` `renderDiff(diffText, {filePath})`, drawn in a `Text(…, paddingX, paddingY)` as Pi's edit renderer draws it | `tui/diff_render.go` `RenderDiff` in `tui/text.go` `NewPaddedText` |

Rules of the conversation kinds:

- **No closures.** `markdownTransformers` and a custom `markdownTheme` are
  closures and are excluded: the kinds draw with `getMarkdownTheme()` and no
  transformer, as Pi's defaults do. `outputPad` is 0 or 1, the values of Pi's
  `outputPad` setting (`ca:core/settings-manager.ts:173`).
- **`toolDefinition`** names the renderers, since a tool definition is
  closures. `"builtin"` is the definition the main transcript draws a tool
  with when the tool has no renderers of its own: the built-in tool's
  definition (`createReadToolDefinition` and its siblings, `edit` with
  `renderShell: "self"`) when `toolName` is `read`, `bash`, `powershell`,
  `edit`, `write`, `grep`, `find` or `ls`, and otherwise a definition without
  renderers. `"empty"` is a definition without renderers (`{}`) for any
  name: Pi's call and result fallbacks. Pi's `undefined` definition, which the
  main transcript never draws, and an extension's own renderers are not
  expressible; a Node component with either becomes a `lines` range.
- **Theme.** Pi's conversation components draw with the global `theme`,
  not with style functions an author passes, so the surface's overrides
  (§6) do not reach them; the host draws them with the active theme.
- **Thinking** is a content block of `assistant-message`, as in Pi. A click on
  a thinking run toggles it as in Pi; that state is the host's (§4.2).
- **`isPartial`** is the component's state, true until a result arrives as
  final (Pi's constructor sets it true; `updateResult` defaults to false).
  SDKs always send it explicitly.
- **`output`** of `bash-execution` is the output appended so far. The host
  appends it with `appendOutput`, which strips ANSI escapes and turns `\r\n`
  and `\r` into `\n`. The header, borders and spinner take the command's
  color (`dim` for `excludeFromContext`, otherwise `bashMode`) in every state,
  as Pi 1.1.0's `updateDisplay` draws them.
- **Kept instances.** An `assistant-message`, `tool-execution` or
  `bash-execution` node with an `id` keeps its host component across frames
  while its kind and constructor fields (`toolName`, `toolCallId`,
  `toolDefinition`, `cwd`; `command`, `excludeFromContext`) are unchanged, as
  a Pi author keeps a component and calls its update methods. A field that
  differs from the value this sender sent last applies through the matching
  upstream method: `updateContent`, `setHideThinkingBlock` (which clears the
  click toggles), `setHiddenThinkingLabel`, `setOutputPad`; `updateArgs`,
  `setArgsComplete`, `markExecutionStarted`, `updateResult`, `setExpanded`,
  `setShowImages`, `setImageWidthCells`; `appendOutput` of the added output,
  `setComplete`, `setExpanded`. Upstream state that only moves forward
  (`executionStarted`, `argsComplete`, output that no longer extends the
  last output, a completed bash command running again) constructs a new
  instance. So a tool card's renderer state (a shell's elapsed time, an
  edit's preview) and a click's toggle live as long as the node, and a
  repeated value never undoes a click. A node without an `id` is a new
  component in every frame, as `new …Component` is in Pi.
- **Images** in a tool result travel as other kit images (§7): `ref` names
  bytes sent in `view.images`.
- **Live rendering.** A running `bash-execution` spinner is host-animated
  (§4.4). A tool card that draws again on its own (a shell's elapsed time, an
  edit preview computed off the loop) asks the host to repaint, as Pi's
  `context.invalidate` requests a render. A Node view whose rows depend on
  such host-timed state reproduces its lines only when the times agree; the
  host drops it otherwise (§5.2).
- **Bounds.** At most 10,000 content blocks per message and per tool result,
  as for list items.

Example (Go: a titled track picker):

```json
{"root":{"kind":"container","children":[
  {"kind":"dynamic-border","color":"accent"},
  {"kind":"text","text":"Pick a track","paddingX":1,"paddingY":0},
  {"kind":"select-list","id":"tracks","maxVisible":10,
   "items":[{"value":"t1","label":"Blue in Green","description":"Miles Davis"}]},
  {"kind":"dynamic-border","color":"accent"}]},
 "focus":"tracks","theme":{"accent":"#e0a040"}}
```

Example (Go: one exchange):

```json
{"root":{"kind":"container","children":[
  {"kind":"user-message","text":"List src","outputPad":1},
  {"kind":"assistant-message","id":"a1","outputPad":1,
   "message":{"content":[{"type":"thinking","thinking":"Use ls."},{"type":"toolCall"}],"stopReason":"toolUse"}},
  {"kind":"tool-execution","id":"call-1","toolName":"ls","toolCallId":"call-1",
   "args":{"path":"src"},"cwd":"/work","argsComplete":true,"executionStarted":true,
   "result":{"content":[{"type":"text","text":"a.go\nb.go"}]},"isPartial":false}]}}
```

## 3. Wire (protocol.go)

The view is optional on every frame that carries extension lines:

| surface | carrier | new field |
|---|---|---|
| `ui.custom` frame | `RemoteOverlayRenderPayload` (notify `ui.custom.render`) | `view` |
| widget push | `WidgetPushPayload` (`widget_push`) and the `ui.setWidget` call args | `view` |
| header/footer | `ui.setHeader`/`ui.setFooter` call args | `view` |
| tool `renderCall`/`renderResult`, message and entry renderers | `RenderResult` (the `render_tool`, `render_message` and `render_entry` responses) | `view` |

Two frame modes, chosen by the sender:

1. **Authoritative view** (Go, Rust, Python): `lines` is **absent** and `view`
   is present. The host renders the view at the width it lays the surface out
   at, and that is what the terminal shows. A `lines` node supplies its rows
   verbatim.
2. **Annotated lines** (Node): `lines` is present and stays what the terminal
   shows. `view` (with `view.width`) describes them. The host renders the view
   at `view.width` and compares the result byte for byte with `lines`. When
   they differ, the view is dropped for that frame and only the lines remain
   (§5.2). Node sends a view only while the host reports a frontend (§8).

New messages:

- notify `ui.view.event` (host → SDK), `ViewEventPayload{key, node, type, index, item, id, value}`: an interactive node's callback fired (§4). `key` is the `ui.custom` key.
- notify `ui.view.evicted` (host → SDK), `ViewEvictedPayload{refs}`: the
  host dropped image bytes. The SDK sends their data again the next time a
  frame references them (§7).
- `StatePayload.frontend` (in the ready state and in `state_update`): `true`
  while a frontend session draws the interactive mode (§8).

The wire has no version or negotiation, as for every other field (AGENTS.md:
one current wire contract). An SDK that does not know `view` never sends it.

## 4. Interactivity: select-list and settings-list

### 4.1 State owner

For authoritative views, **the host owns interactive state**: the selection,
filter, scroll window, search input, the open submenu, and each setting's
current value after a change. It holds one `tui.SelectList` or
`tui.SettingsList` per `(surface, id)`, and runs the user's keybindings
(`tui.select.*`) on PiG's loop with Pi's own key handling. An SDK has no
keybinding table and no port of these components, so moving the state into
the SDK would mean three re-ports that drift. In Node, state stays in the
real pi-tui component inside the Node runtime, exactly as in Pi; its view
only reports it.

### 4.2 Frame updates

A new frame rebuilds the component tree, but keeps an interactive node's
host instance when its `id` and kind are unchanged and its constructor fields
(`items` without `currentValue`, `maxVisible`, `layout`, `enableSearch`, the
submenus) are equal. Pi's equivalent is an author who keeps a component
instead of constructing a new one. Then:

- a `selectedIndex` or `filter` that differs from the value this sender sent
  last for that node is applied through `setSelectedIndex`/`setFilter`;
- a setting's `currentValue` that differs from the last value sent is applied
  through `updateValue(id, value)`.

A value the sender repeats never overrides a change the user made on the
host. Changed constructor fields construct a new instance, as Pi's
`new SelectList(...)` would.

### 4.3 Input routing (ui.custom only)

Only `ui.custom` takes input; widgets, header/footer and renderer output do
not, as in Pi. For a `ui.custom` frame whose view names a `focus`, the host
gives each key to the focused node when that node's upstream `handleInput`
acts on it:

- `select-list`: keys matching `tui.select.up`, `tui.select.down`,
  `tui.select.confirm`, `tui.select.cancel` (`select-list.ts:145-170`);
- `settings-list`: those keys and a space (`settings-list.ts:222-250`), plus
  every other key when `enableSearch` is set (the search input takes it), and
  every key while a submenu is open.

Every other key goes to the extension as `ui.custom.input`, as today. An
author who adds keys of their own (for example `d` to delete) still receives
them. Pi's common pattern (`handleInput(data) { list.handleInput(data) }`)
behaves the same way, since SelectList ignores keys it does not bind.

**Mouse (Pi parity).** In fullscreen `tuiMode` Pi routes a normalized
`TuiMouseEvent` to the component under the pointer, with `x`/`y` local to
it; regular mode routes none (`tui-alt-screen.ts`, `tui.ts` `handleMouse`).
PiG does the same for `ui.custom` components (overlay and editor slot). A
component that takes the mouse sets `mouse: true` on its `ui.custom.render`
frame, and the host sends it notify `ui.custom.mouse` with `{key, event}`,
the event in `TuiMouseEvent`'s JSON names (`type, button, x, y, screenX,
screenY, width, height, shift, alt, ctrl, wheelDelta?, clickCount?`). For a
view, the host first dispatches the event into its own tree of Pi components,
as Pi's Container, Box, SelectList and SettingsList would: a click on a
select-list row fires `selectionChange` and then `select` through
`ui.view.event` (§4.4), and only events the tree leaves unhandled reach the
component's handler. Events share the `HandleInput` serial queue. The SDKs
draw again after press, click, drag and wheel, as Pi's default `render`
does, but not after move or release. The host answers the terminal
synchronously, so a component that takes the mouse counts as handled for
every event in its bounds, and text selection does not start over it.
Widgets, header/footer and renderer views take no mouse, as they take no keys;
Node's widget, header/footer and tool-renderer components get none yet,
although Pi routes it to them (a recorded gap).

### 4.4 Events back to the extension (async contract)

Pi's component callbacks run synchronously inside `handleInput` on the TUI
loop, and the author then calls `tui.requestRender()` or `done(value)`:
`onSelect(item)`, `onCancel()`, `onSelectionChange(item)` for SelectList
(`select-list.ts:49-51, 145-170, 262-267`); `onChange(id, newValue)` and
`onCancel()` for SettingsList (`settings-list.ts:45-46, 222-300`).

PiG keeps the ordering and moves the callback across the process:

1. The host runs the component's `handleInput` on its UI loop. The state
   change is drawn in the same frame, so there is no round trip before the
   terminal shows the new selection.
2. Each callback the component fires becomes one `ui.view.event` notify, in
   firing order, on the extension's connection. It follows every
   `ui.custom.input` sent before it. Types: `select`, `cancel`,
   `selectionChange` (with `index` into the shown items and `item`), and
   `change` (with `id` and `value`). A settings submenu's own selection is not
   an event: it completes the submenu (`done(item.value)`, or `done()` on
   cancel) and becomes the settings list's `change`.
3. The SDK dispatches events on the same serial queue as `HandleInput`, so
   an event handler and input never run concurrently and keep arrival order.
   The host does not wait for a reply. A handler's result has
   `HandleInput`'s meaning: `Done` closes the overlay with its value, and an
   error closes it with that error.
4. Events for a surface that has closed are dropped on both sides.

Loader animation is host-owned for authoritative views, like D94's working
indicator. The host advances the frame every `intervalMs` while the surface
is mounted, and stops when the surface closes or the frame drops the loader.

## 5. Host rendering and validation

### 5.1 Registry

The host package `coding/extension/host/subprocess/viewkit` (renderer) turns
a decoded view into a `tui.Component` tree with exactly the Go ports listed
in §2. It applies the defaults, builds style functions from the surface's
theme (§6), and keeps interactive instances (§4.2). Rendering a view is
`root.Render(width)`. During the render it records each node's rows and
width for the frontend structure. The host renders a view only on a surface
it is laying out, never on the wire path.

Byte identity is proved by an exact-ANSI test. It renders the same component
tree through the kit and through the tui ports directly, at several widths,
and compares the bytes. The parity families `startup`,
`interactive-rendering` and `extensions-runtime` stay unchanged.

### 5.2 Validation

- **Authoritative view.** The schema must be valid: known kinds and tokens,
  required ids, bounds, image refs present (§7), and override colors valid
  (§6). An invalid view is rejected: the surface keeps its previous frame,
  and the host logs one diagnostic per surface. Typed SDK builders make most
  invalid views unrepresentable.
- **Annotated lines.** After the schema checks, the host renders the view at
  `view.width` and compares it with `lines`. Equal: the view is kept.
  Different: the view is dropped, and the frame is lines only. Either way the
  terminal draws `lines`, so the structure can never change terminal output.
  Validation runs only while a frontend is attached, because Node sends no
  view otherwise.

### 5.3 Width, seq and dedup

- `seq` and the stale-width rules for `ui.custom` frames are unchanged. A
  view travels in the frame it describes, under that frame's `seq` and width
  key.
- An authoritative view renders at the width the host lays the surface out
  at, with no request to the extension. The host caches the rendered lines
  per width, next to the view.
- Dedup compares lines and view together. SDKs compare the encoded view bytes
  with the last frame's as well as the lines. The host compares the raw view
  bytes and decodes only a changed view.

## 6. Theme tokens and per-surface overrides

- Component themes use Pi's token names through the same coding-agent theme
  functions (`getSelectListTheme`: `accent`, `muted`; `getSettingsListTheme`:
  `accent`, `muted`, `dim`; `getMarkdownTheme`: the `md*` tokens). Node
  tokens (`bg`, `color`, ...) name any foreground or background token of Pi's
  theme schema (`tui/theme_dark.json`), according to the slot.
- `view.theme` overrides tokens for this surface only. The set is closed:
  foreground `accent`, `muted`, `dim`, `text`, `border`, `borderAccent`,
  `borderMuted`, `success`, `warning`, `error`; background `selectedBg`,
  `customMessageBg`. Values are `#rrggbb`.
- The host renders an override through the active theme's color mode, as if
  the theme JSON set that token: a 24-bit SGR in truecolor and the nearest
  xterm color in 256-color mode. So the terminal shows the override too. Pi's
  equivalent is an author passing a style function of their own.
- An unknown token or a malformed color makes the view invalid (§5.2).
- A frontend applies its own theme for every token the view does not
  override, and the override for the ones it does. pig-music sets
  `accent` to the track color.

## 7. Image transport

- A `view.images` entry `{ref, mimeType, data}` carries the bytes (base64)
  the first time a frame on this connection references `ref`. `ref` is the
  lowercase hex SHA-256 of the decoded bytes. The host verifies it, and a
  mismatch makes the view invalid.
- After that, nodes refer to `ref` only. The SDK remembers which refs it has
  sent on this connection.
- Bounds: 8 MiB decoded per image, and 32 MiB and 64 images per connection.
  An image over the bound makes the view invalid.
- Eviction is least recently used, and only of refs that no live frame of the
  connection references. The host tells the SDK with `ui.view.evicted`, and
  the SDK forgets the refs. A frame that names a ref the host lacks is
  rejected as invalid (the previous frame stays). The host then sends
  `ui.view.evicted` for that ref, and the SDK sends the frame again with the
  data.
- A `lines` node's annotations (`image`, `progress`, `list`) are
  frontend-only. An SDK omits them, and the image bytes, when no frontend is
  attached.

## 8. The D91 shape and the frontend flag

`extensions/sdk/frontend` gains `View` and `ViewNode`, the decoded view with
layout:

- `ViewNode` carries `Kind`, `ID`, the §2 fields, `Rows` (the rows of its
  parent's lines it covers, in order) and `Width` (cells laid out). `Image`
  carries the decoded bytes. Hstack children carry their columns.
- `View` carries `Root`, `Theme` (the overrides) and `Focus`.

It is optional structure on existing nodes. The node set stays closed:

- `Lines.View` (main-region components and the dock blobs),
- `Overlay.View`,
- `ToolCard.ResultView` (next to `Result`).

A dock `Lines` blob gets a view only when one of its components has one. The
view is then a `container` of each component's view, or a `lines` range for a
component without one.

The conversation kinds (§2.1) carry the nodes PiG's main transcript reports
for the same component, so a frontend draws them with its transcript
renderers. `ViewNode.Transcript` holds them: a `user-message` has a
`MarkdownText` with `RoleUser`; an `assistant-message` has one
`MarkdownText` per text block and one `Thinking` per thinking run, in order,
with `Streaming` on its last part while `isStreaming`, and a `Lines` node for
its error line; a `tool-execution` has its `ToolCard`, with `Diff` once a
finished edit carries one. A `diff` node carries `ViewNode.Diff` (`Path` is
`filePath`, `Text` the diff). A `bash-execution` node has no transcript node,
as the main transcript reports a `!` command as `Lines`; a frontend draws its
rows.

The lines remain in every node, so a frontend that ignores `View`, or a node
kind it does not draw, falls back to them. A frontend that draws a view node
natively must draw the same information. Node equality compares a view by its
frame: the producer's `seq` and its last render width, not a tree walk.

`Env.Act` reaches interactive view nodes with `Action.ViewNode`. The kit id
goes in `ViewNode`, and the item is the select-list item's `value` or the
setting's `id`. The keys are computed from the view's selection and filter, as
for `Selector`/`Settings`, and travel through terminal input to the focused
node (§4.3). An action on a node that is not the view's `focus` does nothing.

`Env.Click` reaches view elements too (`pig additive (D91)`):
`Click.ViewPath` names a view node by the index of each child on its path,
so `Row` and `Column` count from that node's first cell, and
`Click.Item` names a select-list item's `value` or a setting's `id`. PiG turns
the click into the gesture a terminal sends at the cell that element occupies
in the fullscreen layout (`press`, then `release` and `click` to the component
that took the press; when none took it, `release` to the component under the
pointer and `click` when that leaves the release too), delivered through §4.3 to the same component with
the same local `x`/`y`. Clicks count as Pi counts a terminal's: one on the
cell and component of the click before it, within Pi's 500 ms double-click
interval, has `clickCount` 2, then 3. `Click.Count` is the session's own
count, such as Tern's `activate` after a double click's second click on a
list item (`Count` 2), which PiG delivers with `clickCount` 2 only when it
had not counted that click 2 already. A list item scrolled out of the window
is wheeled into view first. It works on overlay (`overlay.N`) and dock views
only while the run's `tuiMode` is fullscreen, and does nothing otherwise.
Reachable:
select-list items, settings-list rows (not inside an open submenu), annotated
`lines` list rows, and the first cell of text, box and other kit nodes. Not
reachable: interior cells of custom-drawn `ansi`/`lines` blocks and
transcript views.

**Frontend flag.** `StatePayload.frontend` is true while a frontend session
draws: set in `openFrontend` and cleared in `leaveFrontend`, and pushed with
`state_update`. Stock PiG never sets it. With it false:

- Node walks nothing and sends no view;
- Go, Rust and Python omit frontend-only `lines` annotations and their image
  bytes;
- the host builds no frontend structure, because `TuiSurface` does not exist.

Authoritative views are an author's choice of API: the host renders them in
every mode, because they are the content.

## 9. Per-SDK author API

Interface and behavior are identical across SDKs. Only naming follows each
language.

| capability | Go (`extensions/sdk`, `extensions/sdk/kit`) | Rust (`pig_sdk::kit`) | Python (`pig_sdk.kit`) |
|---|---|---|---|
| node constructors (upstream positional order and defaults) | `kit.NewContainer(...)`, `kit.NewBox(px, py)`, `kit.NewText(text, px, py)`, `kit.NewTruncatedText`, `kit.NewMarkdown(text, px, py)`, `kit.NewSpacer(n)`, `kit.NewDynamicBorder(token)`, `kit.NewSelectList(id, items, maxVisible)`, `kit.NewSettingsList(id, items, maxVisible)`, `kit.NewImage(data, mimeType)`, `kit.NewLoader(message)`, `kit.NewHStack`/`NewVStack`, `kit.NewLines(lines)` | `kit::Container::new()`, `kit::Text::new(text, px, py)`, … | `kit.Container(...)`, `kit.Text(text, padding_x=1, padding_y=1)`, … |
| a view | `kit.View{Root, Focus, Theme}` | `kit::View` | `kit.View` |
| ui.custom with a view | a component implementing `ViewComponent` (`View(width int) kit.View` + `HandleInput`), optionally `ViewEventHandler` (`HandleViewEvent(kit.Event) (RemoteComponentResult, error)`) | trait `ViewComponent` (`view`, `handle_input`, `handle_view_event`) | protocol `ViewComponent` (`view(width)`, `handle_input`), optionally protocol `ViewEventHandler` (`handle_view_event(event)`) |
| mouse (ui.custom, fullscreen) | optional `sdk.MouseHandler` (`HandleMouse(sdk.MouseEvent) (RemoteComponentResult, error)`) on a `RemoteComponent` or `ViewComponent` | `handles_mouse` + `handle_mouse(&MouseEvent)` on `RemoteComponent`/`ViewComponent` | protocol `pig_sdk.MouseHandler` (`handle_mouse(MouseEvent)`) |
| widget | `ctx.SetWidget(key, kit.View, opts...)` | `ctx.set_widget_view(key, view, opts)` | `ctx.set_widget(key, kit.View, options)` |
| header/footer | `ctx.SetHeaderView(view)`, `ctx.SetFooterView(view)` | `set_header_view`/`set_footer_view` | `set_header_view`/`set_footer_view` |
| tool renderers | `ToolRenderers.CallView`, `ToolRenderers.ResultView` (returning `kit.View`, same `ToolRenderContext` and width as the line forms), on `SetToolRenderers`, resolver results and tool definitions alike; a view form declares the phase rendered and wins over a line form set for the same phase | `ToolRendererSet { call_view, result_view }`, tool `render_call_view`/`render_result_view`, `ext.render_tool_call_view`/`render_tool_result_view` | `ToolRenderers(call_view=, result_view=)`, `ToolDefinition(call_view=, result_view=)`, `ext.tool_renderers(..., call_view=, result_view=)` |
| message/entry renderers | `ext.MessageViewRenderer(customType, fn)`, `ext.EntryViewRenderer(customType, fn)` | `message_view_renderer`/`entry_view_renderer` | `message_view_renderer`/`entry_view_renderer` |
| events | `kit.Event{Node, Type, Index, Item, ID, Value}` | `kit::Event` | `kit.Event` |
| `lines` annotations | `kit.Lines{Image, Progress, List}`, `kit.List{Items []kit.ListItem{Label, Detail, Columns}, Selected}` | `kit::Lines::new(content)` with `.image(data, mime)`, `.progress(value, max)`, `.list(kit::List)` (`kit::ListItem`; `selected: None` is `-1` on the wire) | `kit.Lines(content, list=kit.List(...))` with `.image(data, mime_type)`, `.progress(value, max)` (`kit.ListItem`; `selected=-1` is none) |
| conversation kinds (§2.1) | `kit.NewUserMessage(text)`; `kit.NewAssistantMessage(message *kit.Message)` with `UpdateContent(message, isStreaming)`, `SetHideThinkingBlock`, `SetHiddenThinkingLabel`, `SetOutputPad`, `kit.Message{Content, StopReason, ErrorMessage}`, `kit.TextBlock`/`ThinkingBlock`/`ToolCallBlock`; `kit.NewToolExecution(toolName, toolCallID, args, cwd)` with `UpdateArgs`, `MarkExecutionStarted`, `SetArgsComplete`, `UpdateResult(kit.ToolResult, isPartial)`, `SetExpanded`, `SetShowImages`, `SetImageWidthCells`, `kit.TextContent`/`ImageContent`, `ToolDefinition` (`kit.ToolDefinitionBuiltin`, `kit.ToolDefinitionEmpty`); `kit.NewBashExecution(command, excludeFromContext)` with `AppendOutput`, `SetComplete(exitCode *int, cancelled, truncated, fullOutputPath)`, `SetExpanded`; `kit.NewDiff(diff)`; an `ID` field keeps a node's host component (§2.1) | `kit::UserMessage::new`, `kit::AssistantMessage::new(Option<kit::Message>)` with `update_content`, `set_hide_thinking_block`, `set_hidden_thinking_label`, `set_output_pad`, `kit::Message { content, stop_reason, error_message }`, `kit::ContentBlock::{text, thinking, tool_call}`; `kit::ToolExecution::new(tool_name, tool_call_id, args, cwd)` with `.tool_definition(kit::ToolDefinition::{Builtin, Empty})`, `update_args`, `mark_execution_started`, `set_args_complete`, `update_result(kit::ToolResult, is_partial)`, `set_expanded`, `set_show_images`, `set_image_width_cells`, `kit::ToolResultContent::{text, image}`; `kit::BashExecution::new(command, exclude_from_context)` with `append_output`, `set_complete(Option<i64>, cancelled, truncated, full_output_path)`, `set_expanded`; `kit::Diff::new(diff)` with `.file_path(..)`, `.padding(x, y)`; `.id(..)` | `kit.UserMessage(text, output_pad=1)`; `kit.AssistantMessage(message=None, *, id="")` with `update_content(message, is_streaming)`, `set_hide_thinking_block`, `set_hidden_thinking_label`, `set_output_pad`, `kit.Message(content, stop_reason, error_message)`, `kit.text_block`/`thinking_block`/`tool_call_block`; `kit.ToolExecution(tool_name, tool_call_id="", args=None, cwd="", *, tool_definition=kit.TOOL_DEFINITION_BUILTIN, id="")` with `update_args`, `mark_execution_started`, `set_args_complete`, `update_result(kit.ToolResult, is_partial)`, `set_expanded`, `set_show_images`, `set_image_width_cells`, `kit.text_content`/`image_content`, `kit.TOOL_DEFINITION_EMPTY`; `kit.BashExecution(command, exclude_from_context=False, *, id="")` with `append_output`, `set_complete(exit_code, cancelled, truncated, full_output_path)`, `set_expanded`; `kit.Diff(diff, *, file_path="", padding_x=0, padding_y=0)` |

The `*View` forms are additions next to the line forms. Every existing
signature is unchanged. A line-returning author keeps today's wire exactly.

**Node automatic derivation** (`runtime-node/runtime.mjs`, after
`component.render(width)` and only while `state.frontend`):

- A component maps to a kind only when `component.render` is exactly the
  base class's `render` (`Container`, `Box`, `Text`, `TruncatedText`,
  `Markdown`, `Spacer`, `SelectList`, `SettingsList`, `Image`, `HStack`,
  `VStack`, coding-agent `DynamicBorder`), or `Loader.prototype.render` for
  `loader`. A subclass that overrides `render`, and any other component, is
  a `lines` range. Its rows come from the parent's `mouseLayout` (Container,
  Box) or from rendering it again only when no layout exists.
- A style closure maps to a token only when its output on a probe string
  equals `theme.fg(token, probe)`/`theme.bg(token, probe)` of the current
  theme. A component theme maps only when it equals coding-agent's
  `getSelectListTheme()`/`getSettingsListTheme()`/`getMarkdownTheme()` on
  probes. Anything else makes that component a `lines` range. Node declares
  no overrides, because Pi has none.
- A field that only a closure reaches (`truncatePrimary`, `transform`,
  `visible`, a submenu factory) makes that component a `lines` range.
  Exception: a settings item's `submenu` closure is reported as the marker
  `{"kind":"lines"}` (no content), since the closed list renders the same
  either way. While a submenu is open, the whole list is a `lines` range.
- The walk reads fields pi-tui marks private. A pin test fails when an
  upstream upgrade renames one.
- Host validation (§5.2) is the backstop: a mismatch costs only the
  structure.
- The conversation components map as follows (pinned fields as above):
  `UserMessageComponent` to `user-message`, `AssistantMessageComponent` to
  `assistant-message` (from `lastMessage`, with no click toggle set),
  `ToolExecutionComponent` to `tool-execution` when its definition's
  `renderCall`, `renderResult` and `renderShell` are those of the built-in
  tool definition for its name (`"builtin"`) or are all absent (`"empty"`),
  `BashExecutionComponent` to `bash-execution` (`excludeFromContext` from its
  border's color, `output` from its output lines, `frame` from its loader),
  and a `Text` whose text is a `renderDiff` result the runtime recorded to
  `diff`. Each requires the default markdown theme and no markdown
  transformer; anything else is a `lines` range.

**Node author-supplied annotations and overrides.** Pi's components cannot
say what a frontend-only annotation would, so Node authors declare them on
the component, as PiG-only properties typed in `extensions/sdk-ts`: a
component whose rows become a `lines` range may set `viewLines` (`{image?,
progress?, list?}`, the §2 shapes, with `image` as `{data, mimeType}`), and a
surface's root component may set `viewTheme` (§13.3). The runtime sends them
only while `state.frontend`, except that `viewTheme` also styles the
terminal rows in every mode.

## 10. Conformance

Row `TestConformance_ComponentKitRendersPiComponents`
(`test/extension-conformance/component_kit_test.go`):

- **Fixture command `kit-probe`** in each SDK opens `ui.custom` with a
  `container` holding a `dynamic-border` (accent), a `text` with
  `paddingX=2`, `paddingY=0`, `bg=customMessageBg`, a `markdown` with a list
  and bold text, an `hstack` of two `truncated-text` with `grow`, a `spacer`
  and a `select-list`. The select-list has id `kit-tracks`, `maxVisible=3`,
  five items with descriptions, `selectedIndex=2`, `focus="kit-tracks"`, and
  `theme={"accent":"#d75f00"}`.
- **Distinguishable values.** The reference rows are the inproc-go tree built
  from the `tui` ports directly with the same arguments. Rows rendered at
  widths 30, 72 and 120 must match byte for byte. The first frame must show
  item 3 selected in `#d75f00`. A fallback cannot pass: an SDK that sends no
  view shows nothing, lines from a Text-layout fallback differ, and the
  default accent differs from the override.
- **Interaction.** Keys `down`, `down`, `down` (wraps to item 0), `x`,
  `enter`. The fixture logs each `HandleViewEvent` and `HandleInput` call,
  and closes on `select` with the log joined by `,`. Expected result:
  `selectionChange:3:k3,selectionChange:4:k4,selectionChange:0:k0,input:x,select:0:k0`.
  The rendered rows after each key must equal the reference's rows after the
  same `handleInput` calls. `x` must reach `HandleInput` as `ui.custom.input`,
  because the list does not bind it.
- **Matrix:** inproc-go (reference), subprocess-go, fused-go, packed-go. The
  row adds subprocess-rust, subprocess-python, packed-rust and packed-python
  when those SDKs land, and subprocess-node and subprocess-node-packed with a
  frontend-attached harness when the Node walk lands. It is added per SDK as
  each lands. No skipped placeholders.
- **Further rows:**
  - `TestConformance_ComponentKitWidgetHeaderFooterAndRenderers`: the same
    tree through `setWidget`, `setHeaderView`, `ToolRenderers.ResultView` and
    the message renderer.
  - `TestConformance_ComponentKitImagesSendOnce`: the second frame carries no
    data; after `ui.view.evicted` the next frame carries it again.
  - `TestConformance_ComponentKitConversationKinds`: fixture command
    `kit-conversation` sets the widget `kit-conversation` to a container of a
    user message, a streaming assistant message with thinking (id `kit-a1`),
    an errored one with hidden thinking under the label `Pondering kit...`
    and `outputPad` 0, tool cards for `ls` (complete, id `kit-t1`), `grep`
    (partial), `read` (error, expanded) and `kit_tool` (`"empty"`), bash
    commands that exit 2 and that hide five lines, and a diff. Its rows must
    equal, at every kit width, the rows of the same components built with the
    ports the main transcript uses; a Node cell without a frontend draws Pi's
    own components, so the row also compares Pi's bytes with the ports'. With
    a frontend the view must carry each kind's transcript nodes. A second
    `kit-conversation next` frame updates the kept nodes and must draw the
    same rows as fresh components in that state.
- **Gates:**
  - `wire_capability_coverage_test.go` lists `ui.view.event` and
    `ui.view.evicted`, with each SDK's symbol;
  - `test/parity/sdk-surface.toml` gains the kit rows;
  - `sdk-surface-exceptions.toml` holds the Rust/Python/Node cells until they
    land, each with a reason naming its lane.

## 11. Records

- **D107**, recorded in `docs/parity/DIVERGENCE-IDS.txt`. D100-D105 are taken
  on the 0.4.2 integration branch and D106 was revoked, so the kit takes D107
  (coordination note from the 0.4.2 lead): "the
  extension component kit: Go, Rust and
  Python extensions describe Pi's tui components as a view the host renders
  with its tui ports; Node's components are read into the same view; a D91
  frontend receives it as optional structure (additive); tern-d91-extview;
  approved by owner Michael Kinsy 2026-10-06."
- `docs/additive-features.md` § D107:
  - Stock disposition: required substrate for non-Node SDK component parity.
    The authoritative-view path is Pi-parity for Go/Rust/Python authors. It
    closes the "Custom TUI components" gap. The frontend structure is
    additive and inert without a frontend.
  - Pi source: §2 citations.
  - Stock behavior: no frontend, so no Node view and no structure. Terminal
    bytes are unchanged, proved by the exact-ANSI test and the parity
    families.
  - Remove when: Pi gains a cross-process component protocol.
- **D91 amendment** (append to D91's text): "An extension surface may also
  carry its component structure (D107): `Lines.View`, `Overlay.View` and
  `ToolCard.ResultView` describe the lines as Pi's tui components (container,
  box, text, truncated text, markdown, spacer, dynamic border, select list,
  settings list, image, loader, stacks and opaque line ranges) and Pi's
  conversation components (user and assistant messages with thinking, tool
  executions, bash executions and diffs), with the rows each covers, the
  extension's per-surface theme overrides and the focused list. A
  conversation node carries the transcript nodes the main transcript reports
  for it (`ViewNode.Transcript`, `ViewNode.Diff`). The lines stay
  authoritative; a frontend that does not draw a view, or one of its kinds,
  draws the lines. `Env.Act` reaches a view's focused list through
  `Action.ViewNode`. `StatePayload.frontend` tells extensions a frontend
  draws, so Node derives views only then."
- `docs/extension-api-parity.md`: replace the "Custom TUI components" gap
  with the kit's parity row and its async contract (§4.4), and add the
  SDK coverage matrix rows.

## 12. Risks

- **Port fidelity.** For authoritative views, terminal parity depends on
  PiG's ports. `SelectList` is a new port and gets an upstream-derived test.
  `SettingsList` gains Pi's theme argument. A port bug shows in both the
  kit and the native PiG UI, and is fixed at the port.
- **Host-owned state.** An author cannot read the selection synchronously,
  because it arrives as events. Pi authors usually read it in `onSelect` and
  `onSelectionChange` anyway; `getSelectedItem()` has no remote equivalent.
  This is recorded in the parity row.
- **Key routing.** An author who intercepts a list's bound keys before the
  list (for example to veto `up`) cannot do so for an authoritative view.
  This is recorded.
- **Node private fields.** These are pinned by test; the validation
  backstop limits the damage to lost structure.
- **Frame size.** Views add JSON per frame at the 16 ms throttle. Images are
  sent once. Node pays only with a frontend attached.
- **Closures.** `truncatePrimary`, `transform`, `visible` and `navigateTo` are
  not expressible for Go, Rust and Python. This is recorded as an interface
  limitation of the kit, not drift between SDKs, since all three share it.

## 13. Resolved questions

Decided by the coordinator on 2026-10-06:

1. `accent2`: no PiG-only token. pig-music has no second accent, so nothing
   maps to one. Revisit only when a real extension needs one.
2. Track tables: the frontend-only `list` annotation on `lines` (§2), with
   `columns` for the table cells. The rows stay the extension's own.
3. Node per-surface overrides: yes, all four SDKs have them. A surface's
   root component may set the PiG-only `viewTheme` ({token: "#rrggbb"}, the
   §6 set), typed in `extensions/sdk-ts` by augmenting pi-tui `Component`.
   The runtime renders that surface with those tokens overridden in every
   mode (`tui.WithTokenColors` semantics: the palette's color mode, faint
   tokens stay faint). While a frontend draws, it sends them as
   `view.theme`. An invalid entry is not applied, and the host rejects the
   view. The conformance row covers it with a value distinguishable from the
   default theme.
4. Rejected views are never written to raw stderr while the TUI or a
   frontend owns the terminal. The host reports once per surface through the
   extension diagnostic path the TUI already shows for extension errors, and
   writes to stderr only in non-interactive modes, where stderr is the
   established channel.
