/// <reference path="./pi-pin.d.ts" />
import type { MarkdownTransformContext, Theme } from "@earendil-works/pi-coding-agent";
import "@earendil-works/pi-tui";

export type * from "@earendil-works/pi-coding-agent";

/**
 * Data rendered by PiG's fixed native login template.
 *
 * The host validates grid dimensions, palette symbols, colors, and display
 * widths before replacing the current header.
 */
export interface PiGLoginDefinition {
  brand: string[];
  hero: string[];
  mascot: string[];
  palette: Record<string, string>;
  name: string;
  description: string;
  tagline: string;
}

/**
 * One sprite for PiG's /sprite catalogue: the 16-by-14 pig the startup header
 * draws and /sprite preview shows beside the wordmark, colored by the palette. The host validates the grids, palette symbols, colors and text.
 */
export interface PiGSpriteDefinition {
  id: string;
  name: string;
  tagline: string;
  mascot: string[];
  palette: Record<string, string>;
}

/** A Pi theme token a surface may override (PiG component kit, D107 §6). */
export type PiGViewThemeToken =
  | "accent"
  | "muted"
  | "dim"
  | "text"
  | "border"
  | "borderAccent"
  | "borderMuted"
  | "success"
  | "warning"
  | "error"
  | "selectedBg"
  | "customMessageBg";

/**
 * Token overrides for one surface, each a `#rrggbb` color. PiG renders the
 * surface with them in every mode, escaped in the theme's color mode; a faint
 * token stays faint. An unknown token or a malformed color is applied
 * nowhere, and a frontend rejects the surface's view.
 */
export type PiGViewTheme = Partial<Record<PiGViewThemeToken, `#${string}`>>;

/** One row of a {@link PiGViewList}: its primary text, a secondary text, and further table cells. */
export interface PiGViewListItem {
  label: string;
  detail?: string;
  columns?: string[];
}

/** Says a component's rows are a list: `items[i]` is row `i`. `selectedIndex` is the highlighted item, or -1. */
export interface PiGViewList {
  items: PiGViewListItem[];
  selectedIndex: number;
}

/**
 * What a component's rows show, for a frontend drawing them natively. PiG
 * sends it only while a frontend draws, and only for a component it cannot
 * read structure from (a custom `render`). The terminal always draws the rows.
 */
export interface PiGViewLines {
  /** The rows depict this image (base64 data). */
  image?: { data: string; mimeType: string };
  /** The rows show a position in a range. */
  progress?: { value: number; max: number };
  /** The rows are a list, one item per row; a list whose item count differs from the rows is rejected. */
  list?: PiGViewList;
}

/** The provenance the loader stamped on an extension (Pi's `SourceInfo`). */
export interface PiGExtensionSourceInfo {
  path: string;
  source: string;
  scope: string;
  origin: string;
  baseDir?: string;
}

/** D614: where a client draws a plugin mount. */
export type PiGViewSlot = "notify" | "status" | "widget-above" | "widget-below" | "header" | "footer" | "dialog" | "overlay" | "panel" | "tool-card" | "message" | "entry";
/** D614: the closed JSON subset an intent argument or answer is checked against (depth 4, enum at most 32, object at most 32 properties, array at most 64 items). */
export interface PiGViewShape {
  type: "boolean" | "string" | "integer" | "number" | "enum" | "object" | "array";
  enum?: string[];
  properties?: Record<string, PiGViewShape>;
  required?: string[];
  items?: PiGViewShape;
  maxItems?: number;
}
export interface PiGViewIntent {
  /** "<extension>.<verb>". */
  name: string;
  nodeKey?: string;
  arg: PiGViewShape;
  answer?: PiGViewShape;
  cancellable?: boolean;
  /** 100..300000; default 30000. */
  timeoutMs?: number;
  /**
   * Lets a client raise the intent while the mount is hidden: it is how the user opens a view the extension hid, and the
   * handler shows the mount. At most one intent of a mount has it; it names no node and its `arg` accepts `{}`.
   */
  whenHidden?: boolean;
  /**
   * Names the intent a client raises, with `{}`, when the user dismisses a dialog or overlay mount: the handler closes
   * the mount. At most one intent of a mount has it; it names no node and its `arg` accepts `{}`. Without one, Escape
   * leaves the mount and it stays open until the extension closes it.
   */
  dismiss?: boolean;
}
export interface PiGViewAsset {
  id: string;
  mime: "image/png" | "image/jpeg" | "image/gif" | "image/webp";
  size: number;
  sha256: string;
  delivery: "inline" | "managed";
}
/**
 * D107: the bytes of one image of a {@link PiGWireView}, in its `images` list. All three members are required. `ref` is the lowercase hex
 * SHA-256 of the decoded bytes, `mimeType` is the image's media type, and `data` is the bytes in base64 (at most 8 MiB decoded). The host
 * rejects the view when `ref` does not match the bytes.
 */
