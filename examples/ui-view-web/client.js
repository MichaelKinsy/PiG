// A reference client of the plugin UI carrier (docs/ui-view-carrier.md). It attaches over the bridge, keeps the
// client's side of the carrier's rules (numbers without a hole, a resync after a gap, a redraw only when the digest
// changed), draws the snapshots it is sent as controls (a node is the control its kind and its a11y role make it) and
// their drawings as vector graphics, applies their a11y and focus records, and raises their intents.

const SLOTS = ["notify", "status", "widget-above", "widget-below", "header", "footer", "dialog", "overlay", "panel", "tool-card", "message", "entry"];
// The node kinds this page draws. A kind outside the list is refused by the host for this client; a node the page
// cannot draw after all is reported with ui_view_report.
const NODES = ["container", "box", "vstack", "hstack", "text", "truncated-text", "markdown", "lines", "spacer", "dynamic-border", "image",
  "loader", "grid", "select-list", "settings-list", "input", "editor", "mouse-region", "scroll-view"];
const ABILITIES = {
  slots: SLOTS, nodes: NODES, markdown: true, imageFormats: ["png", "jpeg", "gif", "webp"],
  // snapshotBytes is the carrier's bound of one line: a scene of a few thousand points is some hundred kilobytes.
  limits: { nodes: 2048, depth: 32, snapshotBytes: 1 << 22, textBytes: 1 << 16, mounts: 32, intentArgBytes: 1 << 16 },
  input: { keyboard: true, pointer: true, text: true },
  // input.bindings: the page fires key bindings (docs/ui-view-carrier.md, Bindings); it reports no swipes.
  claims: [{ name: "client.kind", value: "web" }, { name: "input.bindings", value: ["key"] }],
  // Both drawing profiles, up to the host's own bounds (coding/uiview/drawing.go).
  drawing: { profiles: ["scene", "pixels"], shapes: 4096, pixels: 1 << 20 },
};

// The address carries a one-time code in its fragment (`/#<code>`), which a browser never sends to a server. The page takes
// it out of the address bar at once and posts it to /exchange (in a body, so in no request address). It keeps the secret
// it is answered with for this tab (sessionStorage: one tab, one origin, so one port; a reload keeps it) and sends it in an
// Authorization header with every request. The secret is in no address and in no cookie: a cookie of this host would go to
// every other port of it.
const SECRET_KEY = "ui-view-web-secret";
const USED = "This address was used or has expired. Open the file the bridge named again; if there is none, restart the bridge.";
const RESTARTED = "The bridge does not know this tab any more: it was restarted. Open the file the new bridge named.";
const SIGNED_OUT = "This tab is not signed in. Open the file the bridge named.";
const code = /^#([0-9a-f]{32})$/.exec(location.hash)?.[1];
if (code) history.replaceState(null, "", location.pathname + location.search);
let secret = sessionStorage.getItem(SECRET_KEY);
const state = document.getElementById("state");
const chat = document.getElementById("chat");
let session = "";
// Whether the page is a client of a running session's view endpoint (the bridge's --connect) and not of a pig of its own.
let attached = false;
let attachment = 0;
let lastSeq = 0;
let resyncing = false;
let nextId = 0;
let nextMount = 0;
const pending = new Map(); // command + " " + id -> resolve
const mounts = new Map(); // plugin + "\n" + key -> { snapshot, element, ids, prefix }

// What the page exposes to a test or a console: the carrier state it keeps.
window.uiView = { mounts, abilities: ABILITIES, get session() { return session; }, get attachment() { return attachment; }, get lastSeq() { return lastSeq; }, resyncs: 0, gaps: 0, reports: 0,
  // Milliseconds. timing.draw: what the last draw took. timing.intent: from the last intent's command to the end of the draw of the update its handler made.
  // timing.gesture: the same from the pointer or key event on a drawing's region, so with the wait for the drag's turn.
  timing: {} };

function say(text, kind = "note") {
  const line = document.createElement("div");
  line.className = `line ${kind}`;
  line.textContent = text;
  chat.append(line);
  line.scrollIntoView({ block: "nearest" });
  return line;
}

function post(command) {
  // A tab without a secret (the bridge refused it, or it never had one) asks the bridge for nothing.
  if (!secret) return Promise.resolve();
  // A refusal means the bridge forgot this tab's secret (it was restarted, or newer secrets pushed it out).
  return fetch(`/send?session=${session}`, { method: "POST", headers: authorization(), body: JSON.stringify(command) }).then((res) => {
    if (res.status === 403) signedOut(RESTARTED);
    return res;
  });
}

// request sends a command with a fresh id and resolves with its response.
function request(command, id = `w${++nextId}`) {
  return new Promise((resolve) => {
    pending.set(`${command.type} ${id}`, resolve);
    post({ id, ...command });
  });
}

function onLine(record) {
  if (record.type === "response") {
    const key = `${record.command} ${record.id}`;
    pending.get(key)?.(record);
    pending.delete(key);
  } else if (record.type === "ui_view") {
    onEvent(record);
  } else if (record.type === "ui_view_gap") {
    window.uiView.gaps++;
    resync();
  } else if (record.type === "extension_ui_request" && record.method === "notify") {
    say(record.message);
  } else if (record.type === "message_end" && record.message?.role === "assistant") {
    const text = (record.message.content ?? []).filter((part) => part.type === "text").map((part) => part.text).join("");
    if (text) say(text, "assistant");
  }
}

// The client's rule: an event at or below the last number is dropped, the next number is applied, and a hole means resync.
function onEvent(event) {
  if (event.attachment !== attachment || event.seq <= lastSeq) return;
  if (resyncing) return; // The host writes the resync reply before any event numbered after it.
  if (event.seq !== lastSeq + 1) return resync();
  lastSeq = event.seq;
  apply(event.snapshot);
}

async function resync() {
  if (resyncing) return;
  resyncing = true;
  window.uiView.resyncs++;
  const reply = await request({ type: "ui_view_resync", attachment });
  resyncing = false;
  if (!reply.success) return attach();
  adopt(reply.data);
}

// adopt takes the state of an attach or resync reply: mounts the reply lacks are gone, and a mount whose digest the
// page already holds is not drawn again.
function adopt(data) {
  attachment = data.attachment;
  lastSeq = data.seq;
  const kept = new Set(data.snapshots.map((s) => `${s.plugin}\n${s.mount.key}`));
  for (const [key, mount] of mounts) if (!kept.has(key)) drop(key, mount);
  for (const snapshot of data.snapshots) apply(snapshot);
  counters();
}

// counters writes the carrier's numbers in the page's status line when the address asks for them (`?debug`). Without
// it the line is empty once the page is attached, and says only what the user must know: that it is connecting, that
// no session runs, that the stream broke.
const debug = new URLSearchParams(location.search).has("debug");
function counters() {
  state.textContent = debug ? `attachment ${attachment}, event ${lastSeq}, ${mounts.size} mounts` : "";
}

async function attach() {
  for (const [key, mount] of mounts) drop(key, mount);
  const reply = await request({ type: "ui_view_attach", abilities: ABILITIES });
  if (!reply.success) return void (state.textContent = `attach failed: ${reply.error}`);
  adopt(reply.data);
}

// The panel mounts stand beside the chat, each with a tab. A tab is labelled with the mount's title, or with its key
// when it has none (and with its plugin too when two panels share a label). `chosen` is the panel the user chose; a panel
// newly mounted or shown again by its extension is chosen. Side by side, every panel is drawn; as tabs, the chosen
// one. A panel behind another is still drawn into, so it is current when the user chooses it.
const panels = { root: document.getElementById("panels"), tabs: document.querySelector("#panels [role=tablist]"), side: document.getElementById("side"), chosen: "" };

function showPanels() {
  const open = [...mounts].filter(([, mount]) => mount.tab && mount.snapshot.state !== "hidden");
  if (!open.some(([key]) => key === panels.chosen)) panels.chosen = open.at(-1)?.[0] ?? "";
  const side = panels.side.getAttribute("aria-pressed") === "true";
  panels.root.hidden = open.length === 0;
  panels.tabs.parentElement.hidden = open.length < 2;
  for (const [key, mount] of mounts) {
    if (!mount.tab) continue;
    const label = labelOf(mount.snapshot);
    mount.tab.textContent = open.some(([other, m]) => other !== key && labelOf(m.snapshot) === label) ? `${mount.snapshot.plugin} / ${label}` : label;
    mount.tab.hidden = mount.snapshot.state === "hidden";
    mount.tab.setAttribute("aria-selected", String(key === panels.chosen));
    mount.tab.tabIndex = key === panels.chosen ? 0 : -1;
    mount.element.classList.toggle("behind", !side && key !== panels.chosen);
  }
  offer();
}

// The prompt stands at the window's lower edge while the page is one column (index.html: sticky), over what scrolls
// under it. What the browser scrolls into view, a focused row for one, stops above it: the page's scroll padding is
// the height of that block.
const root = document.documentElement;
new ResizeObserver(([entry]) => {
  root.style.scrollPaddingBottom = getComputedStyle(entry.target).position === "sticky" ? `${entry.target.offsetHeight}px` : "";
}).observe(document.querySelector("main"));

// offer lists the hidden mounts the user can open: the ones that declare a `whenHidden` intent, each as a button that
// reads the mount's title. Choosing one raises that intent with {}, and the extension's handler shows the view; the
// page shows nothing by itself. A hidden mount without the intent is not offered.
const closed = document.getElementById("closed");
const opener = (snapshot) => (snapshot.state === "hidden" ? (snapshot.intents ?? []).find((decl) => decl.whenHidden) : undefined);

