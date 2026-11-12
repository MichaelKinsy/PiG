// A reference client of the plugin UI carrier (docs/ui-view-carrier.md). It attaches over the bridge, keeps the
// client's side of the carrier's rules (numbers without a hole, a resync after a gap, a redraw only when the digest
// changed), draws the snapshots it is sent, applies their a11y and focus records, and raises their intents.

const SLOTS = ["notify", "status", "widget-above", "widget-below", "header", "footer", "dialog", "overlay", "panel", "tool-card", "message", "entry"];
// The node kinds this page draws. A kind outside the list is refused by the host for this client; a node the page
// cannot draw after all is reported with ui_view_report.
const NODES = ["container", "box", "vstack", "hstack", "text", "truncated-text", "markdown", "lines", "spacer", "dynamic-border", "image",
  "select-list", "settings-list", "input", "editor", "mouse-region", "scroll-view"];
const ABILITIES = {
  slots: SLOTS, nodes: NODES, markdown: true, imageFormats: ["png", "jpeg", "gif", "webp"],
  limits: { nodes: 2048, depth: 32, snapshotBytes: 1 << 20, textBytes: 1 << 16, mounts: 32, intentArgBytes: 1 << 16 },
  input: { keyboard: true, pointer: true, text: true },
  claims: [{ name: "client.kind", value: "web" }],
};

const token = new URLSearchParams(location.search).get("token");
const state = document.getElementById("state");
const chat = document.getElementById("chat");
let session = "";
let attachment = 0;
let lastSeq = 0;
let resyncing = false;
let nextId = 0;
let nextMount = 0;
const pending = new Map(); // command + " " + id -> resolve
const mounts = new Map(); // plugin + "\n" + key -> { snapshot, element, ids, prefix }

// What the page exposes to a test or a console: the carrier state it keeps.
window.uiView = { mounts, get attachment() { return attachment; }, get lastSeq() { return lastSeq; }, resyncs: 0, gaps: 0, reports: 0 };

function say(text, kind = "note") {
  const line = document.createElement("div");
  line.className = `line ${kind}`;
  line.textContent = text;
  chat.append(line);
  line.scrollIntoView({ block: "nearest" });
}

function post(command) {
  return fetch(`/send?token=${token}&session=${session}`, { method: "POST", body: JSON.stringify(command) });
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
  state.textContent = `attachment ${attachment}, event ${lastSeq}, ${mounts.size} mounts`;
}

async function attach() {
  for (const [key, mount] of mounts) drop(key, mount);
  const reply = await request({ type: "ui_view_attach", abilities: ABILITIES });
  if (!reply.success) return void (state.textContent = `attach failed: ${reply.error}`);
  adopt(reply.data);
}

function drop(key, mount, restore = true) {
  const focused = mount.element.contains(document.activeElement);
  mount.element.remove();
  mounts.delete(key);
  if (focused && restore) mount.restore?.focus();
}

function apply(snapshot) {
  const key = `${snapshot.plugin}\n${snapshot.mount.key}`;
  const held = mounts.get(key);
  if (["finished", "removed", "broken"].includes(snapshot.state)) {
    if (held) drop(key, held);
    const why = snapshot.reason ? ` (${snapshot.reason})` : "";
    const result = snapshot.result === undefined ? "" : `: ${JSON.stringify(snapshot.result)}`;
    say(`${snapshot.plugin}/${snapshot.mount.key} ${snapshot.state}${why}${result}`);
  } else if (held && held.snapshot.mount.epoch === snapshot.mount.epoch && snapshot.digest && held.snapshot.digest === snapshot.digest) {
    // The same content: nothing is drawn.
    held.snapshot = snapshot;
    held.element.hidden = snapshot.state === "hidden";
  } else {
    draw(key, snapshot, held);
  }
  state.textContent = `attachment ${attachment}, event ${lastSeq}, ${mounts.size} mounts`;
}