export interface PiGViewImage {
  ref: string;
  mimeType: string;
  data: string;
}
/**
 * D614: the `image` node of the D107 view, Pi's `Image` component. It names its bytes by `ref`; both `ref` and `mimeType` are required. Put the
 * bytes in the `images` list of the view that first names them. A later view may name an image the host still holds by its `ref` alone and
 * leave the record out of `images`; a view that names a `ref` the host does not hold is rejected. `maxWidthCells` is 60 when absent.
 * `fallbackColor` is a theme token of the text the node shows when the client cannot draw the image.
 */
export type PiGViewImageNode = {
  kind: "image";
  id?: string;
  ref: string;
  mimeType: string;
  maxWidthCells?: number;
  maxHeightCells?: number;
  imageId?: number;
  filename?: string;
  fallbackColor?: string;
};
/**
 * The D107 wire view, `{ root: { kind: "text", text: "..." } }`. A node of any kind may carry `id`, a string unique in the view: an
 * a11y, focus or drawing record names a node by it. The colour of a `text` or a `box` node is `bg`, a background token; neither has a
 * `color` member, and the host draws none it is sent.
 */
export type PiGWireView = { root: PiGViewNode; focus?: string; theme?: Record<string, string>; images?: PiGViewImage[] };
/** D107: the options of a child of an `hstack` or a `vstack` (Pi's `StackEntryOptions` without `visible`); it rides on the child as `stack`. */
export type PiGViewStackEntry = { basis?: number; grow?: number; shrink?: number; minSize?: number; maxSize?: number };
/** D107: a theme-token style of a `markdown` node (Pi's `DefaultTextStyle`): `color` is a foreground token, `bgColor` a background token. */
export type PiGViewTextStyle = { color?: string; bgColor?: string; bold?: boolean; italic?: boolean; strikethrough?: boolean; underline?: boolean };
/** D107: one item of a `select-list` (Pi's `SelectItem`). */
export type PiGViewSelectItem = { value: string; label?: string; description?: string };
/** D107: one item of a `settings-list` (Pi's `SettingItem`); a `submenu` is a node. */
export type PiGViewSettingItem = { id: string; label?: string; description?: string; currentValue?: string; values?: string[]; submenu?: PiGViewNode };
/** D107: a loader's indicator (Pi's `LoaderIndicatorOptions`): no `frames` is the default spinner and `frames: []` hides it. */
export type PiGViewLoaderIndicator = { frames?: string[]; intervalMs?: number };
/** D107: an editor's or an input's cursor: a logical line and a UTF-16 column. */
export type PiGViewCursor = { line: number; col: number };
/** D107: one block of an assistant message (`text`, `thinking` or `toolCall`) or of a tool result (`text`, or `image` with `ref` and `mimeType`). */
export type PiGViewContentBlock = { type: string; text?: string; thinking?: string; ref?: string; mimeType?: string };
/** D107: the members every node kind shares. `id` names the node (required for `select-list`, `settings-list` and `grid`), `stack` sets this node's options inside an `hstack` or a `vstack`. */
export type PiGViewNodeBase = { id?: string; stack?: PiGViewStackEntry };
/** D107: the node kinds of the host's view contract, discriminated by `kind`. A member a kind does not have is a type error. */
export type PiGViewNode =
  | (PiGViewNodeBase & { kind: "container"; children?: PiGViewNode[] })
  | (PiGViewNodeBase & { kind: "box"; children?: PiGViewNode[]; paddingX?: number; paddingY?: number; bg?: string })
  | (PiGViewNodeBase & { kind: "text"; text?: string; paddingX?: number; paddingY?: number; bg?: string })
  | (PiGViewNodeBase & { kind: "truncated-text"; text?: string; paddingX?: number; paddingY?: number })
  | (PiGViewNodeBase & { kind: "markdown"; text?: string; paddingX?: number; paddingY?: number; defaultTextStyle?: PiGViewTextStyle; renderLatex?: boolean })
  | (PiGViewNodeBase & { kind: "spacer"; lines?: number })
  | (PiGViewNodeBase & { kind: "dynamic-border"; color?: string })
  | (Omit<PiGViewNodeBase, "id"> & { kind: "select-list"; id: string; items?: PiGViewSelectItem[]; maxVisible?: number; layout?: { minPrimaryColumnWidth?: number; maxPrimaryColumnWidth?: number }; selectedIndex?: number; filter?: string })
  | (Omit<PiGViewNodeBase, "id"> & { kind: "settings-list"; id: string; items?: PiGViewSettingItem[]; maxVisible?: number; enableSearch?: boolean; selectedIndex?: number; filter?: string })
  | (PiGViewNodeBase & PiGViewImageNode)
  | (PiGViewNodeBase & { kind: "loader" | "cancellable-loader" | "bordered-loader"; message?: string; spinnerColor?: string; messageColor?: string; indicator?: PiGViewLoaderIndicator; frame?: number; cancellable?: boolean })
  | (PiGViewNodeBase & { kind: "hstack" | "vstack"; children?: PiGViewNode[]; gap?: number; align?: "stretch" | "start" | "center" | "end" })
  | (PiGViewNodeBase & { kind: "lines"; content?: string[]; image?: { ref: string }; progress?: { value: number; max: number }; list?: { items: { label: string; detail?: string; columns?: string[] }[]; selectedIndex: number } })
  | (PiGViewNodeBase & { kind: "user-message"; text?: string; outputPad?: 0 | 1 })
  | (PiGViewNodeBase & { kind: "assistant-message"; message?: { content: PiGViewContentBlock[]; stopReason?: "stop" | "length" | "toolUse" | "error" | "aborted"; errorMessage?: string }; outputPad?: 0 | 1; hideThinkingBlock?: boolean; hiddenThinkingLabel?: string; isStreaming?: boolean })
  | (PiGViewNodeBase & { kind: "tool-execution"; toolName: string; toolCallId?: string; args?: unknown; toolDefinition?: "builtin" | "empty"; cwd?: string; showImages?: boolean; imageWidthCells?: number; executionStarted?: boolean; argsComplete?: boolean; expanded?: boolean; result?: { content: PiGViewContentBlock[]; isError?: boolean; details?: unknown; durationMs?: number }; isPartial?: boolean; outputPad?: number })
  | (PiGViewNodeBase & { kind: "bash-execution"; command?: string; excludeFromContext?: boolean; output?: string; complete?: { exitCode?: number; cancelled?: boolean; truncated?: boolean; fullOutputPath?: string; fullOutputError?: string }; expanded?: boolean; outputPad?: number })
  | (PiGViewNodeBase & { kind: "diff"; diff?: string; filePath?: string })
  | (PiGViewNodeBase & { kind: "scroll-view"; children: [PiGViewNode]; follow?: "none" | "end"; primary?: boolean; overscroll?: "chain" | "contain"; scrollbar?: "hidden" | "auto" | "always"; scrollbarTrackColor?: string; scrollbarThumbColor?: string; scrollbarHideDelayMs?: number; scrollTop?: number })
  | (PiGViewNodeBase & { kind: "mouse-region"; children: [PiGViewNode]; region?: { types?: string[]; handled?: boolean; capture?: boolean; focus?: boolean; render?: boolean } })
  | (PiGViewNodeBase & { kind: "input"; value?: string; prompt?: string; placeholder?: string; placeholderColor?: string; cursor?: PiGViewCursor; focused?: boolean })
  | (PiGViewNodeBase & { kind: "editor"; value?: string; cursor?: PiGViewCursor; focused?: boolean; autocompleteMaxVisible?: number; borderColor?: string; disableSubmit?: boolean; history?: string[]; autocomplete?: { commands?: { name: string; description?: string; argumentHint?: string }[]; basePath?: string } })
  | (PiGViewNodeBase & { kind: "branch-summary"; summary?: string })
  | (PiGViewNodeBase & { kind: "compaction-summary"; summary?: string; tokensBefore?: number; expanded?: boolean })
  | (PiGViewNodeBase & { kind: "custom-message"; customType?: string; text?: string; expanded?: boolean })
  | (PiGViewNodeBase & { kind: "skill-invocation"; skill?: { name?: string; content?: string }; expanded?: boolean })
  | (PiGViewGrid & Pick<PiGViewNodeBase, 'stack'>);