function offer() {
  const kept = new Map([...closed.querySelectorAll("button")].map((button) => [button.dataset.mount, button]));
  for (const [key, mount] of mounts) {
    if (!opener(mount.snapshot)) continue;
    let button = kept.get(key);
    if (button) kept.delete(key);
    else {
      button = document.createElement("button");
      button.type = "button";
      button.dataset.mount = key;
      button.addEventListener("click", () => open(key));
      closed.append(button);
    }
    button.textContent = labelOf(mount.snapshot);
    button.title = `${mount.snapshot.plugin} / ${mount.snapshot.mount.key}`;
  }
  for (const button of kept.values()) button.remove();
  closed.hidden = closed.querySelector("button") === null;
}

async function open(key) {
  const mount = mounts.get(key);
  const decl = mount && opener(mount.snapshot);
  if (!decl) return;
  const label = labelOf(mount.snapshot);
  const result = await raise(mount, decl, "", undefined, undefined, { arg: {}, quiet: true });
  if (result.outcome !== "done") return void say(`${label} did not open (${result.outcome})`);
  // The button that held the focus is gone: the focus goes to the view it opened.
  const shown = mounts.get(key);
  if (shown && shown.snapshot.state !== "hidden" && (document.activeElement ?? document.body) === document.body) {
    (shown.element.querySelector("input, textarea, button, [tabindex]") ?? shown.tab)?.focus();
  }
}

// labelOf is what a mount is called in a tab: its title, or its key.
const labelOf = (snapshot) => snapshot.title || snapshot.mount.key;

// tab makes the tab of a panel mount: a click chooses the panel, the arrow keys move along the tabs.
function tab(key, mount) {
  const el = document.createElement("button");
  el.setAttribute("role", "tab");
  el.id = `${mount.prefix}tab`;
  el.title = `${mount.snapshot.plugin} / ${mount.snapshot.mount.key}`;
  mount.element.id = `${mount.prefix}panel`;
  el.setAttribute("aria-controls", mount.element.id);
  el.addEventListener("click", () => {
    panels.chosen = key;
    showPanels();
    mount.element.scrollIntoView({ block: "nearest" });
  });
  el.addEventListener("keydown", (event) => {
    const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
    if (!step) return;
    event.preventDefault();
    const tabs = [...panels.tabs.children].filter((other) => !other.hidden);
    const next = tabs[(tabs.indexOf(el) + step + tabs.length) % tabs.length];
    next.click();
    next.focus();
  });
  panels.tabs.append(el);
  return el;
}

panels.side.addEventListener("click", () => {
  panels.side.setAttribute("aria-pressed", String(panels.side.getAttribute("aria-pressed") !== "true"));
  showPanels();
});

function drop(key, mount, restore = true) {
  const focused = mount.element.contains(document.activeElement);
  mount.element.remove();
  mount.tab?.remove();
  mounts.delete(key);
  showPanels();
  if (focused && restore) mount.restore?.focus();
}

// ended is what the page says when a mount ends. A mount its extension ended itself (finished, removed) goes from the
// page without a word: the extension closed its own view. A mount that broke while it was on the page (its extension
// stopped, or is being loaded again by a reload) leaves one line in the user's words, by the mount's title, and the
// line goes when the same key is mounted again, as it is after a reload. With `?debug` the end is also written as the
// carrier states it.
const notices = new Map(); // plugin + "\n" + key -> the line of the mount that broke
function ended(key, snapshot, held) {
  const why = snapshot.reason ? ` (${snapshot.reason})` : "";
  if (debug) say(`${snapshot.plugin}/${snapshot.mount.key} ${snapshot.state}${why}${snapshot.result === undefined ? "" : `: ${JSON.stringify(snapshot.result)}`}`);
  if (snapshot.state !== "broken" || !held) return;
  notices.get(key)?.remove();
  notices.set(key, say(`${labelOf(held.snapshot)} closed${snapshot.reason === "owner_gone" ? ": its extension stopped" : why}.`));
}

function apply(snapshot) {
  const key = `${snapshot.plugin}\n${snapshot.mount.key}`;
  const held = mounts.get(key);
  if (!["finished", "removed", "broken"].includes(snapshot.state) && notices.has(key)) {
    notices.get(key).remove();
    notices.delete(key);
  }
  if (held?.tab && held.snapshot.state === "hidden" && snapshot.state !== "hidden") panels.chosen = key;
  if (["finished", "removed", "broken"].includes(snapshot.state)) {
    if (held) drop(key, held);
    ended(key, snapshot, held);
  } else if (held && held.snapshot.mount.epoch === snapshot.mount.epoch && snapshot.digest && held.snapshot.digest === snapshot.digest) {
    // The same content: nothing is drawn.
    held.snapshot = snapshot;
    present(held);
    showPanels();
  } else {
    draw(key, snapshot, held);
  }
  counters();
}

// shell makes the element of a new mount: a card with a caption and the mount's own intents as buttons, the view, and
// a status line. A mount of the dialog or the overlay slot is a dialog element. The mount's controls have no listener
// of their own: the shell listens once and reads what was activated from the element (`data-act`) and its view node,
// so an update can keep every element (morph) and still act on the newest view.
function shell(snapshot) {
  const mount = { prefix: `m${++nextMount}-`, restore: document.activeElement, floating: snapshot.slot === "dialog" || snapshot.slot === "overlay" };
  const el = (mount.element = document.createElement(mount.floating ? "dialog" : "article"));
  el.className = "mount";
  el.dataset.plugin = snapshot.plugin;
  el.dataset.key = snapshot.mount.key;
  mount.head = document.createElement("header");
  mount.head.append(document.createElement("span"));
  mount.body = text("view", "");
  mount.hints = text("hints", "");
  mount.hints.setAttribute("role", "toolbar");
  mount.hints.setAttribute("aria-label", "Shortcuts");
  mount.hints.hidden = true;
  mount.footer = document.createElement("footer");
  mount.footer.setAttribute("role", "status");
  el.append(mount.head, mount.body, mount.hints, mount.footer);
  el.addEventListener("click", (event) => onClick(mount, event));
  el.addEventListener("keydown", (event) => onKey(mount, event));
  el.addEventListener("pointerdown", (event) => onPointerDown(mount, event));
  el.addEventListener("pointermove", (event) => onPointerMove(mount, event));
  for (const end of ["pointerup", "pointercancel"]) el.addEventListener(end, (event) => onPointerUp(mount, event));
  if (mount.floating) {
    // The mount is the extension's: the page does not close it. A request to close it (Escape, the platform's own
    // gesture) is passed on (leave); a dialog the browser closed all the same is opened again.
    el.addEventListener("cancel", (event) => {
      event.preventDefault();
      leave(mount);
    });
    el.addEventListener("close", () => present(mount));
    // A dialog that opened without the focus (present) becomes modal when the user enters it.
    el.addEventListener("focusin", () => {
      // After the focus has moved, not while it moves.
      queueMicrotask(() => {
        const entered = document.activeElement;
        if (mount.snapshot.slot !== "dialog" || !el.open || el.matches(":modal") || !el.contains(entered)) return;
        el.close();
        el.showModal();
        entered.focus();
      });
    });
  }
  return mount;
}

// busy: the user is typing somewhere else: the focus is in a text field outside `within` that holds text. A mount
// takes no focus from it (the rule of the terminal client: a view never takes the keyboard from an editor that holds
// text); the page reports `unfocused` and the user moves into the mount.
function busy(within) {
  const at = document.activeElement;
  return Boolean(at) && !within.contains(at) && typed(at) && at.value !== "";
}

// present shows or hides a mount as its state says. A dialog mount is opened as a modal dialog and an overlay as a
// dialog beside the page, and either takes the focus as it opens, unless the user is typing elsewhere: then it opens
// without the focus. Hidden, it is closed, so the page under it takes input again.
function present(mount) {
  const hidden = mount.snapshot.state === "hidden";
  const el = mount.element;
  el.hidden = hidden;
  if (!mount.floating || !el.isConnected) return;
  if (hidden) {
    if (el.open) el.close();
  } else if (!el.open) {
    if (busy(el)) {
      el.open = true;
      report(mount, "focus", "a text field holds text", "unfocused", "info");
    } else if (mount.snapshot.slot === "dialog") el.showModal();
    else el.show();
  }
}

