// @earendil-works/pi-tui for extensions. Everything that runs unchanged in an
// extension process is Pi's own code, copied verbatim from the pinned release
// into pi-dist/pi-tui (automation/gen/vendor-pi-dist.sh): key parsing, width
// utilities, keybindings, fuzzy matching, autocomplete and the components
// extensions construct, with the marked release Pi's Markdown renders
// through. Markdown reads the host terminal's capabilities, which the runtime
// seeds from each state snapshot. The modules share one instance each, so for
// example setKittyProtocolActive and setKeybindings affect every component.
export { Marked } from "./marked/lib/marked.esm.js";
export { CombinedAutocompleteProvider } from "./pi-dist/pi-tui/autocomplete.js";
export { Box } from "./pi-dist/pi-tui/components/box.js";
export { CancellableLoader } from "./pi-dist/pi-tui/components/cancellable-loader.js";
export { Editor } from "./pi-dist/pi-tui/components/editor.js";
export { HStack } from "./pi-dist/pi-tui/components/h-stack.js";
export { Input } from "./pi-dist/pi-tui/components/input.js";
export { Loader } from "./pi-dist/pi-tui/components/loader.js";
export { Markdown } from "./pi-dist/pi-tui/components/markdown.js";
export { MouseRegion } from "./pi-dist/pi-tui/components/mouse-region.js";
export { SelectList } from "./pi-dist/pi-tui/components/select-list.js";
export { SettingsList } from "./pi-dist/pi-tui/components/settings-list.js";
export { Spacer } from "./pi-dist/pi-tui/components/spacer.js";
export { allocateStackSizes, Stack, visibleStackEntries } from "./pi-dist/pi-tui/components/stack.js";
export { Text } from "./pi-dist/pi-tui/components/text.js";
export { TruncatedText } from "./pi-dist/pi-tui/components/truncated-text.js";
export { VStack } from "./pi-dist/pi-tui/components/v-stack.js";
export { fuzzyFilter, fuzzyMatch } from "./pi-dist/pi-tui/fuzzy.js";
export { getKeybindings, KeybindingsManager, setKeybindings, TUI_KEYBINDINGS } from "./pi-dist/pi-tui/keybindings.js";
export {
  decodeKittyPrintable,
  decodePrintableKey,
  isKeyRelease,
  isKeyRepeat,
  isKittyProtocolActive,
  Key,
  matchesKey,
  parseKey,
  setKittyProtocolActive,
} from "./pi-dist/pi-tui/keys.js";
export { renderLatex } from "./pi-dist/pi-tui/latex.js";
export { StdinBuffer } from "./pi-dist/pi-tui/stdin-buffer.js";
export { parseOsc11BackgroundColor, parseTerminalColorSchemeReport } from "./pi-dist/pi-tui/terminal-colors.js";
export {
  allocateImageId,
  calculateImageRows,
  deleteAllKittyImages,
  deleteKittyImage,
  encodeITerm2,
  encodeKitty,
  getGifDimensions,
  getImageDimensions,
  getJpegDimensions,
  getPngDimensions,
  getWebpDimensions,
  hyperlink,
  imageFallback,
} from "./pi-dist/pi-tui/terminal-image.js";
export { Container, CURSOR_MARKER, compositeTuiLine, isFocusable, isViewportTUI } from "./pi-dist/pi-tui/tui.js";
export {
  getOsc8LinkAtColumn,
  sliceByColumn,
  stripTerminalSequences,
  truncateToWidth,
  visibleWidth,
  wrapTextWithAnsi,
} from "./pi-dist/pi-tui/utils.js";

// Upstream declares these as TypeScript types only. Extensions compiled
// against older SDK typings may still import them as values.
export class Component {
  invalidate() {}
  render() { return []; }
}

export class Focusable extends Component {
  constructor() {
    super();
    this.focused = false;
  }
  handleInput() {}
}

export class SelectItem { constructor(label, value = label) { this.label = label; this.value = value; } }
export class TUI {}
export class OverlayHandle {}
export class EditorTheme {}

// pig divergence (D73): the terminal, its capability probing and cell
// geometry, images, screens and scroll views, the clipboard and the renderer
// itself belong to PiG's Go host. They are importable here and throw a descriptive error only
// when called or constructed.
function hostOnlyTui(name) {
  return new Error(`${name} is not available to extensions running in PiG: the terminal is owned by PiG's host (see DIVERGENCES.md D73)`);
}
function hostOnlyTuiFunction(name) {
  const fn = function () { throw hostOnlyTui(name); };
  Object.defineProperty(fn, "name", { value: name });
  return fn;
}
function hostOnlyTuiClass(name) {
  return { [name]: class { constructor() { throw hostOnlyTui(name); } } }[name];
}
export const Image = hostOnlyTuiClass("Image");
export const ProcessTerminal = hostOnlyTuiClass("ProcessTerminal");
export const ScrollView = hostOnlyTuiClass("ScrollView");
export const TuiAltScreen = hostOnlyTuiClass("TuiAltScreen");
export const TuiMainScreen = hostOnlyTuiClass("TuiMainScreen");
export const detectCapabilities = hostOnlyTuiFunction("detectCapabilities");
export const getCapabilities = hostOnlyTuiFunction("getCapabilities");
export const getCellDimensions = hostOnlyTuiFunction("getCellDimensions");
export const getNativeClipboard = hostOnlyTuiFunction("getNativeClipboard");
export const renderImage = hostOnlyTuiFunction("renderImage");
export const resetCapabilitiesCache = hostOnlyTuiFunction("resetCapabilitiesCache");
export const setCapabilities = hostOnlyTuiFunction("setCapabilities");
export const setCapabilityOverrides = hostOnlyTuiFunction("setCapabilityOverrides");
export const setCellDimensions = hostOnlyTuiFunction("setCellDimensions");