/** D614: one event of a grid cell: its text (at most 256 bytes) and its colour (a theme token, "#rrggbb" or "#rrggbbaa"). */
export type PiGViewGridChip = { text: string; color?: string };
/**
 * D614: one cell of a {@link PiGViewGrid}. `value` names it in the intent a choice raises (at most 256 bytes, unique in the grid; a cell
 * without one cannot be chosen), `label` is its first line (at most 64 bytes), `mark` draws it as today, `dim` as outside the shown range,
 * `color` tints the label, `chips` are the events it shows (at most 8) and `more` counts the events the sender left out (0..9999).
 */
export type PiGViewGridCell = { value?: string; label?: string; mark?: "current"; dim?: boolean; color?: string; chips?: PiGViewGridChip[]; more?: number };
/**
 * D614: the grid node of the D107 view, a calendar-style grid of equal columns whose cells are data. Use it as the `root` of a
 * {@link PiGWireView} or as a child of a container node. The host is its only validator: `columns` is 1..16, `head` is absent or one label a
 * column (64 bytes each), `chipRows` 1..8 (default 3), at most 128 cells in at most 32 rows, `selected` one of the cell values, and a member
 * the record lacks rejects the call with code `invalid`. An intent whose `nodeKey` is the grid's `id` is raised with `{ value }` of the cell
 * the user chose.
 */