// draw shows a snapshot. A mount already on the page keeps its element and morph keeps what it can of its view, so an
// update leaves what the user is typing, the selection and the focus where they are.
function draw(key, snapshot, held) {
  const mount = held ?? { prefix: `m${++nextMount}-`, restore: document.activeElement };
  const was = mount.ids ?? new Map();
  mount.snapshot = snapshot;
  mount.ids = new Map();
  if (!held) {
    mount.element = document.createElement("article");
    mount.element.className = "mount";
    mount.element.dataset.plugin = snapshot.plugin;
    mount.element.dataset.key = snapshot.mount.key;
    mount.head = document.createElement("header");
    mount.head.append(document.createElement("span"));
    mount.body = text("view", "");
    mount.footer = document.createElement("footer");
    mount.element.append(mount.head, mount.body, mount.footer);
  }
  mount.element.hidden = snapshot.state === "hidden";
  mount.head.firstChild.textContent = `${snapshot.plugin} / ${snapshot.mount.key}${snapshot.degraded ? " (degraded)" : ""}`;
  mount.body.removeAttribute("style");
  theme(mount.body, snapshot.view);

  const undrawn = [];
  const fresh = snapshot.view?.root ? node(snapshot.view.root, mount, "root", undrawn, snapshot.view) : text("undrawn", snapshot.degraded ? "This view does not fit this client." : "");
  const focused = mount.element.contains(document.activeElement) ? document.activeElement : null;
  const selection = focused && typed(focused) ? [focused.selectionStart, focused.selectionEnd, focused.selectionDirection] : null;
  const root = mount.body.firstChild;
  if (!root) mount.body.append(fresh);
  else if (morph(root, fresh, mount) !== root) root.replaceWith(fresh);
  // A text node that morph could not keep in place (it moved to another parent) hands its text to the element drawn for it.
  for (const [id, el] of mount.ids) {
    const old = was.get(id);
    if (old && old !== el && typed(el) && old.tagName === el.tagName && (el.view.value ?? "") === (old.view.value ?? "")) el.value = old.value;
  }
  const actions = JSON.stringify((snapshot.intents ?? []).filter((decl) => !decl.nodeKey));
  if (actions !== mount.actions) {
    mount.actions = actions;
    mount.head.replaceChildren(mount.head.firstChild);
    for (const decl of JSON.parse(actions)) {
      const button = document.createElement("button");
      button.textContent = decl.name;
      button.addEventListener("click", () => raise(mount, decl, "", undefined));
      mount.head.append(button);
    }
  }
  applyA11y(mount);
  if (!held) document.querySelector(`section[data-slot="${snapshot.slot}"]`).append(mount.element);
  mounts.set(key, mount);
  // The focus stays on the element that had it; when that element was drawn again, on the one with its id.
  const target = focused?.isConnected ? focused : focused?.id ? document.getElementById(focused.id) : null;
  if (target && document.activeElement !== target) {
    target.focus({ preventScroll: true });
    if (selection && typed(target)) target.setSelectionRange(...selection);
  }
  applyFocus(mount, !held);
  for (const [path, kind] of undrawn) report(mount, path, `this client does not draw ${kind}`);
}

const typed = (el) => el.tagName === "INPUT" || el.tagName === "TEXTAREA";
// The kinds whose element morph keeps while it morphs their children. Their listeners name only the mount and the id.
const SHELLS = new Set(["container", "box", "vstack", "hstack", "mouse-region", "scroll-view"]);