// draw shows a snapshot. A mount already on the page keeps its element and morph keeps every element whose node is
// still in the view, so an update leaves what the user is typing, the selection and the focus where they are, and a
// style that changed moves by its transition.
function draw(key, snapshot, held) {
  const started = performance.now();
  const mount = held ?? shell(snapshot);
  const was = mount.ids ?? new Map();
  const before = mount.snapshot;
  mount.snapshot = snapshot;
  if (!held && snapshot.slot === "panel") {
    mount.tab = tab(key, mount);
    panels.chosen = key;
  }
  mount.element.dataset.digest = snapshot.digest ?? "";
  const title = `${snapshot.title || `${snapshot.plugin} / ${snapshot.mount.key}`}${snapshot.degraded ? " (degraded)" : ""}`;
  mount.head.firstChild.textContent = title;
  mount.head.firstChild.classList.toggle("title", Boolean(snapshot.title));
  if (mount.floating) {
    mount.element.setAttribute("aria-label", title);
    // The browser asks before it closes the dialog (its `cancel` event) only for a mount that can be dismissed.
    mount.element.setAttribute("closedby", dismissal(snapshot) ? "closerequest" : "none");
  }
  mount.body.removeAttribute("style");
  theme(mount.body, snapshot.view);

  const ctx = { mount, view: snapshot.view, a11y: new Map((snapshot.a11y ?? []).map((entry) => [entry.node, entry])), ids: new Map(), owned: new Set(), undrawn: [], boxes: 0, inline: false, was };
  let fresh = snapshot.view?.root ? node(snapshot.view.root, ctx, "root") : text("undrawn", snapshot.degraded ? "This view does not fit this client." : "");
  if (snapshot.drawing) {
    // The drawing fills the node it names; without one, or when that node is a control, it follows the view.
    const graphic = scene(snapshot);
    const filled = ctx.ids.get(snapshot.drawing.node);
    if (filled && !/^(?:INPUT|TEXTAREA|PROGRESS|IMG|HR)$/.test(filled.tagName)) {
      filled.replaceChildren(graphic);
      filled.classList.add("drawn");
    } else {
      const both = text("canvas", "");
      both.append(fresh, graphic);
      fresh = both;
    }
  }
  applyA11y(snapshot, ctx, mount.prefix);
  (snapshot.focus ?? []).forEach((entry, i) => ctx.ids.get(entry.node)?.setAttribute("tabindex", String(i + 1)));

  const focused = mount.element.contains(document.activeElement) ? document.activeElement : null;
  const selection = focused && typed(focused) ? [focused.selectionStart, focused.selectionEnd, focused.selectionDirection] : null;
  const root = mount.body.firstChild;
  if (!root) mount.body.append(fresh);
  else if (morph(root, fresh) !== root) root.replaceWith(fresh);
  mount.ids = new Map([...mount.body.querySelectorAll("[data-node]")].map((el) => [el.dataset.node, el]));
  // A title that the view's own first heading repeats is not drawn twice.
  mount.head.firstChild.classList.toggle("repeated", Boolean(snapshot.title) && mount.body.querySelector("h2, h3, h4, h5, h6")?.textContent.trim() === snapshot.title.trim());
  // A text node that morph could not keep in place (it moved to another parent) hands its text to the element drawn for it.
  for (const [id, el] of mount.ids) {
    const old = was.get(id);
    if (old && old !== el && typed(el) && old.tagName === el.tagName && (el.view.value ?? "") === (old.view.value ?? "")) el.value = old.value;
  }
  // The mount's own intents are buttons in its header, but for the ones a binding or a region of the drawing raises:
  // those have their own trigger, which gives them their argument. The intent that opens the mount while it is hidden
  // (`whenHidden`) has its trigger in the list of hidden mounts (offer).
  const triggered = new Set([...(snapshot.bindings ?? []), ...(snapshot.drawing?.regions ?? [])].map((trigger) => trigger.intent));
  const actions = JSON.stringify((snapshot.intents ?? []).filter((decl) => !decl.nodeKey && !decl.whenHidden && !triggered.has(decl.name)));
  if (actions !== mount.actions) {
    mount.actions = actions;
    mount.head.replaceChildren(mount.head.firstChild);
    for (const decl of JSON.parse(actions)) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = words(decl.name);
      button.title = decl.name;
      button.dataset.act = "intent";
      button.dataset.intent = decl.name;
      mount.head.append(button);
    }
  }
  hints(mount, snapshot.bindings ?? []);
  if (!held) document.querySelector(`section[data-slot="${snapshot.slot}"]`).append(mount.element);
  mounts.set(key, mount);
  present(mount);
  showPanels(); // before the focus moves: an element that is not shown takes none
  // The focus stays on the element that had it; when that element was drawn again, on the one with its id.
  const target = focused?.isConnected ? focused : focused?.id ? document.getElementById(focused.id) : null;
  if (target && document.activeElement !== target) {
    target.focus({ preventScroll: true });
    if (selection && typed(target)) target.setSelectionRange(...selection);
  }
  applyFocus(mount, !held, before);
  for (const [path, kind] of ctx.undrawn) report(mount, path, `this client does not draw ${kind}`);
  mount.drawnAt = performance.now();
  window.uiView.timing.draw = mount.drawnAt - started;
}

// words names an intent for its button. An intent has a name, `<extension>.<verb>`, and no label, so the button reads
// the verb in words ("map.zoomIn": "Zoom in") and carries the name as its title.
function words(name) {
  const verb = name.slice(name.lastIndexOf(".") + 1).replace(/([a-z0-9])([A-Z])/g, "$1 $2").replace(/[-_]+/g, " ").trim().toLowerCase();
  return verb ? verb[0].toUpperCase() + verb.slice(1) : name;
}

// A key id of a binding (coding/uiview/bindings.go): `shift`, `ctrl` and `alt` in any order, each followed by `+`,
// then the base key: a letter, a digit, a named key or a symbol named by the character it produces.
function parseKey(id) {
  const key = { shift: false, ctrl: false, alt: false };
  let base = id;
  for (let m; (m = /^(shift|ctrl|alt)\+(?=.)/.exec(base)); base = base.slice(m[0].length)) key[m[1]] = true;
  return { ...key, base };
}