export type PiGViewGrid = { kind: "grid"; id: string; columns: number; head?: string[]; chipRows?: number; selected?: string; cells: PiGViewGridCell[] };
/** One fact the attached client states about itself, named "<area>.<fact>". */
export interface PiGViewClaim {
  name: string;
  value: boolean | number | string | string[];
}
/** Tests one claim: present, equals (boolean, integer or string), contains (every listed string is claimed) or atLeast (integer). */
export interface PiGViewClause {
  claim: string;
  op: "present" | "equals" | "contains" | "atLeast";
  value?: boolean | number | string | string[];
}
/** Holds when any group holds; a group holds when all its clauses do. At most 8 groups of 8 clauses. */
export interface PiGViewWhen {
  any: { all: PiGViewClause[] }[];
}
export interface PiGViewStroke {
  color: string;
  width: number;
}
/** One shape of a scene; a member of another kind rejects the drawing. A color is "#rrggbb", "#rrggbbaa" or a theme color token. */
export interface PiGViewDrawShape {
  kind: "group" | "rect" | "ellipse" | "line" | "polyline" | "path" | "text" | "image";
  key?: string;
  fill?: string;
  stroke?: PiGViewStroke;
  opacity?: number;
  /** Six numbers. */
  transform?: number[];
  children?: PiGViewDrawShape[];
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  radius?: number;
  cx?: number;
  cy?: number;
  rx?: number;
  ry?: number;
  x1?: number;
  y1?: number;
  x2?: number;
  y2?: number;
  /** 2..1024 vertices of a polyline. */
  points?: { x: number; y: number }[];
  closed?: boolean;
  /** 1..1024 steps of a path; the first is a move. */
  commands?: { op: "move" | "line" | "quad" | "cubic" | "close"; x?: number; y?: number; x1?: number; y1?: number; x2?: number; y2?: number }[];
  text?: string;
  size?: number;
  align?: "start" | "center" | "end";
  asset?: string;
}
/** Turns a gesture inside `shape` (a rect, an ellipse or a closed polyline) into a declared intent; the client hit-tests. */
export interface PiGViewRegion {
  key: string;
  shape: PiGViewDrawShape;
  gestures: ("activate" | "press" | "release" | "drag")[];
  intent: string;
  name: string;
  role?: string;
}
/** A scene of at most 4096 shapes nested 8 deep, or a pixel grid over an image asset, with at most 64 regions. */
export interface PiGViewDrawing {
  profile: "scene" | "pixels";
  /** The id of the view node the drawing fills. */
  node?: string;
  viewport?: { x: number; y: number; width: number; height: number; fit: "contain" | "cover" | "stretch" };
  shapes?: PiGViewDrawShape[];
  pixels?: { width: number; height: number; asset: string; scaling: "nearest" | "smooth" };
  regions?: PiGViewRegion[];
}
/** A view a client is sent in place of the mount's view when `when` holds for that client's claims. */
export interface PiGViewAlternative {
  when: PiGViewWhen;
  view: PiGWireView;
  drawing?: PiGViewDrawing;
}
/** Describes the view node with id `node` to assistive technology. */
export interface PiGViewA11y {
  node: string;
  name?: string;
  description?: string;
  role?: string;
  selected?: boolean;
  checked?: boolean;
  expanded?: boolean;
  busy?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  required?: boolean;
  invalid?: boolean;
  relations?: { kind: "labelledBy" | "describedBy" | "controls" | "owns"; target: string }[];
}
/**
 * Makes the node with id `node` focusable; the order of the entries is the focus order. `request` asks for the focus when
 * the mount is first drawn (`mount`) or when the revision that carries it is first drawn (`update`); a client that does not
 * give it leaves an `unfocused` notice.
 */