// morph makes what is drawn (old) show the freshly drawn node and returns the element that stands for it: old when
// both are the same kind under the same id and the kind is a shell or takes text, else fresh. An input or editor that
// is kept keeps the user's text unless the view's value changed. Children with an id are matched by id and the others
// by position, so a node that is gone takes its element with it and never lends it to another id.
function morph(old, fresh, mount) {
  const n = fresh.view;
  if (!n || old.view?.kind !== n.kind || old.view.id !== n.id || !(typed(old) || SHELLS.has(n.kind))) return fresh;
  for (const { name } of [...old.attributes]) if (!fresh.hasAttribute(name)) old.removeAttribute(name);
  for (const { name, value } of fresh.attributes) if (old.getAttribute(name) !== value) old.setAttribute(name, value);
  if (typed(old) && (n.value ?? "") !== (old.view.value ?? "")) old.value = n.value ?? "";
  old.view = n;
  if (n.id) mount.ids.set(n.id, old);
  if (typed(old)) return old;
  const olds = [...old.children];
  const byId = new Map(olds.filter((el) => el.view?.id).map((el) => [el.view.id, el]));
  const kept = new Set();
  [...fresh.children].forEach((child, i) => {
    const match = child.view?.id ? byId.get(child.view.id) : olds[i]?.view?.id ? undefined : olds[i];
    const now = match && !kept.has(match) ? morph(match, child, mount) : child;
    kept.add(now);
    if (old.children[i] !== now) old.insertBefore(now, old.children[i] ?? null);
  });
  for (const el of olds) if (!kept.has(el)) el.remove();
  if (n.kind === "scroll-view" && n.follow === "end") old.scrollTop = old.scrollHeight;
  return old;
}

function text(className, value) {
  const el = document.createElement("div");
  el.className = className;
  el.textContent = value;
  return el;
}

// The sizes of a view (gap, padding, basis, minSize, maxSize, a spacer's lines) are terminal cells (D107). Here a cell
// is 1ch wide (the width of "0" in the view's font) and one line high.
const cols = (n) => `calc(${n} * var(--col))`;
const rows = (n) => `calc(${n} * var(--row))`;
const ALIGN = { stretch: "stretch", start: "flex-start", center: "center", end: "flex-end" };

// colour follows the rule drawings use (coding/uiview/drawing.go, validColor): "#rrggbb" or "#rrggbbaa" is the colour
// itself and a name is a theme token, drawn from the custom property --pig-<token> that the page (index.html) or the
// view's theme sets. A token the page has no colour for draws nothing.
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

// node draws one D107 view node. The kinds that carry an id (select-list, settings-list, input, editor, mouse-region,
// scroll-view) become focusable elements under that id, so a11y and focus records can name them.
function node(n, mount, path, undrawn, view) {
  let el;
  const child = (c, i) => node(c, mount, `${path}/${i}`, undrawn, view);
  const children = (parent) => {
    (n.children ?? []).forEach((c, i) => parent.append(child(c, i)));
    return parent;
  };
  switch (n.kind) {
    case "container":
      el = children(text("container", ""));
      break;
    case "box":
      el = children(text("box", ""));
      pad(el, n, 1);
      el.style.background = colour(n.bg);
      break;
    case "vstack": case "hstack":
      el = stack(n, text(n.kind, ""), child);
      break;
    case "text":
      el = text("text", n.text ?? "");
      pad(el, n, 1);
      el.style.background = colour(n.bg);
      break;
    case "truncated-text":
      el = text("text truncated", n.text ?? "");
      pad(el, n, 0);
      break;
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
      el = text("lines", (n.content ?? []).join("\n"));
      break;
    case "spacer":
      el = text("spacer", "");
      el.style.height = rows(n.lines ?? 1);
      break;
    case "dynamic-border":
      el = document.createElement("hr");
      el.className = "rule";
      el.style.borderColor = colour(n.color);
      break;
    case "image": {
      const bytes = (view.images ?? []).find((image) => image.ref === n.ref);
      if (!bytes) return undrawnNode(n, path, undrawn);
      el = document.createElement("img");
      el.src = `data:${bytes.mimeType};base64,${bytes.data}`;
      el.alt = n.filename ?? "";
      break;
    }
    case "select-list": case "settings-list":
      el = listbox(n, mount);
      break;
    case "input":
      el = document.createElement("input");
      el.value = n.value ?? "";
      el.placeholder = n.placeholder ?? "";
      el.addEventListener("keydown", (event) => event.key === "Enter" && activate(mount, n.id, el.value));
      break;
    case "editor":
      el = document.createElement("textarea");
      el.value = n.value ?? "";
      el.addEventListener("keydown", (event) => event.key === "Enter" && (event.ctrlKey || event.metaKey) && activate(mount, n.id, el.value));
      break;
    case "mouse-region":
      el = children(text("mouse-region", ""));
      el.tabIndex = 0;
      el.addEventListener("click", () => activate(mount, n.id, undefined));
      el.addEventListener("keydown", (event) => (event.key === "Enter" || event.key === " ") && (event.preventDefault(), activate(mount, n.id, undefined)));
      break;
    case "scroll-view":
      el = children(text("scroll-view", ""));
      el.tabIndex = 0;
      if (n.follow === "end") queueMicrotask(() => (el.scrollTop = el.scrollHeight));
      break;
    default:
      return undrawnNode(n, path, undrawn);
  }
  el.view = n;
  if (n.id) {
    el.id = mount.prefix + n.id;
    mount.ids.set(n.id, el);
  }
  return el;
}

