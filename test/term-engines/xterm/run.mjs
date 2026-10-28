// SPDX-License-Identifier: MIT
// Replays the corpus in <corpus dir>/plan.txt through xterm.js headless and prints, as JSON lines, the engine and the screen after every step.
// See test/term-engines/README in docs/testing/term-engines.md for the plan and output formats.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import xterm from "@xterm/headless";

const { Terminal } = xterm;
const dir = process.argv[2];
const plan = readFileSync(join(dir, "plan.txt"), "utf8").split("\n").filter(Boolean).map((line) => line.split("\t"));
const version = JSON.parse(readFileSync(new URL("./node_modules/@xterm/headless/package.json", import.meta.url), "utf8")).version;
console.log(JSON.stringify({ engine: "xterm.js", version }));

const write = (term, data) => new Promise((resolve) => term.write(data, resolve));

function style(cell) {
  const parts = [];
  if (!cell.isFgDefault()) parts.push(cell.isFgRGB() ? `fg#${cell.getFgColor().toString(16)}` : `fg${cell.getFgColor()}`);
  if (!cell.isBgDefault()) parts.push(cell.isBgRGB() ? `bg#${cell.getBgColor().toString(16)}` : `bg${cell.getBgColor()}`);
  for (const [flag, name] of [[cell.isBold(), "bold"], [cell.isItalic(), "italic"], [cell.isDim(), "dim"], [cell.isUnderline(), "underline"],
    [cell.isBlink(), "blink"], [cell.isInverse(), "inverse"], [cell.isInvisible(), "invisible"], [cell.isStrikethrough(), "strike"], [cell.isOverline(), "overline"]]) {
    if (flag) parts.push(name);
  }
  return parts.join(" ");
}

function dump(term) {
  const buffer = term.buffer.active;
  const rows = [];
  const styles = [];
  let cell;
  for (let y = 0; y < term.rows; y++) {
    const line = buffer.getLine(buffer.baseY + y);
    let text = "";
    const runs = [];
    for (let x = 0; x < term.cols; x++) {
      cell = line.getCell(x, cell);
      if (cell.getWidth() === 0) continue;
      text += cell.getChars() || " ";
      const s = style(cell);
      const last = runs[runs.length - 1];
      if (last && last[2] === s && last[0] + last[1] === x) last[1] += cell.getWidth();
      else if (s !== "") runs.push([x, cell.getWidth(), s]);
    }
    rows.push(text);
    styles.push(runs);
  }
  // The scroll margins and origin mode are not public API; they are read from the parser's own state, and a build that moves them fails here.
  const { scrollTop, scrollBottom } = term._core._bufferService.buffer;
  const origin = term._core.coreService.decPrivateModes.origin;
  if (!Number.isInteger(scrollTop) || !Number.isInteger(scrollBottom) || typeof origin !== "boolean") throw new Error("xterm.js no longer exposes scrollTop, scrollBottom and decPrivateModes.origin");
  const margins = { top: scrollTop, bottom: scrollBottom, origin, full: scrollTop === 0 && scrollBottom === term.rows - 1 };
  return { cursor: [buffer.cursorX, buffer.cursorY], rows, styles, margins };
}

const scenarios = [];
for (const fields of plan) {
  if (fields[0] === "scenario") scenarios.push({ name: fields[1], cols: +fields[2], rows: +fields[3], variants: [] });
  else if (fields[0] === "variant") scenarios.at(-1).variants.push({ name: fields[1], file: fields[2], steps: [] });
  else scenarios.at(-1).variants.at(-1).steps.push({ end: +fields[1], resize: fields.length > 2 ? [+fields[2], +fields[3]] : null });
}
for (const scenario of scenarios) {
  for (const variant of scenario.variants) {
    const data = readFileSync(join(dir, variant.file));
    const term = new Terminal({ cols: scenario.cols, rows: scenario.rows, allowProposedApi: true });
    const checkpoints = [];
    let start = 0;
    for (const step of variant.steps) {
      if (step.resize) term.resize(step.resize[0], step.resize[1]);
      await write(term, data.subarray(start, step.end));
      start = step.end;
      checkpoints.push(dump(term));
    }
    term.dispose();
    console.log(JSON.stringify({ scenario: scenario.name, variant: variant.name, checkpoints }));
  }
}
