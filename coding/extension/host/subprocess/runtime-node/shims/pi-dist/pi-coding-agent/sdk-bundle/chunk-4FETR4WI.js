import {
  theme
} from "./chunk-6PEVBP2X.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/modes/interactive/components/armin.js
var ARMIN_WIDTH = 31;
var ARMIN_HEIGHT = 36;
var WIDTH = ARMIN_WIDTH;
var HEIGHT = ARMIN_HEIGHT;
var BITS = [
  255,
  255,
  255,
  127,
  255,
  240,
  255,
  127,
  255,
  237,
  255,
  127,
  255,
  219,
  255,
  127,
  255,
  183,
  255,
  127,
  255,
  119,
  254,
  127,
  63,
  248,
  254,
  127,
  223,
  255,
  254,
  127,
  223,
  63,
  252,
  127,
  159,
  195,
  251,
  127,
  111,
  252,
  244,
  127,
  247,
  15,
  247,
  127,
  247,
  255,
  247,
  127,
  247,
  255,
  227,
  127,
  247,
  7,
  232,
  127,
  239,
  248,
  103,
  112,
  15,
  255,
  187,
  111,
  241,
  0,
  208,
  91,
  253,
  63,
  236,
  83,
  193,
  255,
  239,
  87,
  159,
  253,
  238,
  95,
  159,
  252,
  174,
  95,
  31,
  120,
  172,
  95,
  63,
  0,
  80,
  108,
  127,
  0,
  220,
  119,
  255,
  192,
  63,
  120,
  255,
  1,
  248,
  127,
  255,
  3,
  156,
  120,
  255,
  7,
  140,
  124,
  255,
  15,
  206,
  120,
  255,
  255,
  207,
  127,
  255,
  255,
  207,
  120,
  255,
  255,
  223,
  120,
  255,
  255,
  223,
  125,
  255,
  255,
  63,
  126,
  255,
  255,
  255,
  127
];
var BYTES_PER_ROW = Math.ceil(WIDTH / 8);
var DISPLAY_HEIGHT = Math.ceil(HEIGHT / 2);
var EFFECTS = ["typewriter", "scanline", "rain", "fade", "crt", "glitch", "dissolve"];
function isArminPixel(x, y) {
  if (y >= HEIGHT)
    return false;
  const byteIndex = y * BYTES_PER_ROW + Math.floor(x / 8);
  const bitIndex = x % 8;
  return (BITS[byteIndex] >> bitIndex & 1) === 0;
}
__name(isArminPixel, "isArminPixel");
function getChar(x, row) {
  const upper = isArminPixel(x, row * 2);
  const lower = isArminPixel(x, row * 2 + 1);
  if (upper && lower)
    return "\u2588";
  if (upper)
    return "\u2580";
  if (lower)
    return "\u2584";
  return " ";
}
__name(getChar, "getChar");
function buildFinalGrid() {
  const grid = [];
  for (let row = 0; row < DISPLAY_HEIGHT; row++) {
    const line = [];
    for (let x = 0; x < WIDTH; x++) {
      line.push(getChar(x, row));
    }
    grid.push(line);
  }
  return grid;
}
__name(buildFinalGrid, "buildFinalGrid");
var ArminComponent = class {
  static {
    __name(this, "ArminComponent");
  }
  ui;
  interval = null;
  effect;
  finalGrid;
  currentGrid;
  effectState = {};
  cachedLines = [];
  cachedWidth = 0;
  gridVersion = 0;
  cachedVersion = -1;
  constructor(ui) {
    this.ui = ui;
    this.effect = EFFECTS[Math.floor(Math.random() * EFFECTS.length)];
    this.finalGrid = buildFinalGrid();
    this.currentGrid = this.createEmptyGrid();
    this.initEffect();
    this.startAnimation();
  }
  invalidate() {
    this.cachedWidth = 0;
  }
  render(width) {
    if (width === this.cachedWidth && this.cachedVersion === this.gridVersion) {
      return this.cachedLines;
    }
    const padding = 1;
    const availableWidth = width - padding;
    this.cachedLines = this.currentGrid.map((row) => {
      const clipped = row.slice(0, availableWidth).join("");
      const padRight = Math.max(0, width - padding - clipped.length);
      return ` ${theme.fg("accent", clipped)}${" ".repeat(padRight)}`;
    });
    const message = "ARMIN SAYS HI";
    const msgPadRight = Math.max(0, width - padding - message.length);
    this.cachedLines.push(` ${theme.fg("accent", message)}${" ".repeat(msgPadRight)}`);
    this.cachedWidth = width;
    this.cachedVersion = this.gridVersion;
    return this.cachedLines;
  }
  createEmptyGrid() {
    return Array.from({ length: DISPLAY_HEIGHT }, () => Array(WIDTH).fill(" "));
  }
  initEffect() {
    switch (this.effect) {
      case "typewriter":
        this.effectState = { pos: 0 };
        break;
      case "scanline":
        this.effectState = { row: 0 };
        break;
      case "rain":
        this.effectState = {
          drops: Array.from({ length: WIDTH }, () => ({
            y: -Math.floor(Math.random() * DISPLAY_HEIGHT * 2),
            settled: 0
          }))
        };
        break;
      case "fade": {
        const positions = [];
        for (let row = 0; row < DISPLAY_HEIGHT; row++) {
          for (let x = 0; x < WIDTH; x++) {
            positions.push([row, x]);
          }
        }
        for (let i = positions.length - 1; i > 0; i--) {
          const j = Math.floor(Math.random() * (i + 1));
          [positions[i], positions[j]] = [positions[j], positions[i]];
        }
        this.effectState = { positions, idx: 0 };
        break;
      }
      case "crt":
        this.effectState = { expansion: 0 };
        break;
      case "glitch":
        this.effectState = { phase: 0, glitchFrames: 8 };
        break;
      case "dissolve": {
        this.currentGrid = Array.from({ length: DISPLAY_HEIGHT }, () => Array.from({ length: WIDTH }, () => {
          const chars = [" ", "\u2591", "\u2592", "\u2593", "\u2588", "\u2580", "\u2584"];
          return chars[Math.floor(Math.random() * chars.length)];
        }));
        const dissolvePositions = [];
        for (let row = 0; row < DISPLAY_HEIGHT; row++) {
          for (let x = 0; x < WIDTH; x++) {
            dissolvePositions.push([row, x]);
          }
        }
        for (let i = dissolvePositions.length - 1; i > 0; i--) {
          const j = Math.floor(Math.random() * (i + 1));
          [dissolvePositions[i], dissolvePositions[j]] = [dissolvePositions[j], dissolvePositions[i]];
        }
        this.effectState = { positions: dissolvePositions, idx: 0 };
        break;
      }
    }
  }
  startAnimation() {
    const fps = this.effect === "glitch" ? 60 : 30;
    this.interval = setInterval(() => {
      const done = this.tickEffect();
      this.updateDisplay();
      this.ui.requestRender();
      if (done) {
        this.stopAnimation();
      }
    }, 1e3 / fps);
  }
  stopAnimation() {
    if (this.interval) {
      clearInterval(this.interval);
      this.interval = null;
    }
  }
  tickEffect() {
    switch (this.effect) {
      case "typewriter":
        return this.tickTypewriter();
      case "scanline":
        return this.tickScanline();
      case "rain":
        return this.tickRain();
      case "fade":
        return this.tickFade();
      case "crt":
        return this.tickCrt();
      case "glitch":
        return this.tickGlitch();
      case "dissolve":
        return this.tickDissolve();
      default:
        return true;
    }
  }
  tickTypewriter() {
    const state = this.effectState;
    const pixelsPerFrame = 3;
    for (let i = 0; i < pixelsPerFrame; i++) {
      const row = Math.floor(state.pos / WIDTH);
      const x = state.pos % WIDTH;
      if (row >= DISPLAY_HEIGHT)
        return true;
      this.currentGrid[row][x] = this.finalGrid[row][x];
      state.pos++;
    }
    return false;
  }
  tickScanline() {
    const state = this.effectState;
    if (state.row >= DISPLAY_HEIGHT)
      return true;
    for (let x = 0; x < WIDTH; x++) {
      this.currentGrid[state.row][x] = this.finalGrid[state.row][x];
    }
    state.row++;
    return false;
  }
  tickRain() {
    const state = this.effectState;
    let allSettled = true;
    this.currentGrid = this.createEmptyGrid();
    for (let x = 0; x < WIDTH; x++) {
      const drop = state.drops[x];
      for (let row = DISPLAY_HEIGHT - 1; row >= DISPLAY_HEIGHT - drop.settled; row--) {
        if (row >= 0) {
          this.currentGrid[row][x] = this.finalGrid[row][x];
        }
      }
      if (drop.settled >= DISPLAY_HEIGHT)
        continue;
      allSettled = false;
      let targetRow = -1;
      for (let row = DISPLAY_HEIGHT - 1 - drop.settled; row >= 0; row--) {
        if (this.finalGrid[row][x] !== " ") {
          targetRow = row;
          break;
        }
      }
      drop.y++;
      if (drop.y >= 0 && drop.y < DISPLAY_HEIGHT) {
        if (targetRow >= 0 && drop.y >= targetRow) {
          drop.settled = DISPLAY_HEIGHT - targetRow;
          drop.y = -Math.floor(Math.random() * 5) - 1;
        } else {
          this.currentGrid[drop.y][x] = "\u2593";
        }
      }
    }
    return allSettled;
  }
  tickFade() {
    const state = this.effectState;
    const pixelsPerFrame = 15;
    for (let i = 0; i < pixelsPerFrame; i++) {
      if (state.idx >= state.positions.length)
        return true;
      const [row, x] = state.positions[state.idx];
      this.currentGrid[row][x] = this.finalGrid[row][x];
      state.idx++;
    }
    return false;
  }
  tickCrt() {
    const state = this.effectState;
    const midRow = Math.floor(DISPLAY_HEIGHT / 2);
    this.currentGrid = this.createEmptyGrid();
    const top = midRow - state.expansion;
    const bottom = midRow + state.expansion;
    for (let row = Math.max(0, top); row <= Math.min(DISPLAY_HEIGHT - 1, bottom); row++) {
      for (let x = 0; x < WIDTH; x++) {
        this.currentGrid[row][x] = this.finalGrid[row][x];
      }
    }
    state.expansion++;
    return state.expansion > DISPLAY_HEIGHT;
  }
  tickGlitch() {
    const state = this.effectState;
    if (state.phase < state.glitchFrames) {
      this.currentGrid = this.finalGrid.map((row) => {
        const offset = Math.floor(Math.random() * 7) - 3;
        const glitchRow = [...row];
        if (Math.random() < 0.3) {
          const shifted = glitchRow.slice(offset).concat(glitchRow.slice(0, offset));
          return shifted.slice(0, WIDTH);
        }
        if (Math.random() < 0.2) {
          const swapRow = Math.floor(Math.random() * DISPLAY_HEIGHT);
          return [...this.finalGrid[swapRow]];
        }
        return glitchRow;
      });
      state.phase++;
      return false;
    }
    this.currentGrid = this.finalGrid.map((row) => [...row]);
    return true;
  }
  tickDissolve() {
    const state = this.effectState;
    const pixelsPerFrame = 20;
    for (let i = 0; i < pixelsPerFrame; i++) {
      if (state.idx >= state.positions.length)
        return true;
      const [row, x] = state.positions[state.idx];
      this.currentGrid[row][x] = this.finalGrid[row][x];
      state.idx++;
    }
    return false;
  }
  updateDisplay() {
    this.gridVersion++;
  }
  dispose() {
    this.stopAnimation();
  }
};

export {
  ARMIN_WIDTH,
  ARMIN_HEIGHT,
  isArminPixel,
  ArminComponent
};
