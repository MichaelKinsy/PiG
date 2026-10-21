// The global state pi-coding-agent's theme and keybinding helpers read: the process-global theme (initTheme) and pi-tui's keybindings, set the way the
// interactive mode sets them. A case source's `call` (a `function`, so `this` is the generator's { load }) uses it to put the state in place, runs Pi's helper,
// and returns the state next to the result, so a consumer installs the same palette and keybindings before it calls its own port.
const THEME = Symbol.for("@earendil-works/pi-coding-agent:theme");

// useTheme initializes Pi's global theme `name` under a terminal of `colorMode` ("truecolor" or "256color") and returns the palette the host publishes for it
// (ExtensionThemePalette): each token's opening escape sequence as Pi's Theme resolved it.
export async function useTheme(env, name, colorMode) {
  const pi = await env.load("pi-coding-agent");
  const tui = await env.load("pi-tui");
  if (colorMode === "truecolor") process.env.COLORTERM = "truecolor";
  else delete process.env.COLORTERM;
  tui.resetCapabilitiesCache();
  pi.initTheme(name);
  return paletteOf(globalThis[THEME]);
}

export function paletteOf(theme) {
  const foregrounds = {};
  const backgrounds = {};
  for (const token of theme.fgAnsi.keys()) foregrounds[token] = theme.getFgAnsi(token);
  for (const token of theme.bgAnsi.keys()) backgrounds[token] = theme.getBgAnsi(token);
  return { name: theme.name, foregrounds, backgrounds, modifiers: true, mode: theme.getColorMode() };
}

// Texts that exercise every style function: empty, plain, wide, escape sequences (a close inside the text, an open without a close), line breaks, CR LF.
export const SAMPLES = ["", "x", "to do it", "a\nb", "漢字", "\x1b[1m", "  ", "line\x1b[22m with close", "\x1b[3mi\x1b[23m", "multi\nline\x1b[1m\nbold", "a\r\nb", "\n"];

export const THEMES = [["dark", "truecolor"], ["light", "256color"], ["dark", "256color"], ["light", "truecolor"]];

// Keybindings: pi-tui's own table plus app.* entries of the shapes keyText must format (several keys, none, a slash, an "alt" part).
export async function useKeybindings(env, userName) {
  const tui = await env.load("pi-tui");
  const defs = {
    ...tui.TUI_KEYBINDINGS,
    "app.interrupt": { defaultKeys: "escape", description: "Interrupt" },
    "app.test.multi": { defaultKeys: ["alt+x", "ctrl+shift+f5", "alt+/"], description: "Multi" },
    "app.test.none": { defaultKeys: [], description: "None" },
    "app.test.slash": { defaultKeys: ["ctrl+/", "super+k"], description: "Slash" },
    "app.test.alt": { defaultKeys: "alt+alt", description: "Alt" },
    "app.test.unicode": { defaultKeys: ["é+alt+ü", "ß", "alt+ß", "øption"], description: "Unicode" },
  };
  const user = userName === "overridden" ? { "app.interrupt": ["ctrl+c", "alt+q"], "tui.select.confirm": [] } : {};
  tui.setKeybindings(new tui.KeybindingsManager(defs, user));
  return { defs, user };
}

// withPlatform runs fn with process.platform set to `platform`, which formatKeyText reads.
export function withPlatform(platform, fn) {
  const descriptor = Object.getOwnPropertyDescriptor(process, "platform");
  Object.defineProperty(process, "platform", { value: platform });
  try {
    return fn();
  } finally {
    Object.defineProperty(process, "platform", descriptor);
  }
}