function undrawnNode(n, path, undrawn) {
  undrawn.push([path, n.kind]);
  return text("undrawn", `[${n.kind}]`);
}

// listbox draws a select-list or a settings-list: arrow keys move the selection, Enter or a click activates it. A
// settings-list item steps through its values.
function listbox(n, mount) {
  const el = document.createElement("div");
  el.setAttribute("role", "listbox");
  el.tabIndex = 0;
  const items = n.items ?? [];
  let selected = Math.min(Math.max(n.selectedIndex ?? 0, 0), Math.max(items.length - 1, 0));
  const current = items.map((item) => item.currentValue);
  const options = items.map((item, i) => {
    const option = document.createElement("div");
    option.setAttribute("role", "option");
    option.id = `${mount.prefix}${n.id}-${i}`;
    const label = document.createElement("span");
    label.textContent = item.label ?? item.value ?? item.id ?? "";
    const detail = document.createElement("small");
    option.append(label, detail);
    option.addEventListener("click", () => (select(i), fire()));
    return option;
  });
  const select = (i) => {
    selected = i;
    options.forEach((option, j) => {
      option.setAttribute("aria-selected", String(j === i));
      option.lastChild.textContent = n.kind === "settings-list" ? current[j] ?? "" : items[j].description ?? "";
    });
    if (options[i]) el.setAttribute("aria-activedescendant", options[i].id);
  };
  const fire = () => {
    const item = items[selected];
    if (!item) return;
    if (n.kind === "select-list") return activate(mount, n.id, item.value ?? item.label);
    const values = item.values ?? [];
    if (values.length) current[selected] = values[(values.indexOf(current[selected]) + 1) % values.length];
    select(selected);
    activate(mount, n.id, current[selected], item.id);
  };
  el.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      select((selected + (event.key === "ArrowDown" ? 1 : items.length - 1)) % Math.max(items.length, 1));
    } else if (event.key === "Enter") {
      event.preventDefault();
      fire();
    }
  });
  el.append(...options);
  select(selected);
  return el;
}

// activate raises every intent the mount declares for the node.
function activate(mount, nodeKey, value, item) {
  for (const decl of mount.snapshot.intents ?? []) if (decl.nodeKey === nodeKey) raise(mount, decl, nodeKey, value, item);
}