const KEY_NAMES = { Enter: "enter", " ": "space", Backspace: "backspace", Delete: "delete", Insert: "insert", Home: "home", End: "end", PageUp: "pageUp", PageDown: "pageDown", ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right" };

// bound finds the mount's key binding for a key event, comparing keys parsed. Shift is part of the id of a letter, a
// digit and a named key; a symbol is named by the character, whatever keys produced it.
function bound(mount, event) {
  const base = KEY_NAMES[event.key] ?? (/^F\d{1,2}$/.test(event.key) ? event.key.toLowerCase() : event.key.length === 1 ? event.key.toLowerCase() : "");
  if (!base || event.metaKey) return undefined;
  const shifted = base.length > 1 || /^[a-z0-9]$/.test(base);
  return (mount.keys ?? []).find((key) => key.base === base && key.ctrl === event.ctrlKey && key.alt === event.altKey && (!shifted || key.shift === event.shiftKey));
}

// hints draws the mount's bindings: a key binding with a name is a button that shows its key and fires it, in a row
// under the view; one without a name only has its key. A mount with a key binding can hold the focus as a whole. The
// page reports no swipes, so it tells the extension of each gesture binding that it does not honour it.
function hints(mount, bindings) {
  const shown = JSON.stringify(bindings);
  if (shown === mount.bound) return;
  mount.bound = shown;
  mount.keys = bindings.flatMap((binding, i) => (binding.key ? [{ ...parseKey(binding.key), i }] : []));
  mount.hints.replaceChildren();
  bindings.forEach((binding, i) => {
    if (!binding.key) return report(mount, `bindings[${i}]`, "this client reports no swipes", "unsupported", "info");
    if (!binding.name) return;
    const key = parseKey(binding.key);
    const parts = [key.ctrl && "Ctrl", key.alt && "Alt", key.shift && "Shift", key.base.length === 1 ? key.base.toUpperCase() : key.base[0].toUpperCase() + key.base.slice(1)].filter(Boolean);
    const button = document.createElement("button");
    button.type = "button";
    button.dataset.act = "binding";
    button.dataset.binding = String(i);
    button.setAttribute("aria-keyshortcuts", parts.map((part) => (part === "Ctrl" ? "Control" : part)).join("+"));
    button.append(text("", binding.name, "span"), text("", parts.join("+"), "kbd"));
    mount.hints.append(button);
  });
  mount.hints.hidden = mount.hints.children.length === 0;
  if (mount.keys.length) mount.body.tabIndex = 0;
  else mount.body.removeAttribute("tabindex");
}

// fireBinding raises a binding's intent with its constant argument, or {} when it has none.
function fireBinding(mount, i) {
  const binding = (mount.snapshot.bindings ?? [])[i];
  const decl = binding && (mount.snapshot.intents ?? []).find((d) => !d.nodeKey && d.name === binding.intent);
  if (decl) raise(mount, decl, "", undefined, undefined, { arg: binding.arg ?? {} });
}

// typed: the element takes text the user types (an input or an editor node).
const typed = (el) => el.tagName === "TEXTAREA" || (el.tagName === "INPUT" && el.type === "text");
// keyOf names an element among its siblings: the id of its view node, or the key of its shape in a drawing.
const keyOf = (el) => (el.nodeType !== 1 ? "" : el.view?.id ? `#${el.view.id}` : (el.getAttribute("data-k") ?? ""));

// morph makes what is drawn (old) show the freshly drawn tree and returns the node that stands for it: old when both
// are the same element for the same view node or shape, else fresh. Attributes are set to the fresh ones, an input or
// an editor keeps the user's text unless the view's value changed, and a checkbox takes the state the view gives.
// Children with a key are matched by key and the others by position, so a node that is gone takes its element with it
// and never lends it to another id.
function morph(old, fresh) {
  if (old.nodeType !== fresh.nodeType || old.nodeName !== fresh.nodeName || keyOf(old) !== keyOf(fresh) || old.view?.kind !== fresh.view?.kind) return fresh;
  if (old.nodeType !== 1) {
    if (old.data !== fresh.data) old.data = fresh.data;
    return old;
  }
  for (const { name } of [...old.attributes]) if (!fresh.hasAttribute(name)) old.removeAttribute(name);
  for (const { name, value } of fresh.attributes) if (old.getAttribute(name) !== value) old.setAttribute(name, value);
  if (typed(old) && (fresh.view?.value ?? "") !== (old.view?.value ?? "")) {
    // The extension changed the value under the user: the selection stays where it was, as far as the new text goes.
    const selection = [old.selectionStart, old.selectionEnd, old.selectionDirection];
    old.value = fresh.view?.value ?? "";
    if (document.activeElement === old) old.setSelectionRange(...selection);
  }
  if (old.type === "checkbox" || old.type === "radio") old.checked = fresh.checked;
  old.view = fresh.view;
  if (typed(old)) return old;
  const olds = [...old.childNodes];
  const byKey = new Map(olds.filter(keyOf).map((el) => [keyOf(el), el]));
  const kept = new Set();
  [...fresh.childNodes].forEach((child, i) => {
    const key = keyOf(child);
    const match = key ? byKey.get(key) : olds[i] && !keyOf(olds[i]) ? olds[i] : undefined;
    const now = match && !kept.has(match) ? morph(match, child) : child;
    kept.add(now);
    if (old.childNodes[i] !== now) old.insertBefore(now, old.childNodes[i] ?? null);
  });
  for (const el of olds) if (!kept.has(el)) el.remove();
  if (old.view?.kind === "scroll-view" && old.view.follow === "end") old.scrollTop = old.scrollHeight;
  return old;
}

function text(className, value, tag = "div") {
  const el = document.createElement(tag);
  if (className) el.className = className;
  el.textContent = value;
  return el;
}

// The sizes of a view (gap, padding, basis, minSize, maxSize, a spacer's lines) are terminal cells (D107). Here a cell
// is the page's spacing tokens: --view-col wide and --view-row high (index.html).
const cols = (n) => `calc(${n} * var(--col))`;
const rows = (n) => `calc(${n} * var(--row))`;
const ALIGN = { stretch: "stretch", start: "flex-start", center: "center", end: "flex-end" };

// colour follows the rule drawings use (coding/uiview/drawing.go, validColor): "#rrggbb" or "#rrggbbaa" is the colour
// itself and a name is a theme token, drawn from the custom property --pig-<token> that the page (index.html), a theme
// directory or the view's theme sets. A token the page has no colour for draws nothing.
const HEX = /^#(?:[0-9a-f]{6}|[0-9a-f]{8})$/;
const TOKEN = /^[a-z][A-Za-z0-9]*$/;
function colour(value) {
  return HEX.test(value) ? value : TOKEN.test(value ?? "") ? `var(--pig-${value})` : "";
}

// theme sets the view's token overrides on its element, so they reach this view's nodes and no other mount.
function theme(el, view) {
  for (const [token, value] of Object.entries(view?.theme ?? {})) if (TOKEN.test(token) && HEX.test(value)) el.style.setProperty(`--pig-${token}`, value);
}

// markdown draws the everyday subset of CommonMark as elements built one by one: headings, paragraphs, lists, quotes,
// fenced code and rules, with code spans, emphasis, strikethrough and links. The text is never parsed as HTML: a tag,
// a script or an on* attribute in it is drawn as the characters it is, and a link is kept only when it is http, https
// or mailto.
function markdown(source) {
  const root = text("markdown", "");
  const make = (tag, parent = root) => parent.appendChild(document.createElement(tag));
  const lines = source.split("\n");
  let block = null; // the open paragraph, list or quote
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    let m;
    if (/^\s*```/.test(line)) {
      const body = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) body.push(lines[i]);
      make("code", make("pre")).textContent = body.join("\n");
      block = null;
    } else if (!line.trim()) block = null;
    else if ((m = /^(#{1,6})\s+(.*)$/.exec(line))) {
      inline(make(`h${m[1].length}`), m[2]);
      block = null;
    } else if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      make("hr");
      block = null;
    } else if ((m = /^\s*(?:[-*+]|(\d+)[.)])\s+(.*)$/.exec(line))) {
      const tag = m[1] ? "OL" : "UL";
      if (block?.tagName !== tag) block = make(tag);
      inline(make("li", block), m[2]);
    } else {
      m = /^\s*>\s?(.*)$/.exec(line);
      const tag = m ? "BLOCKQUOTE" : "P";
      if (block?.tagName !== tag) block = make(tag);
      else block.append("\n");
      inline(block, m ? m[1] : line);
    }
  }
  return root;
}

const INLINE = /`([^`]+)`|\*\*([^*]+)\*\*|~~([^~]+)~~|\*([^*\s][^*]*)\*|\b_([^_]+)_\b|\[([^\]]+)\]\(([^)\s]+)\)/g;
const INLINE_TAGS = ["code", "strong", "del", "em", "em", "a"];

function inline(parent, source) {
  let at = 0;
  for (const m of source.matchAll(INLINE)) {
    const which = m.findIndex((group, i) => i > 0 && group !== undefined) - 1;
    if (which === 5 && !/^(?:https?:|mailto:)/i.test(m[7])) continue; // not a link this page follows: left as text
    parent.append(source.slice(at, m.index));
    at = m.index + m[0].length;
    const el = parent.appendChild(document.createElement(INLINE_TAGS[which]));
    if (which === 0) el.textContent = m[1];
    else inline(el, m[which + 1]);
    if (which === 5) Object.assign(el, { href: m[7], target: "_blank", rel: "noopener noreferrer" });
  }
  parent.append(source.slice(at));
}

// pad sets a node's padding; `fallback` is what the kind has when the view names none.
function pad(el, n, fallback) {
  el.style.padding = `${rows(n.paddingY ?? fallback)} ${cols(n.paddingX ?? fallback)}`;
}

// stack draws an hstack or a vstack as Pi's stacks lay them out (tui/stack.go): `gap` cells between children, and each
// child's `stack` entry as a flex item: basis (none: its own size), grow 0, shrink 1, minSize 0, maxSize none. Only
// an hstack has a size to share out and an `align` (where a shorter child sits); a vstack's children take its width
// and their own or their basis height.
function stack(n, el, child) {
  const row = n.kind === "hstack";
  const size = row ? cols : rows;
  el.style.gap = size(n.gap ?? 0);
  if (row) el.style.alignItems = ALIGN[n.align ?? "stretch"];
  (n.children ?? []).forEach((c, i) => {
    const item = child(c, i);
    const entry = c.stack ?? {};
    const basis = entry.basis === undefined ? "auto" : size(entry.basis);
    item.style.flex = row ? `${entry.grow ?? 0} ${entry.shrink ?? 1} ${basis}` : `0 0 ${basis}`;
    if (row || entry.minSize !== undefined) item.style[row ? "minWidth" : "minHeight"] = size(entry.minSize ?? 0);
    if (entry.maxSize !== undefined) item.style[row ? "maxWidth" : "maxHeight"] = size(entry.maxSize);
    el.append(item);
  });
  return el;
}

// node draws one D107 view node as the control it is. What a node is comes from its kind and, for a node with an id,
// from the role its a11y record gives it: a mouse-region is a button, or a checkbox, a switch or a radio with its
// checked state; a text with the role heading is a heading; a select-list is a listbox, or a row of tabs. The kinds
// that carry an id become elements under that id, so a11y and focus records can name them. Inside a button or a label
// (ctx.inline) a node is drawn as phrasing content without its padding: the control has its own.
function node(n, ctx, path) {
  const role = n.id ? ctx.a11y.get(n.id)?.role : undefined;
  const tag = ctx.inline ? "span" : "div";
  const child = (c, i) => node(c, ctx, `${path}/${i}`);
  const children = (parent, change = {}) => {
    const saved = { boxes: ctx.boxes, inline: ctx.inline };
    Object.assign(ctx, change);
    (n.children ?? []).forEach((c, i) => parent.append(child(c, i)));
    Object.assign(ctx, saved);
    return parent;
  };
  let el; // the element that stands for the node among its siblings
  let named; // the element that carries the node's id, when it is not el
  switch (n.kind) {
    case "container":
      el = children(text("container", "", tag));
      break;
    case "box":
      el = children(text(n.bg ? "box card" : "box", "", tag), { boxes: ctx.boxes + 1 });
      if (!ctx.inline) pad(el, n, 1);
      el.style.background = colour(n.bg);
      break;
    case "vstack": case "hstack":
      el = stack(n, text(n.kind, "", tag), child);
      break;
    case "text": case "truncated-text": {
      const heading = role === "heading" && !ctx.inline;
      if (heading) ctx.owned.add(n.id);
      el = text(n.kind === "text" ? "text" : "text truncated", n.text ?? "", heading ? `h${Math.min(2 + ctx.boxes, 6)}` : tag);
      if (!ctx.inline) pad(el, n, n.kind === "text" ? 1 : 0);
      if (n.kind === "text") el.style.background = colour(n.bg);
      break;
    }
    case "markdown": {
      el = markdown(n.text ?? "");
      const style = n.defaultTextStyle ?? {};
      el.style.color = colour(style.color);
      el.style.background = colour(style.bgColor);
      if (style.bold) el.style.fontWeight = "bold";
      if (style.italic) el.style.fontStyle = "italic";
      el.style.textDecoration = [style.underline && "underline", style.strikethrough && "line-through"].filter(Boolean).join(" ");
      break;
    }
    case "lines":
      el = lines(n, ctx);
      break;
    case "spacer":
      el = text("spacer", "", tag);
      el.style.height = rows(n.lines ?? 1);
      el.setAttribute("aria-hidden", "true");
      break;
    case "dynamic-border":
      el = document.createElement("hr");
      el.className = "rule";
      el.style.borderColor = colour(n.color);
      break;
    case "image": {
      const bytes = (ctx.view.images ?? []).find((image) => image.ref === n.ref);
      if (!bytes) return undrawnNode(n, path, ctx.undrawn);
      el = picture(bytes, n.filename ?? "");
      if (n.maxWidthCells) el.style.maxWidth = cols(n.maxWidthCells);
      if (n.maxHeightCells) el.style.maxHeight = rows(n.maxHeightCells);
      break;
    }
    case "loader": {
      el = text("loader", "", tag);
      el.setAttribute("role", "status");
      const spinner = el.appendChild(text("spinner", "", "span"));
      spinner.setAttribute("aria-hidden", "true");
      spinner.style.color = colour(n.spinnerColor);
      el.appendChild(text("", n.message ?? "Loading...", "span")).style.color = colour(n.messageColor);
      break;
    }
    case "select-list": case "settings-list":
      ctx.owned.add(n.id);
      el = listbox(n, ctx.mount, role === "tablist");
      break;
    case "grid":
      ctx.owned.add(n.id);
      el = grid(n, ctx);
      break;
    case "input":
      ctx.owned.add(n.id);
      el = document.createElement("input");
      el.type = "text";
      // What is typed here is the extension's data (a name, a place), not prose: the browser neither corrects nor offers it.
      el.spellcheck = false;
      el.autocomplete = "off";
      el.value = n.value ?? "";
      el.placeholder = n.placeholder ?? "";
      el.dataset.act = "submit";
      break;
    case "editor":
      ctx.owned.add(n.id);
      el = document.createElement("textarea");
      el.value = n.value ?? "";
      el.dataset.act = "submit";
      break;
    case "mouse-region":
      ctx.owned.add(n.id);
      if (role === "checkbox" || role === "switch" || role === "radio") {
        // A native checkbox or radio; the region's child is its label.
        el = document.createElement("label");
        el.className = "mouse-region check";
        named = el.appendChild(document.createElement("input"));
        named.type = role === "radio" ? "radio" : "checkbox";
        if (role === "switch") named.setAttribute("role", "switch");
        children(el, { inline: true });
      } else if (!role || ["button", "link", "tab", "menuitem", "treeitem"].includes(role)) {
        el = children(text("mouse-region", "", "button"), { inline: true });
        el.type = "button";
        if (role && role !== "button") el.setAttribute("role", role);
      } else {
        el = children(text("mouse-region", "", tag));
        el.tabIndex = 0;
        el.setAttribute("role", role);
      }
      (named ?? el).dataset.act = "node";
      break;
    case "scroll-view":
      el = children(text("scroll-view", "", tag));
      el.tabIndex = 0;
      if (n.follow === "end") queueMicrotask(() => (el.scrollTop = el.scrollHeight));
      break;
    default:
      return undrawnNode(n, path, ctx.undrawn);
  }
  el.view = n;
  if (n.id) {
    const at = named ?? el;
    at.id = ctx.mount.prefix + n.id;
    at.dataset.node = n.id;
    ctx.ids.set(n.id, at);
  }
  return el;
}

function undrawnNode(n, path, undrawn) {
  undrawn.push([path, n.kind]);
  return text("undrawn", `This client does not draw ${n.kind}.`);
}

function picture(bytes, alt) {
  const el = document.createElement("img");
  el.className = "image";
  el.src = `data:${bytes.mimeType};base64,${bytes.data}`;
  el.alt = alt;
  return el;
}

// lines draws a lines node: rows a component of its own drew, kept as they are in a monospace face. Its annotations
// say what the rows show, and the page then draws that instead: `progress` a progress bar, `list` a list with one
// row per item (label, detail and the further columns as the columns of one grid, the selected row marked current),
// `image` the image the rows depict.
function lines(n, ctx) {
  const content = (n.content ?? []).join("\n");
  if (n.progress && n.progress.max > 0) {
    const el = text("lines", content, "progress");
    el.max = n.progress.max;
    el.value = Math.min(Math.max(n.progress.value, 0), n.progress.max);
    return el;
  }
  if (n.list) {
    const items = n.list.items ?? [];
    const detailed = items.some((item) => item.detail);
    const cells = (item) => [item.label ?? "", ...(detailed ? [item.detail ?? ""] : []), ...(item.columns ?? [])];
    const el = text("lines rows", "", ctx.inline ? "span" : "div");
    el.setAttribute("role", "list");
    el.style.gridTemplateColumns = `minmax(0, 1fr)${" auto".repeat(Math.max(1, ...items.map((item) => cells(item).length)) - 1)}`;
    items.forEach((item, i) => {
      const row = el.appendChild(text("", "", "div"));
      row.setAttribute("role", "listitem");
      if (i === n.list.selectedIndex) row.setAttribute("aria-current", "true");
      for (const cell of cells(item)) row.append(text("", cell, "span"));
    });
    return el;
  }
  const bytes = n.image && (ctx.view.images ?? []).find((image) => image.ref === n.image.ref);
  if (bytes) return picture(bytes, "");
  return text("lines", content, ctx.inline ? "span" : "pre");
}

// listbox draws a select-list or a settings-list as a listbox of options, or as a row of tabs when its a11y role is
// tablist: the arrow keys move the selection, Enter or a click activates it. A settings-list item steps through its
// values. What is selected, and a setting's current value, are kept on the options themselves.
function listbox(n, mount, tabs) {
  const el = text(tabs ? "list tabs" : n.kind === "settings-list" ? "list settings" : "list", "");
  el.setAttribute("role", tabs ? "tablist" : "listbox");
  el.tabIndex = 0;
  el.dataset.act = "list";
  const items = n.items ?? [];
  const selected = Math.min(Math.max(n.selectedIndex ?? 0, 0), Math.max(items.length - 1, 0));
  items.forEach((item, i) => {
    const option = el.appendChild(text("", "", "div"));
    option.setAttribute("role", tabs ? "tab" : "option");
    option.id = `${mount.prefix}${n.id}-${i}`;
    option.dataset.act = "option";
    option.setAttribute("aria-selected", String(i === selected));
    if (n.kind === "settings-list" && item.currentValue !== undefined) option.dataset.value = item.currentValue;
    option.append(text("", item.label ?? item.value ?? item.id ?? "", "span"), text("", (n.kind === "settings-list" ? item.currentValue : item.description) ?? "", "small"));
  });
  if (items.length) el.setAttribute("aria-activedescendant", `${mount.prefix}${n.id}-${selected}`);
  return el;
}

// grid draws a grid node: equal columns, an optional row of column heads, and cells of data. A cell shows its label,
// at most `chipRows` chips and a line that counts the rest; a cell with a value can be chosen, with the arrow keys and
// Enter or with a click, which raises the grid's intent with that value, as a select-list does for an item. The
// cursor is the user's while the view keeps naming the same cell: an update that does not move `selected` leaves it.
function grid(n, ctx) {
  const el = text("grid", "");
  el.setAttribute("role", "grid");
  el.tabIndex = 0;
  el.dataset.act = "grid";
  el.style.gridTemplateColumns = `repeat(${n.columns}, minmax(0, 1fr))`;
  const row = () => {
    const line = el.appendChild(text("", "", "div"));
    line.setAttribute("role", "row");
    return line;
  };
  if (n.head?.length) {
    const head = row();
    for (const label of n.head) head.appendChild(text("", label, "div")).setAttribute("role", "columnheader");
  }
  const cells = n.cells ?? [];
  const before = ctx.was.get(n.id);
  const kept = before?.view?.kind === "grid" && before.view.selected === n.selected ? before.querySelector('[aria-selected="true"]')?.dataset.value : undefined;
  const cursor = cells.some((cell) => cell.value && cell.value === kept) ? kept : (n.selected ?? cells.find((cell) => cell.value)?.value);
  for (let at = 0; at < cells.length; at += n.columns) {
    const line = row();
    cells.slice(at, at + n.columns).forEach((cell, i) => {
      const box = line.appendChild(text(cell.dim ? "dim" : "", "", "div"));
      box.setAttribute("role", "gridcell");
      box.id = `${ctx.mount.prefix}${n.id}-${at + i}`;
      if (cell.value) {
        box.dataset.act = "cell";
        box.dataset.value = cell.value;
        box.setAttribute("aria-selected", String(cell.value === cursor));
        if (cell.value === cursor) el.setAttribute("aria-activedescendant", box.id);
      }
      if (cell.mark === "current") box.setAttribute("aria-current", "date");
      box.appendChild(text("label", cell.label ?? "", "span")).style.color = colour(cell.color);
      const chips = cell.chips ?? [];
      for (const chip of chips.slice(0, n.chipRows ?? 3)) box.appendChild(text("chip", chip.text ?? "", "span")).style.borderColor = colour(chip.color);
      const left = Math.max(chips.length - (n.chipRows ?? 3), 0) + (cell.more ?? 0);
      if (left > 0) box.append(text("more", `+${left} more`, "span"));
    });
  }
  return el;
}

// point moves a grid's cursor to a cell.
function point(gridEl, cell) {
  for (const other of gridEl.querySelectorAll("[aria-selected]")) other.setAttribute("aria-selected", String(other === cell));
  gridEl.setAttribute("aria-activedescendant", cell.id);
}

const cursor = (list) => [...list.children].findIndex((option) => option.getAttribute("aria-selected") === "true");

function choose(list, i) {
  [...list.children].forEach((option, j) => option.setAttribute("aria-selected", String(j === i)));
  if (list.children[i]) list.setAttribute("aria-activedescendant", list.children[i].id);
}

// fire activates the selected item of a list: a select-list raises its value; a settings-list steps the setting to its
// next value and raises that with the setting's id.
function fire(mount, list) {
  const n = list.view;
  const i = cursor(list);
  const item = (n.items ?? [])[i];
  if (!item) return;
  if (n.kind === "select-list") return activate(mount, n.id, item.value ?? item.label);
  const option = list.children[i];
  const values = item.values ?? [];
  let current = option.dataset.value;
  if (values.length) {
    current = values[(values.indexOf(current) + 1) % values.length];
    option.dataset.value = current;
  }
  option.lastChild.textContent = current ?? "";
  activate(mount, n.id, current, item.id);
}

// onClick is the one click listener of a mount: a button, a checkbox or another region raises its node's intents, an
// option is selected and activated, a header button raises the mount's own intent, a region of a drawing its gesture.
function onClick(mount, event) {
  const at = event.target.closest?.("[data-act]");
  if (!at || !mount.element.contains(at) || at.getAttribute("aria-disabled") === "true") return;
  switch (at.dataset.act) {
    case "node":
      activate(mount, at.dataset.node, undefined);
      break;
    case "option":
      choose(at.parentElement, [...at.parentElement.children].indexOf(at));
      fire(mount, at.parentElement);
      break;
    case "cell": {
      const owner = at.closest("[data-act=grid]");
      point(owner, at);
      activate(mount, owner.dataset.node, at.dataset.value);
      break;
    }
    case "binding":
      fireBinding(mount, Number(at.dataset.binding));
      break;
    case "intent": {
      const decl = (mount.snapshot.intents ?? []).find((d) => !d.nodeKey && d.name === at.dataset.intent);
      if (decl) raise(mount, decl, "", undefined);
      break;
    }
    case "region":
      if (mount.dragged) mount.dragged = false;
      else gesture(mount, at.dataset.region, "activate", pointIn(at, event.clientX, event.clientY), undefined, event.timeStamp);
      break;
  }
}

const ARROWS = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };

// onKey is the one key listener of a mount. The focused control gets the key first (control); a key it does not take
// fires the mount's binding of that key. Escape is neither: it is the user leaving (leave), and the page closes no
// dialog mount by itself.
function onKey(mount, event) {
  if (event.key === "Escape") {
    if (mount.floating) event.preventDefault();
    return void leave(mount);
  }
  if (control(mount, event)) return;
  const hit = bound(mount, event);
  if (!hit) return;
  event.preventDefault();
  fireBinding(mount, hit.i);
}

// dismissal is the intent a dialog or an overlay mount declares `dismiss`: the one the user's leaving raises.
const dismissal = (snapshot) => (snapshot.slot === "dialog" || snapshot.slot === "overlay" ? (snapshot.intents ?? []).find((decl) => decl.dismiss) : undefined);

// leave is Escape in a mount, or the browser's request to close a dialog mount. It cancels the mount's cancellable
// intent in flight; with none in flight it raises the mount's `dismiss` intent with {}, once until that intent has
// settled, and the extension hides or removes the mount. A mount that declares no such intent stays as it is.
function leave(mount) {
  if (mount.cancel) return void mount.cancel();
  const decl = dismissal(mount.snapshot);
  if (!decl || mount.leaving) return;
  mount.leaving = true;
  raise(mount, decl, "", undefined, undefined, { arg: {}, quiet: true }).finally(() => {
    mount.leaving = false;
  });
}

// control gives a key to the control that holds the focus and reports whether the control took it. A native control
// handles its own keys (a button and a checkbox Enter and Space, a text field what is typed); the page handles the
// arrows and Enter of a list and a grid, Enter of an input, Ctrl+Enter of an editor, Enter and Space of a region that
// is not a button, and the keys of a drawing's region.
function control(mount, event) {
  const { key } = event;
  const press = key === "Enter" || key === " ";
  const at = event.target.closest?.("[data-act]");
  if (!at || !mount.element.contains(at) || at.getAttribute("aria-disabled") === "true") return false;
  switch (at.dataset.act) {
    case "list": {
      const count = at.children.length;
      const step = at.getAttribute("role") === "tablist" ? { ArrowRight: 1, ArrowLeft: -1 }[key] : undefined;
      const move = { ArrowDown: 1, ArrowUp: -1 }[key] ?? step;
      if (move) {
        event.preventDefault();
        choose(at, (cursor(at) + move + count) % Math.max(count, 1));
      } else if (key === "Enter") {
        event.preventDefault();
        fire(mount, at);
      }
      return Boolean(move) || key === "Enter";
    }
    case "grid": {
      const cells = [...at.querySelectorAll("[role=gridcell]")];
      const from = cells.findIndex((cell) => cell.getAttribute("aria-selected") === "true");
      const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -at.view.columns, ArrowDown: at.view.columns }[key];
      if (step) {
        event.preventDefault();
        let to = from < 0 ? cells.findIndex((cell) => cell.dataset.value) : from + step;
        // Along a row the cursor passes over a cell that cannot be chosen.
        while (Math.abs(step) === 1 && cells[to] && !cells[to].dataset.value) to += step;
        if (cells[to]?.dataset.value) point(at, cells[to]);
      } else if (press && cells[from]) {
        event.preventDefault();
        activate(mount, at.dataset.node, cells[from].dataset.value);
      }
      return Boolean(step) || press;
    }
    case "submit":
      if (key === "Enter" && (at.tagName === "INPUT" || event.ctrlKey || event.metaKey)) activate(mount, at.dataset.node, at.value);
      // A text field takes what is typed into it; a key held with Ctrl or Alt and a function key are not typing.
      return !(event.ctrlKey || event.altKey || /^F\d/.test(key)) || key === "Enter";
    case "node":
      // A button and a checkbox click themselves on Enter and Space.
      if (press && at.tagName !== "BUTTON" && at.tagName !== "INPUT") {
        event.preventDefault();
        activate(mount, at.dataset.node, undefined);
      }
      return press;
    case "intent": case "binding":
      return press;
    case "region": {
      const box = at.getBBox();
      const centre = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
      const arrow = wants(mount, at.dataset.region, "drag") ? ARROWS[key] : undefined;
      if (press) {
        event.preventDefault();
        gesture(mount, at.dataset.region, "activate", centre, undefined, event.timeStamp);
      } else if (arrow) {
        // The keyboard's drag: an eighth of the drawing's viewport in the arrow's direction, then the release.
        event.preventDefault();
        const view = at.ownerSVGElement.viewBox.baseVal;
        const by = { x: (arrow[0] * (view.width || at.ownerSVGElement.clientWidth)) / 8, y: (arrow[1] * (view.height || at.ownerSVGElement.clientHeight)) / 8 };
        gesture(mount, at.dataset.region, "drag", centre, by, event.timeStamp);
        gesture(mount, at.dataset.region, "release", centre);
      }
      return press || Boolean(arrow);
    }
  }
  return false;
}

// A drawing (D614): a scene of vector shapes, or a pixel grid over an image asset, drawn as one SVG element, and the
// regions that turn a gesture into a declared intent as shapes of that element that take the pointer and the keyboard.
const SVG = "http://www.w3.org/2000/svg";
const FIT = { contain: "xMidYMid meet", cover: "xMidYMid slice", stretch: "none" };
const ANCHOR = { center: "middle", end: "end" };

function svg(tag, attributes = {}) {
  const el = document.createElementNS(SVG, tag);
  for (const [name, value] of Object.entries(attributes)) if (value !== undefined) el.setAttribute(name, String(value));
  return el;
}

// paint is a shape's colour as CSS: the colour itself or the theme token's custom property; a token the page has no
// colour for paints nothing.
const paint = (value) => (HEX.test(value) ? value : TOKEN.test(value ?? "") ? `var(--pig-${value}, none)` : "none");

// bytesOf finds the bytes of an asset a drawing names: the asset's declaration gives the SHA-256 of its bytes, and the
// bytes ride in the view's images under that hash as their ref (or under the asset's id).
function bytesOf(snapshot, id) {
  const declared = (snapshot.assets ?? []).find((asset) => asset.id === id);
  const bytes = (snapshot.view?.images ?? []).find((image) => image.ref === declared?.sha256 || image.ref === id);
  return bytes ? `data:${bytes.mimeType};base64,${bytes.data}` : undefined;
}

const PATH = {
  move: (c) => `M${c.x ?? 0} ${c.y ?? 0}`,
  line: (c) => `L${c.x ?? 0} ${c.y ?? 0}`,
  quad: (c) => `Q${c.x1 ?? 0} ${c.y1 ?? 0} ${c.x ?? 0} ${c.y ?? 0}`,
  cubic: (c) => `C${c.x1 ?? 0} ${c.y1 ?? 0} ${c.x2 ?? 0} ${c.y2 ?? 0} ${c.x ?? 0} ${c.y ?? 0}`,
  close: () => "Z",
};

// shape draws one shape of a scene as its SVG element. A shape with a key carries it (data-k), so morph keeps its
// element across updates and a shape that moved is the same element with other attributes.
function shape(s, snapshot) {
  let el;
  switch (s.kind) {
    case "group":
      el = svg("g");
      for (const c of s.children ?? []) el.append(shape(c, snapshot));
      break;
    case "rect":
      el = svg("rect", { x: s.x ?? 0, y: s.y ?? 0, width: s.width ?? 0, height: s.height ?? 0, rx: s.radius });
      break;
    case "ellipse":
      el = svg("ellipse", { cx: s.cx ?? 0, cy: s.cy ?? 0, rx: s.rx ?? 0, ry: s.ry ?? 0 });
      break;
    case "line":
      el = svg("line", { x1: s.x1 ?? 0, y1: s.y1 ?? 0, x2: s.x2 ?? 0, y2: s.y2 ?? 0 });
      break;
    case "polyline":
      el = svg(s.closed ? "polygon" : "polyline", { points: (s.points ?? []).map((p) => `${p.x ?? 0},${p.y ?? 0}`).join(" ") });
      break;
    case "path":
      el = svg("path", { d: (s.commands ?? []).map((c) => PATH[c.op]?.(c) ?? "").join("") });
      break;
    case "text":
      el = svg("text", { x: s.x ?? 0, y: s.y ?? 0, "font-size": s.size || 12, "text-anchor": ANCHOR[s.align] });
      el.textContent = s.text ?? "";
      break;
    case "image":
      el = svg("image", { x: s.x ?? 0, y: s.y ?? 0, width: s.width ?? 0, height: s.height ?? 0, href: bytesOf(snapshot, s.asset), preserveAspectRatio: "none" });
      break;
    default:
      return svg("g");
  }
  if (s.key) el.setAttribute("data-k", s.key);
  if (s.kind !== "group" && s.kind !== "image") {
    const stroke = s.stroke ? `;stroke:${paint(s.stroke.color)};stroke-width:${s.stroke.width ?? 0}` : "";
    el.setAttribute("style", `fill:${s.fill ? paint(s.fill) : s.kind === "text" ? "currentColor" : "none"}${stroke}`);
  }
  if (s.opacity !== undefined) el.setAttribute("opacity", String(s.opacity));
  if (s.transform) el.setAttribute("transform", `matrix(${s.transform.join(" ")})`);
  return el;
}

// scene draws a snapshot's drawing. The viewport is the SVG's viewBox and its fit the way the box is filled; the
// element is as wide as its place and as high as the viewport's proportions make it, unless its place has a height.
function scene(snapshot) {
  const d = snapshot.drawing;
  const root = svg("svg", { class: "drawing" });
  const pixels = d.profile === "pixels" ? d.pixels : undefined;
  const view = d.viewport ?? (pixels ? { x: 0, y: 0, width: pixels.width, height: pixels.height, fit: "contain" } : undefined);
  if (view) {
    root.setAttribute("viewBox", `${view.x ?? 0} ${view.y ?? 0} ${view.width} ${view.height}`);
    root.setAttribute("preserveAspectRatio", FIT[view.fit] ?? FIT.contain);
    root.style.aspectRatio = `${view.width} / ${view.height}`;
  }
  if (pixels) {
    const grid = root.appendChild(svg("image", { x: 0, y: 0, width: pixels.width, height: pixels.height, href: bytesOf(snapshot, pixels.asset), preserveAspectRatio: "none" }));
    grid.style.imageRendering = pixels.scaling === "nearest" ? "pixelated" : "auto";
  }
  for (const s of d.shapes ?? []) root.append(shape(s, snapshot));
  for (const region of d.regions ?? []) {
    const hit = shape({ ...region.shape, key: undefined, fill: undefined, stroke: undefined, opacity: undefined }, snapshot);
    hit.setAttribute("class", "region");
    hit.setAttribute("data-k", `region ${region.key}`);
    hit.setAttribute("data-act", "region");
    hit.setAttribute("data-region", region.key);
    hit.setAttribute("data-gestures", region.gestures.join(" "));
    hit.setAttribute("tabindex", "0");
    hit.setAttribute("role", region.role ?? "button");
    hit.setAttribute("aria-label", region.name);
    hit.removeAttribute("style");
    hit.appendChild(svg("title")).textContent = region.name;
    root.append(hit);
  }
  return root;
}

// wants: the region of the mount's drawing takes that gesture.
function wants(mount, key, name) {
  return (mount.snapshot.drawing?.regions ?? []).some((region) => region.key === key && region.gestures.includes(name));
}

// pointIn is a pointer position in the coordinates of the drawing a shape belongs to.
function pointIn(el, clientX, clientY) {
  const matrix = (el.ownerSVGElement ?? el).getScreenCTM();
  if (!matrix) return { x: 0, y: 0 };
  const p = new DOMPoint(clientX, clientY).matrixTransform(matrix.inverse());
  return { x: p.x, y: p.y };
}

// gesture raises the intent of a region for one gesture, when the region takes that gesture. What the gesture was is
// offered to the intent's argument under the names x and y (the pointer in drawing coordinates), dx and dy (a drag's
// movement since its last event), gesture and region; `argument` fills only the ones the intent's shape declares.
// The gestures of a mount are raised one after the other, each when the one before it is answered: an intent is
// raised on the view the page holds, and the answer to the gesture before may have drawn a new one.
function gesture(mount, key, name, point, by, since) {
  return later(mount, () => send(mount, key, name, point, by, since));
}

// send raises a region's intent for a gesture now, on the view the page holds. It is a step of the mount's gestures.
function send(mount, key, name, point, by = { x: 0, y: 0 }, since) {
  const region = (mount.snapshot.drawing?.regions ?? []).find((r) => r.key === key && r.gestures.includes(name));
  const decl = region && (mount.snapshot.intents ?? []).find((d) => d.name === region.intent);
  if (!decl) return undefined;
  const round = (v) => Math.round(v * 1000) / 1000;
  const extra = { x: round(point.x), y: round(point.y), dx: round(by.x), dy: round(by.y), gesture: name, region: key };
  return raise(mount, decl, decl.nodeKey ?? "", undefined, undefined, { extra, quiet: name !== "activate", since });
}

// later runs a step of the mount's gestures after the steps before it.
function later(mount, step) {
  // A step that failed does not hold up the ones after it.
  return (mount.gestures = (mount.gestures ?? Promise.resolve()).catch(() => {}).then(step));
}

// The pointer on a region: press when it goes down, drag while it moves, release when it goes up. One intent of a
// drag is in flight at a time, because each is raised on the view the last one drew: the moves made meanwhile are
// summed into the next drag, which is sent on an animation frame and at most 30 times a second. The release is always
// sent, after the last drag.
const DRAG_INTERVAL = 1000 / 30;

function onPointerDown(mount, event) {
  const at = event.target.closest?.("[data-act=region]");
  if (!at || event.button !== 0 || !mount.element.contains(at)) return;
  const drag = (mount.drag = { key: at.dataset.region, pointer: event.pointerId, at: [event.clientX, event.clientY], moved: false, sent: 0 });
  mount.dragged = false;
  // While the pointer drags, the scene follows it at once: no transition between its updates.
  if (wants(mount, drag.key, "drag")) mount.element.classList.add("dragging");
  if (wants(mount, drag.key, "drag") || wants(mount, drag.key, "release")) {
    try {
      at.setPointerCapture(event.pointerId);
    } catch {
      // A pointer the browser does not know (a scripted event) cannot be captured; its events still bubble here.
    }
  }
  gesture(mount, drag.key, "press", pointIn(at, event.clientX, event.clientY), undefined, event.timeStamp);
}

function onPointerMove(mount, event) {
  const drag = mount.drag;
  if (!drag || event.pointerId !== drag.pointer) return;
  if (!drag.moved && Math.hypot(event.clientX - drag.at[0], event.clientY - drag.at[1]) < 3) return;
  drag.moved = mount.dragged = true;
  drag.next = [event.clientX, event.clientY, event.timeStamp];
  pump(mount, drag);
}

function onPointerUp(mount, event) {
  const drag = mount.drag;
  if (!drag || event.pointerId !== drag.pointer) return;
  mount.drag = undefined;
  if (drag.moved) drag.next = [event.clientX, event.clientY, event.timeStamp];
  later(mount, () => move(mount, drag));
  later(mount, () => {
    const at = mount.element.querySelector(`[data-region="${CSS.escape(drag.key)}"]`);
    return at && send(mount, drag.key, "release", pointIn(at, event.clientX, event.clientY));
  });
  later(mount, () => mount.drag || mount.element.classList.remove("dragging"));
}

// pump sends the drag's pending move once the one before it is answered and its turn has come.
function pump(mount, drag) {
  if (drag.pumping || !drag.next) return;
  drag.pumping = true;
  later(mount, async () => {
    await new Promise((resolve) => setTimeout(() => requestAnimationFrame(resolve), Math.max(0, drag.sent + DRAG_INTERVAL - performance.now())));
    await move(mount, drag);
    drag.pumping = false;
    if (mount.drag === drag) pump(mount, drag);
  });
}

// move raises the drag gesture for the movement since the last one sent, in the drawing's units as it is drawn now.
// It is a step of the mount's gestures.
async function move(mount, drag) {
  const at = mount.element.querySelector(`[data-region="${CSS.escape(drag.key)}"]`);
  if (!drag.next || !at) return;
  const [x, y, since] = drag.next;
  drag.next = undefined;
  const matrix = at.ownerSVGElement.getScreenCTM();
  const by = { x: (x - drag.at[0]) / (matrix?.a || 1), y: (y - drag.at[1]) / (matrix?.d || 1) };
  drag.at = [x, y];
  drag.sent = performance.now();
  await send(mount, drag.key, "drag", pointIn(at, x, y), by, since);
}

// activate raises every intent the mount declares for the node.
function activate(mount, nodeKey, value, item) {
  for (const decl of mount.snapshot.intents ?? []) if (decl.nodeKey === nodeKey) raise(mount, decl, nodeKey, value, item);
}

// argument builds an intent's argument from its declared shape. An object property named `value` takes the node's
// value and one named `item` the activated item's id; for a gesture on a region of a drawing, the properties named x,
// y, dx, dy, gesture and region take what the gesture was (`extra`). A property the shape does not declare is never
// sent. A string, number or boolean shape takes the node's value itself. It returns undefined when the shape asks for
// more than the node gives: the page then asks the user.
function argument(shape, value, item, extra = {}) {
  const fit = (s, v) => {
    if (v === undefined) return undefined;
    if (s.type === "string" || s.type === "enum") return String(v);
    if (s.type === "integer" || s.type === "number") return Number.isFinite(Number(v)) ? Number(v) : undefined;
    if (s.type === "boolean") return Boolean(v);
    return undefined;
  };
  if (shape.type !== "object") return fit(shape, value ?? true);
  const arg = {};
  const given = { ...extra, value, item };
  for (const [name, sub] of Object.entries(shape.properties ?? {})) {
    // A pointer position is a fraction of a unit; an integer property takes the nearest whole one.
    const v = fit(sub, sub.type === "integer" && typeof extra[name] === "number" ? Math.round(extra[name]) : given[name]);
    if (v !== undefined) arg[name] = v;
    else if ((shape.required ?? []).includes(name)) return undefined;
  }
  return arg;
}

// ask draws a form for the argument's shape and resolves with what the user entered.
function ask(mount, decl) {
  return new Promise((resolve) => {
    const form = document.createElement("form");
    form.className = "arg";
    const fields = Object.entries(decl.arg.type === "object" ? decl.arg.properties ?? {} : { value: decl.arg });
    for (const [name, sub] of fields) {
      const label = document.createElement("label");
      label.textContent = `${name} `;
      let input = document.createElement("input");
      if (sub.enum) {
        input = document.createElement("select");
        for (const choice of sub.enum) input.append(new Option(choice));
      } else if (sub.type === "boolean") input.type = "checkbox";
      else if (sub.type === "integer" || sub.type === "number") input.type = "number";
      else if (sub.type === "object" || sub.type === "array") input.placeholder = "JSON";
      input.name = name;
      label.append(input);
      form.append(label);
    }
    const sendButton = document.createElement("button");
    sendButton.textContent = words(decl.name);
    form.append(sendButton);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const arg = {};
      for (const [name, sub] of fields) {
        const input = form.elements[name];
        if (sub.type === "boolean") arg[name] = input.checked;
        else if (input.value === "") continue;
        else if (sub.type === "integer" || sub.type === "number") arg[name] = Number(input.value);
        else if (sub.type === "object" || sub.type === "array") arg[name] = JSON.parse(input.value);
        else arg[name] = input.value;
      }
      form.remove();
      resolve(decl.arg.type === "object" ? arg : arg.value);
    });
    mount.footer.before(form);
    form.elements[0]?.focus();
  });
}