export interface PiGViewFocus {
  node: string;
  request?: "mount" | "update";
  restore?: "previous" | "self";
}
/** A gesture a binding may name. */
export type PiGViewGesture = "swipe-left" | "swipe-right" | "swipe-up" | "swipe-down";
/**
 * Ties a key or a gesture to an intent of the same content that names no node. A client fires it only while the mount holds
 * the focus and the focused node did not take the key. `key` is a key id as `registerShortcut` writes it (`"d"`,
 * `"shift+left"`, `"ctrl+shift+p"`); `arg` is the constant argument the intent is raised with (`{}` when absent) and `name`
 * what a client shows for the binding.
 */
export type PiGViewBinding = ({ key: string; gesture?: never } | { gesture: PiGViewGesture; key?: never }) & {
  intent: string;
  arg?: unknown;
  name?: string;
};
/** One diagnostic of a mount; `count` is how often it happened. */
export interface PiGViewNotice {
  code: "invalid" | "unsupported" | "limit" | "stale" | "terminal" | "refused" | "failed" | "timeout" | "undrawn" | "unfocused";
  severity: "info" | "warning" | "error";
  message?: string;
  path?: string;
  claim?: string;
  intent?: string;
  measured?: number;
  limit?: number;
  epoch: number;
  rev?: number;
  count: number;
}
export interface PiGViewContent {
  /** The short text a client labels the mount with, in a tab or a list of views: at most 64 bytes on one line. */
  title?: string;
  view: PiGWireView;
  intents?: PiGViewIntent[];
  assets?: PiGViewAsset[];
  fallback?: Record<string, unknown>;
  /** At most 8; the first whose `when` holds for a client's claims is what that client draws. */
  alternatives?: PiGViewAlternative[];
  drawing?: PiGViewDrawing;
  /** At most 256 entries. */
  a11y?: PiGViewA11y[];
  /** At most 256 entries; at most one requests the focus. */
  focus?: PiGViewFocus[];
  /** At most 32 entries; a key or a gesture is bound once. */
  bindings?: PiGViewBinding[];
}
export interface PiGViewMount extends PiGViewContent {
  key: string;
  slot: PiGViewSlot;
  target?: { toolCallId?: string; messageType?: string; entryType?: string };
}
/** One revision of one mount incarnation: pass the ref the last operation returned to the next update. */
export interface PiGViewRef {
  key: string;
  epoch: number;
  rev: number;
}
export interface PiGViewStatus {
  mount: PiGViewRef;
  state: "live" | "hidden" | "finished" | "removed" | "broken";
  /** Set for `broken`. */
  reason?: "session_replaced" | "owner_gone" | "shutdown";
  /** The result a finished mount was given. */
  result?: unknown;
}
/** What the attached client draws. */
export interface PiGViewAbilities {
  slots: PiGViewSlot[];
  nodes: string[];
  markdown: boolean;
  imageFormats: string[];
  limits: { nodes: number; depth: number; snapshotBytes: number; textBytes: number; mounts: number; intentArgBytes: number };
  input: { keyboard: boolean; pointer: boolean; text: boolean };
  /** Further facts the client states about itself; with several clients attached, the claims all of them share. */
  claims?: PiGViewClaim[];
  /** Claims that replace the same-named ones for mounts of one slot. */
  slotClaims?: { slot: PiGViewSlot; claims: PiGViewClaim[] }[];
  /** The drawing profiles the client draws; absent when it draws none. */
  drawing?: { profiles: ("scene" | "pixels")[]; shapes: number; pixels: number };
}
export interface PiGViewIntentInvocation {
  mount: PiGViewRef;
  rev: number;
  nodeKey: string;
  intent: string;
  arg: unknown;
  ticket: string;
  /** Aborts when the host cancels a cancellable intent; an answer after that is dropped. */
  signal: AbortSignal;
}
export interface PiGViewIntentResult {
  outcome: "done" | "refused" | "failed";
  answer?: unknown;
  error?: string;
}

