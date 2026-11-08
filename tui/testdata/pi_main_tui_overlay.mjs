// The Pi main commits PiG follows ahead of the pin, applied to a copy of the pinned pi-tui dist, for the oracle drivers that compare TUI frames:
//   D300  Pi main 25490ea49  cursor markers never reach the terminal (tui.js extractCursorPosition and applyLineResets, utils.js sliceWithWidth)
//   D301  Pi main 4622edb79  Kitty images are drawn after the rows they cover on every terminal (tui-alt-screen.js, no WezTerm test)
// Each replacement must find its anchor exactly once, so the overlay fails when the pin reaches a commit; remove it with its D record.
// The copy sits in a temporary directory whose node_modules links every package the pinned install resolves, so its imports resolve unchanged.
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";

const PATCHES = {
  "utils.js": [
    [
      "            else if (currentCol < startCol)\n                pendingAnsi += ansi.code;",
      '            else if (currentCol < startCol && !ansi.code.startsWith("\\x1b_"))\n                pendingAnsi += ansi.code;',
    ],
  ],
  "tui.js": [
    ["lines[i] = normalizeTerminalOutput(line) + reset;", 'lines[i] = normalizeTerminalOutput(line.replaceAll(CURSOR_MARKER, "")) + reset;'],
    ["lines[row] = line.slice(0, markerIndex) + line.slice(markerIndex + CURSOR_MARKER.length);", 'lines[row] = line.replaceAll(CURSOR_MARKER, "");'],
  ],
  "tui-alt-screen.js": [
    ['        const isWezTerm = Boolean(process.env.WEZTERM_PANE) || process.env.TERM_PROGRAM?.toLowerCase() === "wezterm";\n', ""],
    [
      "        const imageCellsNeedRedraw = !imageAnchorsNeedRedraw &&\n            isWezTerm &&\n",
      "        const imageCellsNeedRedraw = !imageAnchorsNeedRedraw &&\n",
    ],
    [" && screen.some(isImageLine) && isWezTerm;", " && screen.some(isImageLine);"],
  ],
};

// A copy with fresh permissions: the pinned install may be read-only, which a recursive copy keeps and rmSync then cannot remove.
function copyTree(from, to) {
  mkdirSync(to);
  for (const name of readdirSync(from)) {
    if (statSync(join(from, name)).isDirectory()) copyTree(join(from, name), join(to, name));
    else writeFileSync(join(to, name), readFileSync(join(from, name)));
  }
}

/** The directory of a patched copy of the pi-tui dist at `distDir`; PIG_PI_TUI=pinned returns `distDir` itself. */
export function piTuiDist(distDir) {
  if (process.env.PIG_PI_TUI === "pinned") return distDir;
  const real = realpathSync(distDir);
  const dir = mkdtempSync(join(tmpdir(), "pi-tui-main-"));
  process.on("exit", () => rmSync(dir, { recursive: true, force: true }));
  const copy = join(dir, "dist");
  copyTree(real, copy);
  for (const [file, patches] of Object.entries(PATCHES)) {
    let source = readFileSync(join(copy, file), "utf8");
    for (const [from, to] of patches) {
      if (source.split(from).length !== 2) throw new Error(`pi-tui main overlay: anchor not found exactly once in ${file}: ${from}`);
      source = source.replace(from, () => to);
    }
    writeFileSync(join(copy, file), source);
  }
  // Resolve bare imports as the pinned dist does: its own node_modules, then every enclosing one.
  const modules = join(dir, "node_modules");
  mkdirSync(modules);
  const roots = [];
  for (let at = dirname(dirname(real)); ; at = dirname(at)) {
    const candidate = join(at, "node_modules");
    if (existsSync(candidate)) roots.unshift(candidate);
    if (dirname(at) === at) break;
  }
  roots.push(join(dirname(real), "node_modules"));
  for (const root of roots) {
    if (!existsSync(root)) continue;
    for (const name of readdirSync(root)) {
      if (name.startsWith(".")) continue;
      const link = join(modules, name);
      rmSync(link, { force: true });
      symlinkSync(join(root, name), link);
    }
  }
  return copy;
}

/** `piTuiDist` of the dist that holds `indexPath` (its index.js), as the path of its index.js. */
export function piTuiIndex(indexPath) {
  return pathToFileURL(join(piTuiDist(dirname(indexPath)), "index.js")).href;
}