// raise sends one intent and shows how it settled in the mount's status line. A binding gives the argument itself. A gesture of a drawing other than
// activate is quiet: it writes the line only when it did not settle done (or stale, which the next gesture repairs).
async function raise(mount, decl, nodeKey, value, item, { extra, quiet = false, since, arg: constant } = {}) {
  let arg = constant ?? argument(decl.arg, value, item, extra);
  if (arg === undefined) arg = await ask(mount, decl);
  const { key, epoch, rev } = mount.snapshot.mount;
  const id = `w${++nextId}`;
  const command = { type: "ui_view_intent", attachment, plugin: mount.snapshot.plugin, mount: { key, epoch, rev }, intent: decl.name, arg };
  if (nodeKey) command.nodeKey = nodeKey;
  if (!quiet) {
    delete mount.footer.dataset.outcome;
    mount.footer.textContent = `${decl.name} … `;
  }
  if (decl.cancellable) {
    // The carrier's cancel names the intent request by its id.
    const cancel = () => request({ type: "ui_view_intent_cancel" }, id);
    mount.cancel = cancel;
    if (!quiet) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = "Cancel";
      button.addEventListener("click", cancel);
      mount.footer.append(button);
    }
  }
  const posted = performance.now();
  const reply = await request(command, id);
  const result = reply.success ? reply.data : { outcome: reply.code, error: reply.error };
  // The mount may have been redrawn while the intent was in flight: write to the one on the page.
  mount = mounts.get(`${mount.snapshot.plugin}\n${key}`) ?? mount;
  mount.cancel = undefined;
  if (!quiet || (result.outcome !== "done" && result.outcome !== "stale")) {
    mount.footer.textContent = `${decl.name}: ${result.outcome}${result.error ? ` (${result.error})` : ""}${result.answer === undefined ? "" : ` ${JSON.stringify(result.answer)}`}`;
    mount.footer.dataset.outcome = result.outcome;
    mount.footer.toggleAttribute("data-answer", result.answer !== undefined);
  }
  // A checkbox the user clicked shows what the view says, whether or not the intent changed it.
  for (const box of mount.body.querySelectorAll("input[type=checkbox], input[type=radio]")) box.checked = box.hasAttribute("checked");
  if (mount.drawnAt > posted) window.uiView.timing.intent = mount.drawnAt - posted;
  if (since !== undefined && mount.drawnAt > since) window.uiView.timing.gesture = mount.drawnAt - since;
  return result;
}