// argument builds an intent's argument from its declared shape. An object property named `value` takes the node's
// value and one named `item` the activated item's id; a string, number or boolean shape takes the node's value itself.
// It returns undefined when the shape asks for more than the node gives: the page then asks the user.
function argument(shape, value, item) {
  const fit = (s, v) => {
    if (v === undefined) return undefined;
    if (s.type === "string" || s.type === "enum") return String(v);
    if (s.type === "integer" || s.type === "number") return Number.isFinite(Number(v)) ? Number(v) : undefined;
    if (s.type === "boolean") return Boolean(v);
    return undefined;
  };
  if (shape.type !== "object") return fit(shape, value ?? true);
  const arg = {};
  const given = { value, item };
  for (const [name, sub] of Object.entries(shape.properties ?? {})) {
    const v = fit(sub, given[name]);
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
    sendButton.textContent = decl.name;
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

async function raise(mount, decl, nodeKey, value, item) {
  let arg = argument(decl.arg, value, item);
  if (arg === undefined) arg = await ask(mount, decl);
  const { key, epoch, rev } = mount.snapshot.mount;
  const id = `w${++nextId}`;
  const command = { type: "ui_view_intent", attachment, plugin: mount.snapshot.plugin, mount: { key, epoch, rev }, intent: decl.name, arg };
  if (nodeKey) command.nodeKey = nodeKey;
  mount.footer.textContent = `${decl.name} … `;
  if (decl.cancellable) {
    // The carrier's cancel names the intent request by its id.
    const cancel = document.createElement("button");
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", () => request({ type: "ui_view_intent_cancel" }, id));
    mount.footer.append(cancel);
  }
  const reply = await request(command, id);
  const result = reply.success ? reply.data : { outcome: reply.code, error: reply.error };
  // The mount may have been redrawn while the intent was in flight: write to the one on the page.
  mount = mounts.get(`${mount.snapshot.plugin}\n${key}`) ?? mount;
  mount.footer.textContent = `${decl.name}: ${result.outcome}${result.error ? ` (${result.error})` : ""}${result.answer === undefined ? "" : ` ${JSON.stringify(result.answer)}`}`;
  mount.footer.dataset.outcome = result.outcome;
}

const RELATIONS = { labelledBy: "aria-labelledby", describedBy: "aria-describedby", controls: "aria-controls", owns: "aria-owns" };
const STATES = { selected: "aria-selected", checked: "aria-checked", expanded: "aria-expanded", busy: "aria-busy", disabled: "aria-disabled", readOnly: "aria-readonly", required: "aria-required", invalid: "aria-invalid" };

// applyA11y sets what the snapshot's a11y records say about the nodes they name.
function applyA11y(mount) {
  for (const entry of mount.snapshot.a11y ?? []) {
    const el = mount.ids.get(entry.node);
    if (!el) continue;
    if (entry.name) el.setAttribute("aria-label", entry.name);
    if (entry.description) el.setAttribute("aria-description", entry.description);
    if (entry.role) el.setAttribute("role", entry.role);
    for (const [member, attribute] of Object.entries(STATES)) if (entry[member] !== undefined) el.setAttribute(attribute, String(entry[member]));
    const related = {};
    for (const relation of entry.relations ?? []) (related[RELATIONS[relation.kind]] ??= []).push(mount.prefix + relation.target);
    for (const [attribute, ids] of Object.entries(related)) el.setAttribute(attribute, ids.join(" "));
  }
}

// applyFocus puts the named nodes in the focus order the snapshot gives and, on a new mount, moves the focus to the
// node that requests it.
function applyFocus(mount, isNew) {
  const order = mount.snapshot.focus ?? [];
  order.forEach((entry, i) => {
    const el = mount.ids.get(entry.node);
    if (el) el.tabIndex = i + 1;
  });
  const asked = order.find((entry) => entry.request === "mount");
  if (isNew && asked) mount.ids.get(asked.node)?.focus();
  const restore = order.find((entry) => entry.restore);
  if (restore?.restore === "self") mount.restore = undefined;
}

function report(mount, path, message) {
  window.uiView.reports++;
  const { key, epoch } = mount.snapshot.mount;
  request({ type: "ui_view_report", attachment, plugin: mount.snapshot.plugin, mount: { key, epoch }, notice: { code: "undrawn", severity: "warning", path, message } });
}

document.getElementById("prompt").addEventListener("submit", (event) => {
  event.preventDefault();
  const input = event.target.elements.message;
  if (!input.value) return;
  say(input.value, "user");
  request({ type: "prompt", message: input.value });
  input.value = "";
});

const source = new EventSource(`/events?token=${token}`);
source.addEventListener("session", (event) => {
  session = event.data;
  attach();
});
source.addEventListener("message", (event) => onLine(JSON.parse(event.data)));
source.addEventListener("end", () => {
  state.textContent = "pig ended";
  source.close();
});