declare module "@earendil-works/pi-coding-agent" {
  interface ExtensionAPI {
    /**
     * Run `handler(ctx)` once the runtime registered and has its state, before
     * it serves a request (`session_start` included) and after the emits its
     * factory made were delivered. Pi has no such hook.
     */
    onLoaded(handler: (ctx: ExtensionContext) => void | Promise<void>): void;
    /**
     * Answer the intents of this extension's mounts (D614). A bare return value is the answer of a `done` outcome. A
     * later call replaces the handler.
     */
    onViewIntent(handler: (invocation: PiGViewIntentInvocation, ctx: ExtensionContext) => PiGViewIntentResult | unknown | Promise<PiGViewIntentResult | unknown>): void;
    /**
     * Run `handler` when what the attached client draws changed (D614): the first client attached, the abilities
     * changed, or the last client left (`null`; a client that draws no slot is a record with no slots).
     * `ctx.ui.viewAbilities()` already reports the new value, so a mount made in the handler is checked against that
     * client. The state an extension starts in is not a change, and a session no client attaches to never runs the
     * handler. Handlers run in registration order, one change at a time; the host does not wait for them.
     */
    onViewAbilities(handler: (abilities: PiGViewAbilities | null, ctx: ExtensionContext) => void | Promise<void>): void;
  }
  interface ExtensionContext {
    /** The provenance the loader stamped on this extension; undefined when the host holds none. */
    extensionSourceInfo(): PiGExtensionSourceInfo | undefined;
    /**
     * In a tool's `execute`: the call's position among the tool calls of the
     * assistant message that issued it; undefined for a nested `executeTool` call.
     */
    readonly toolCallIndex?: number;
    /** In a tool's `execute`: `JSON.stringify` of the call's arguments, members in the model's order. */
    readonly toolInput?: string;
    /**
     * Pi's `withFileMutationQueue` for a handler that has its context (D187): runs `fn` in the host's file mutation queue for `filePath`. Node runs Pi's one
     * thread, so this registers in the order Pi gives the tools of a parallel batch, exactly as Pi's own export does; the Go, Rust, Python and Lisp SDKs have the same
     * call on their contexts, where it orders a batch's tools that a bare free function leaves to thread scheduling.
     */
    withFileMutationQueue<T>(filePath: string, fn: () => Promise<T>): Promise<T>;
  }
  // Pi c466a359 (#11360) adds ExecOptions.maxBuffer and ExecResult.truncated. PiG's host implements them and this augmentation declares them.
  interface ExecOptions {
    /**
     * Combined stdout/stderr byte limit; exceeding it terminates the command with `truncated: true`.
     * Nonnegative integer up to the runtime string limit. Default: 256 MiB, capped by that limit.
     */
    maxBuffer?: number;
  }
  interface ExecResult {
    /** Output exceeded `maxBuffer`; stdout and stderr are incomplete and `code` is nonzero. */
    truncated: boolean;
  }
  // Pi eebd5d90 adds registerMarkdownTransformer's options and the "custom", "summary" and "changelog" surfaces (types.ts MarkdownMessageType). PiG's host implements them and this augmentation declares them.
  interface ExtensionAPI {
    /**
     * Register a display-only Markdown transformer. `options.messageTypes` lists the surfaces it runs on: "user", "assistant" and "assistant-thinking"
     * (the transcript messages, the default), "custom" (custom messages without a renderer and expanded skill blocks), "summary" (expanded branch and
     * compaction summaries) and "changelog" (What's New, /changelog, Extension Updates). An empty list runs it nowhere.
     */
    registerMarkdownTransformer(
      transformer: (
        markdown: string,
        context: Omit<MarkdownTransformContext, "messageType"> & {
          messageType: "user" | "assistant" | "assistant-thinking" | "custom" | "summary" | "changelog";
        },
      ) => string,
      options?: { messageTypes?: readonly ("user" | "assistant" | "assistant-thinking" | "custom" | "summary" | "changelog")[] },
    ): void;
  }
  interface ExtensionUIContext {
    /** Subscribe to changes of the host's theme; the handler receives `ui.theme` after it took the new palette. Returns the unsubscribe function. */
    onThemeChange(handler: (theme: Theme) => void): () => void;
    /** Subscribe to the host toggling tool-output expansion; the handler receives the new state. Returns the unsubscribe function. */
    onToolsExpandedChange(handler: (expanded: boolean) => void): () => void;
    /** Ring the terminal bell of the interactive terminal; nothing without a terminal UI. */
    bell(): void;
    /** Set PiG's native login template, or reject an invalid definition. */
    setLogin(definition: PiGLoginDefinition): Promise<void>;
    /**
     * Add a sprite to PiG's /sprite catalogue, or reject an invalid definition.
     * Registering the same ID again replaces this extension's sprite; the sprite
     * leaves /sprite when the extension unloads.
     */
    registerSprite(definition: PiGSpriteDefinition): Promise<void>;
    /** What the attached client draws (D614), or null while none is attached. */
    viewAbilities(): PiGViewAbilities | null;
    /** Mount a view in a slot the attached client draws. Rejects with an Error whose `code` is invalid, unsupported, limit, stale, terminal or not_found. */
    mountView(mount: PiGViewMount): Promise<PiGViewStatus>;
    /** Replace the content of a mount; stale unless `mount` carries the epoch and rev the last operation returned. */
    updateView(mount: PiGViewRef, content: PiGViewContent): Promise<PiGViewStatus>;
    hideView(mount: PiGViewRef): Promise<PiGViewStatus>;
    showView(mount: PiGViewRef): Promise<PiGViewStatus>;
    /** End a mount as completed. Terminal: a later operation fails with code `terminal`. */
    finishView(mount: PiGViewRef, result?: unknown): Promise<PiGViewStatus>;
    /** End a mount as withdrawn. Terminal. */
    removeView(mount: PiGViewRef): Promise<PiGViewStatus>;
    viewState(key: string): Promise<PiGViewStatus>;
    /** The notices of the mount under `key`, oldest first; the host keeps at most 32 and counts repeats. */
    viewDiagnostics(key: string): Promise<PiGViewNotice[]>;
  }
  interface ExtensionAPI {
    /**
     * Declare that this extension owns the thinking runs of assistant messages,
     * so a thinking run that is hidden or draws zero rows leaves no blank
     * spacer rows around it. Called while the extension loads, it sets the
     * initial value; called later, it changes it. The host ORs the flag across
     * extensions, and it drops when the extension unloads or reloads. The last
     * call wins. `enabled` defaults to true.
     */
    ownThinkingRuns(enabled?: boolean): void;
  }
}

declare module "@earendil-works/pi-tui" {
  interface Component {
    /** On a surface's root component: token overrides for that surface (PiG only). */
    viewTheme?: PiGViewTheme;
    /** Frontend-only annotations of this component's rows (PiG only). */
    viewLines?: PiGViewLines;
  }
}