const RELATIONS = { labelledBy: "aria-labelledby", describedBy: "aria-describedby", controls: "aria-controls", owns: "aria-owns" };
const STATES = { selected: "aria-selected", checked: "aria-checked", expanded: "aria-expanded", busy: "aria-busy", disabled: "aria-disabled", readOnly: "aria-readonly", required: "aria-required", invalid: "aria-invalid" };
// The states a native control has an attribute of its own for.
const NATIVE = { checked: "input[type=checkbox], input[type=radio]", disabled: "button, input, textarea", readOnly: "input[type=text], textarea", required: "input, textarea" };

// applyA11y sets what the snapshot's a11y records say about the nodes they name, on the freshly drawn elements. A
// role that made the node the control it is (ctx.owned) is not written again, and a state a native control has is set
// as that control's own attribute: a checkbox is checked, a button disabled.
function applyA11y(snapshot, ctx, prefix) {
  for (const entry of snapshot.a11y ?? []) {
    const el = ctx.ids.get(entry.node);
    if (!el) continue;
    if (entry.name) el.setAttribute("aria-label", entry.name);
    if (entry.description) el.setAttribute("aria-description", entry.description);
    if (entry.role && !ctx.owned.has(entry.node)) el.setAttribute("role", entry.role);
    for (const [member, attribute] of Object.entries(STATES)) {
      if (entry[member] === undefined) continue;
      if (NATIVE[member] && el.matches(NATIVE[member])) el.toggleAttribute(member.toLowerCase(), entry[member]);
      else el.setAttribute(attribute, String(entry[member]));
    }
    const related = {};
    for (const relation of entry.relations ?? []) (related[RELATIONS[relation.kind]] ??= []).push(prefix + relation.target);
    for (const [attribute, ids] of Object.entries(related)) el.setAttribute(attribute, ids.join(" "));
  }
}

// applyFocus moves the focus where the snapshot asks: to the node whose focus record requests it, and on an update
// to the node the view's `focus` member names when that member changed and the focus is inside the mount. The order
// of the focus records is the tab order (draw).
function applyFocus(mount, isNew, before) {
  const order = mount.snapshot.focus ?? [];
  // A record asks for the focus when the mount is new (`mount`) or with an update (`update`). The page gives it unless
  // the user is typing elsewhere, and then says so; a dialog mount has said so as it opened (present).
  const asked = order.find((entry) => entry.request === (isNew ? "mount" : "update"));
  if (asked && !busy(mount.element)) mount.ids.get(asked.node)?.focus();
  else if (asked && !mount.floating) report(mount, "focus", "a text field holds text", "unfocused", "info");
  const lead = mount.snapshot.view?.focus;
  if (!isNew && lead && lead !== before?.view?.focus && mount.element.contains(document.activeElement)) mount.ids.get(lead)?.focus();
  const restore = order.find((entry) => entry.restore);
  if (restore?.restore === "self") mount.restore = undefined;
}

function report(mount, path, message, code = "undrawn", severity = "warning") {
  window.uiView.reports++;
  const { key, epoch } = mount.snapshot.mount;
  request({ type: "ui_view_report", attachment, plugin: mount.snapshot.plugin, mount: { key, epoch }, notice: { code, severity, path, message } });
}

document.getElementById("prompt").addEventListener("submit", (event) => {
  event.preventDefault();
  const input = event.target.elements.message;
  if (!input.value || attached) return;
  say(input.value, "user");
  request({ type: "prompt", message: input.value });
  input.value = "";
});

// lost forgets what belonged to a connection that ended: its registry is out of reach, so every mount goes from the
// page and no reply is waited for. The next `session` attaches to what follows.
function lost(why) {
  session = "";
  attachment = 0;
  lastSeq = 0;
  resyncing = false;
  pending.clear();
  for (const [key, mount] of mounts) drop(key, mount);
  state.textContent = why;
}

// signIn trades the address's code for this tab's secret, and says whether the tab holds one. A code that is refused leaves
// a secret the tab already holds in place: the stream says whether the bridge still knows it.
async function signIn() {
  if (code) {
    try {
      const res = await fetch("/exchange", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ code }) });
      if (res.ok) {
        secret = (await res.json()).secret;
        sessionStorage.setItem(SECRET_KEY, secret);
      }
    } catch {
      // The bridge did not answer: the code may be used up, and the tab holds what it held.
    }
  }
  if (!secret) state.textContent = code ? USED : SIGNED_OUT;
  return Boolean(secret);
}

function authorization() {
  return { authorization: `Bearer ${secret}` };
}

// The bridge refused the secret: forget it, close the stream and say what to do. Nothing is asked of the bridge again.
let stopped = false;
let finished = false;
let stream = null;
function signedOut(message) {
  stopped = true;
  secret = null;
  sessionStorage.removeItem(SECRET_KEY);
  stream?.abort();
  lost(message);
}

// What the bridge's events do, by their name.
const handlers = {
  session(data) {
    session = data;
    attach();
  },
  message(data) {
    onLine(JSON.parse(data));
  },
  // With --connect the bridge says what it does about the session's socket. The page is then a client of a running
  // session's view endpoint, which serves the carrier and nothing else: the prompt goes and one line says why.
  endpoint(data) {
    if (!attached) {
      attached = true;
      const form = document.getElementById("prompt");
      form.hidden = true;
      for (const control of form.elements) control.disabled = true;
      document.getElementById("attached").hidden = false;
      // The page has no chat of its own: the session's panels take it (index.html, body.attached).
      document.body.classList.add("attached");
    }
    state.textContent = JSON.parse(data).reason;
  },
  end(data) {
    const end = JSON.parse(data);
    // With --connect the session closed the connection and the bridge opens the next one: the stream stays.
    if (end.retry) return lost(end.reason);
    state.textContent = end.reason ?? "pig ended";
    finished = true;
  },
};

// read dispatches the Server-Sent Events of a response body as the browser's EventSource does: `event:` names the next
// event, `data:` lines are its data, a blank line ends it.
async function read(body) {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  let name = "message";
  let data = [];
  for (;;) {
    const chunk = await reader.read();
    if (chunk.done) return;
    buffer += chunk.value;
    for (let at = buffer.indexOf("\n"); at >= 0; at = buffer.indexOf("\n")) {
      const line = buffer.slice(0, at);
      buffer = buffer.slice(at + 1);
      if (line === "") {
        if (data.length > 0 && Object.hasOwn(handlers, name)) handlers[name](data.join("\n"));
        name = "message";
        data = [];
      } else if (line.startsWith("event:")) {
        name = line.slice(6).replace(/^ /, "");
      } else if (line.startsWith("data:")) {
        data.push(line.slice(5).replace(/^ /, ""));
      }
    }
  }
}

// The stream from the bridge is a fetch with the Authorization header: an EventSource cannot send one, and the secret must
// not be in the stream's address. When it breaks (the bridge cut a page that did not read, or the bridge ended) the page
// connects again after a second, with the same secret, as the browser's EventSource did by itself.
async function follow() {
  while (!stopped && !finished) {
    stream = new AbortController();
    let response = null;
    try {
      response = await fetch("/events", { headers: authorization(), cache: "no-store", signal: stream.signal });
    } catch {
      // The bridge is not there: try again.
    }
    if (stopped) return;
    if (response?.status === 403) return signedOut(RESTARTED);
    if (response?.ok && response.body) {
      try {
        await read(response.body);
      } catch {
        // The connection broke.
      }
    }
    if (stopped || finished) return;
    lost("the stream from the bridge broke; connecting again");
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

if (await signIn()) follow();
