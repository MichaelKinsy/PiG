import {
  formatKeyText,
  theme
} from "./chunk-6PEVBP2X.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/modes/interactive/components/pi-logo-animation.js
import { backgroundAnsi, colorToRgb, foregroundAnsi, getKeybindings, indexedColor, rgbColor, visibleWidth } from "../../../pi-tui.mjs";
var CORAL = [228, 138, 122];
var BLUE = [79, 142, 179];
var YELLOW = [234, 182, 93];
var DEPTH = 0.7;
var CAMERA_DISTANCE = 10;
var FLY_START = 0.1;
var FLY_DURATION = 1.3;
var LOGO_PIXELS = ["ccc.", "b.c.", "bb.y", "b..y"];
var BLOCKS = LOGO_PIXELS.flatMap((line, row) => [...line].flatMap((pixel, column) => {
  const color = pixel === "c" ? CORAL : pixel === "b" ? BLUE : pixel === "y" ? YELLOW : void 0;
  return color ? [{ home: [column, row, 0], color }] : [];
}));
var LOGO_RADIUS = Math.hypot(2, 2, 1 + DEPTH / 2);
var PUZZLE_START = FLY_START + FLY_DURATION + 3;
var PUZZLE_STEP = 0.3;
var PUZZLE_STEPS = 12;
var PUZZLE_RETURN = 1;
var PUZZLE_HOLD = 0.6;
var PUZZLE_CYCLE = PUZZLE_STEP * PUZZLE_STEPS + PUZZLE_RETURN + PUZZLE_HOLD;
var PUZZLE_MOVES = [
  [1, 0, 0],
  [-1, 0, 0],
  [0, 1, 0],
  [0, -1, 0],
  [1, 0, 0],
  [-1, 0, 0],
  [0, 1, 0],
  [0, -1, 0],
  [0, 0, 1],
  [0, 0, -1]
];
function shuffleSteps(cycle) {
  let positions = BLOCKS.map((block) => block.home);
  const steps = [positions];
  const lastMoved = /* @__PURE__ */ new Set();
  let random = cycle * 7919 + 1;
  const next = /* @__PURE__ */ __name(() => hash(random++), "next");
  const key = /* @__PURE__ */ __name(([x, y, z]) => (z + 1) * 16 + y * 4 + x, "key");
  for (let step = 0; step < PUZZLE_STEPS; step++) {
    const occupied = new Set(positions.map(key));
    const nextPositions = [...positions];
    const moved = /* @__PURE__ */ new Set();
    const moveCount = next() < 0.4 ? 2 : 1;
    for (let move = 0; move < moveCount; move++) {
      const candidates = [];
      for (let block = 0; block < positions.length; block++) {
        if (moved.has(block))
          continue;
        const [x, y, z] = positions[block];
        for (const [dx, dy, dz] of PUZZLE_MOVES) {
          const target = [x + dx, y + dy, z + dz];
          if (target[0] < 0 || target[0] > 3 || target[1] < 0 || target[1] > 3)
            continue;
          if (target[2] < -1 || target[2] > 1 || occupied.has(key(target)))
            continue;
          candidates.push({ block, target });
        }
      }
      const fresh = candidates.filter((candidate) => !lastMoved.has(candidate.block));
      const pool = fresh.length > 0 ? fresh : candidates;
      const choice = pool[Math.floor(next() * pool.length)];
      if (!choice)
        break;
      occupied.add(key(choice.target));
      nextPositions[choice.block] = choice.target;
      moved.add(choice.block);
    }
    lastMoved.clear();
    for (const block of moved)
      lastMoved.add(block);
    positions = nextPositions;
    steps.push(positions);
  }
  return steps;
}
__name(shuffleSteps, "shuffleSteps");
var FRAME_MS = 1e3 / 30;
var DUST_DURATION = 0.35;
var WAVE_SPREAD = 0.55;
var WAVE_JITTER = 0.12;
var DISSOLVE_END = FLY_START + WAVE_SPREAD + WAVE_JITTER + DUST_DURATION;
var BACKGROUND_FADE = 0.5;
var EXIT_DURATION = 1.1;
var STARS_START = 2.5;
var STARS_FADE = 2;
var STAR_DENSITY = 0.018;
var STAR_TINTS = [
  [255, 255, 255],
  [170, 195, 255],
  [255, 225, 170],
  [255, 190, 205]
];
var LOGO_COLOR_STEP = 5;
var LIGHT_HALO_STRENGTH = 0.3;
var HALO_RADIUS_X = 6;
var HALO_RADIUS_Y = 3;
var HALO_LEVELS = 32;
var STAR_ALPHA_LEVELS = 8;
var STAR_FLICKER_LEVELS = 3;
var HINT_FADE = 0.5;
var DOT_BITS = [1, 8, 2, 16, 4, 32, 64, 128];
var BRAILLE = Array.from({ length: 256 }, (_, bits) => String.fromCharCode(10240 + bits));
var LIGHT = normalize([-0.45, -0.6, 0.75]);
var HALF_VECTOR = normalize([LIGHT[0], LIGHT[1], LIGHT[2] + 1]);
function normalize(v) {
  const length = Math.hypot(v[0], v[1], v[2]);
  return [v[0] / length, v[1] / length, v[2] / length];
}
__name(normalize, "normalize");
function clamp01(value) {
  return value < 0 ? 0 : value > 1 ? 1 : value;
}
__name(clamp01, "clamp01");
function smooth(value) {
  const t = clamp01(value);
  return t * t * t * (t * (t * 6 - 15) + 10);
}
__name(smooth, "smooth");
function easeOut(value) {
  const t = 1 - clamp01(value);
  return 1 - t * t * t;
}
__name(easeOut, "easeOut");
function mix(a, b, amount) {
  return [a[0] + (b[0] - a[0]) * amount, a[1] + (b[1] - a[1]) * amount, a[2] + (b[2] - a[2]) * amount];
}
__name(mix, "mix");
function hash(value) {
  let x = Math.imul(value ^ 2654435769, 2246822507);
  x ^= x >>> 13;
  x = Math.imul(x, 3266489909);
  x ^= x >>> 16;
  return (x >>> 0) / 4294967296;
}
__name(hash, "hash");
function isLight([r, g, b]) {
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 128;
}
__name(isLight, "isLight");
function toRgb(color) {
  const { r, g, b } = colorToRgb(color);
  return [r, g, b];
}
__name(toRgb, "toRgb");
function glyphInk(text, seed) {
  if (text.trim() === "")
    return 0;
  if (/^[.,:;'`\-_·]$/.test(text))
    return 2;
  if (/^[\u2500-\u257f]$/.test(text))
    return 3;
  return 4 + Math.floor(seed * 3);
}
__name(glyphInk, "glyphInk");
var segmenter = new Intl.Segmenter();
function parseScreen(lines, width, foreground, background) {
  const palette = /* @__PURE__ */ __name((index) => toRgb(indexedColor(index)), "palette");
  return lines.map((line) => {
    const cells = [];
    let fg;
    let bg;
    let dim = false;
    let inverse = false;
    const applySgr = /* @__PURE__ */ __name((params) => {
      const codes = params === "" ? ["0"] : params.split(";");
      for (let i2 = 0; i2 < codes.length; i2++) {
        const parts = codes[i2].split(":").map((part) => Number.parseInt(part, 10));
        const code = parts[0] ?? 0;
        if (code === 38 || code === 48) {
          let args;
          if (parts.length > 1) {
            args = parts.slice(1).filter((part) => !Number.isNaN(part));
          } else {
            const kind = Number.parseInt(codes[i2 + 1] ?? "", 10);
            const count = kind === 5 ? 2 : kind === 2 ? 4 : 1;
            args = codes.slice(i2 + 1, i2 + 1 + count).map((part) => Number.parseInt(part, 10));
            i2 += count;
          }
          let color;
          if (args[0] === 5 && args[1] !== void 0)
            color = palette(args[1]);
          else if (args[0] === 2 && args.length >= 4)
            color = [args[1], args[2], args[3]];
          if (color) {
            if (code === 38)
              fg = color;
            else
              bg = color;
          }
        } else if (code === 0) {
          fg = void 0;
          bg = void 0;
          dim = false;
          inverse = false;
        } else if (code === 2)
          dim = true;
        else if (code === 22)
          dim = false;
        else if (code === 7)
          inverse = true;
        else if (code === 27)
          inverse = false;
        else if (code >= 30 && code <= 37)
          fg = palette(code - 30);
        else if (code >= 90 && code <= 97)
          fg = palette(code - 90 + 8);
        else if (code === 39)
          fg = void 0;
        else if (code >= 40 && code <= 47)
          bg = palette(code - 40);
        else if (code >= 100 && code <= 107)
          bg = palette(code - 100 + 8);
        else if (code === 49)
          bg = void 0;
      }
    }, "applySgr");
    const pushText = /* @__PURE__ */ __name((text) => {
      for (const { segment } of segmenter.segment(text)) {
        const glyphWidth = visibleWidth(segment);
        if (glyphWidth === 0 || cells.length + glyphWidth > width)
          continue;
        let cellFg = fg ?? foreground;
        let cellBg = bg;
        if (inverse) {
          cellFg = bg ?? background;
          cellBg = fg ?? foreground;
        }
        if (dim)
          cellFg = mix(cellFg, cellBg ?? background, 0.4);
        const cell = { text: segment, width: glyphWidth, fg: cellFg, bg: cellBg, delay: 0, ink: 0, seed: 0 };
        cells.push(cell);
        if (glyphWidth === 2)
          cells.push({ ...cell, text: "", width: 0 });
      }
    }, "pushText");
    let i = 0;
    while (i < line.length) {
      const sequenceStart = line.indexOf("\x1B", i);
      if (sequenceStart === -1) {
        pushText(line.slice(i));
        break;
      }
      if (sequenceStart > i)
        pushText(line.slice(i, sequenceStart));
      const kind = line[sequenceStart + 1];
      if (kind === "[") {
        let end = sequenceStart + 2;
        while (end < line.length && (line.charCodeAt(end) < 64 || line.charCodeAt(end) > 126))
          end++;
        if (line[end] === "m")
          applySgr(line.slice(sequenceStart + 2, end));
        i = end + 1;
      } else if (kind === "]" || kind === "_" || kind === "P" || kind === "^") {
        const bell = line.indexOf("\x07", sequenceStart + 2);
        const st = line.indexOf("\x1B\\", sequenceStart + 2);
        if (bell === -1 && st === -1)
          break;
        i = bell !== -1 && (st === -1 || bell < st) ? bell + 1 : st + 2;
      } else {
        i = sequenceStart + 2;
      }
    }
    return cells;
  });
}
__name(parseScreen, "parseScreen");
function rotation(yaw, pitch, roll) {
  const [sy, cy, sx, cx, sz, cz] = [
    Math.sin(yaw),
    Math.cos(yaw),
    Math.sin(pitch),
    Math.cos(pitch),
    Math.sin(roll),
    Math.cos(roll)
  ];
  const ry = [cy, 0, sy, 0, 1, 0, -sy, 0, cy];
  const rx = [1, 0, 0, 0, cx, -sx, 0, sx, cx];
  const rz = [cz, -sz, 0, sz, cz, 0, 0, 0, 1];
  return multiply(rz, multiply(rx, ry));
}
__name(rotation, "rotation");
function multiply(a, b) {
  const result = new Array(9);
  for (let row = 0; row < 3; row++) {
    for (let column = 0; column < 3; column++) {
      result[row * 3 + column] = a[row * 3] * b[column] + a[row * 3 + 1] * b[3 + column] + a[row * 3 + 2] * b[6 + column];
    }
  }
  return result;
}
__name(multiply, "multiply");
var SPIN_RAMP = 1.4;
var SPIN_SPEED = Math.PI * 2 / PUZZLE_CYCLE;
var SPIN_LINGER = 0.6;
var FIRST_FRONT_VIEW = PUZZLE_START + PUZZLE_STEP * PUZZLE_STEPS + PUZZLE_RETURN + PUZZLE_HOLD / 2;
function baseSpinPhase(time) {
  const elapsed = time - FLY_START;
  if (elapsed <= 0)
    return 0;
  const u = elapsed / SPIN_RAMP;
  if (u < 1)
    return SPIN_SPEED * SPIN_RAMP * (u ** 6 - 3 * u ** 5 + 2.5 * u ** 4);
  return SPIN_SPEED * (SPIN_RAMP * 0.5 + elapsed - SPIN_RAMP);
}
__name(baseSpinPhase, "baseSpinPhase");
var SPIN_ALIGNMENT = Math.PI * 2 - baseSpinPhase(FIRST_FRONT_VIEW) % (Math.PI * 2);
function spinPhase(time) {
  return baseSpinPhase(time) + SPIN_ALIGNMENT * smooth((time - FLY_START) / FLY_DURATION);
}
__name(spinPhase, "spinPhase");
function spinAngle(time) {
  const phase = spinPhase(time);
  return phase - SPIN_LINGER * Math.sin(phase);
}
__name(spinAngle, "spinAngle");
var LogoRaster = class {
  static {
    __name(this, "LogoRaster");
  }
  /** Braille dot bits per cell. */
  bits = new Uint8Array(0);
  /** Lit dots per cell. */
  counts = new Uint8Array(0);
  /** Summed RGB of the lit dots per cell. */
  rgb = new Float32Array(0);
  /** Halo tint strength per cell, from 0 to 1. Only set when a halo was requested. */
  haloAmount = new Float32Array(0);
  /** Halo color per cell. */
  haloRgb = new Float32Array(0);
  width = 0;
  height = 0;
  /** Ray parameter of the nearest hit per dot; smaller is nearer. */
  depth = new Float32Array(0);
  /** Face index + 1 of the nearest hit per dot, 0 for none. */
  faceIds = new Uint8Array(0);
  /** Cells written by the previous frame, cleared before the next one. */
  dirty;
  /** Cells with a halo from the previous frame, cleared before the next one. */
  haloDirty;
  render(width, height, pose, boxes, background, haloStrength) {
    const dotWidth = width * 2;
    const dotHeight = height * 4;
    if (width !== this.width || height !== this.height) {
      this.width = width;
      this.height = height;
      this.bits = new Uint8Array(width * height);
      this.counts = new Uint8Array(width * height);
      this.rgb = new Float32Array(width * height * 3);
      this.haloAmount = new Float32Array(width * height);
      this.haloRgb = new Float32Array(width * height * 3);
      this.haloDirty = void 0;
      this.depth = new Float32Array(dotWidth * dotHeight).fill(Infinity);
      this.faceIds = new Uint8Array(dotWidth * dotHeight);
      this.dirty = void 0;
    } else if (this.dirty) {
      const { minX: minX2, minY: minY2, maxX: maxX2, maxY: maxY2 } = this.dirty;
      for (let row = minY2; row <= maxY2; row++) {
        this.bits.fill(0, row * width + minX2, row * width + maxX2 + 1);
        this.counts.fill(0, row * width + minX2, row * width + maxX2 + 1);
        this.rgb.fill(0, (row * width + minX2) * 3, (row * width + maxX2 + 1) * 3);
      }
      this.dirty = void 0;
    }
    if (this.haloDirty) {
      const { minX: minX2, minY: minY2, maxX: maxX2, maxY: maxY2 } = this.haloDirty;
      for (let row = minY2; row <= maxY2; row++) {
        this.haloAmount.fill(0, row * width + minX2, row * width + maxX2 + 1);
        this.haloRgb.fill(0, (row * width + minX2) * 3, (row * width + maxX2 + 1) * 3);
      }
      this.haloDirty = void 0;
    }
    const m = rotation(pose.yaw, pose.pitch, pose.roll);
    const { centerX, centerY, scale } = pose;
    const origin = [m[6] * CAMERA_DISTANCE, m[7] * CAMERA_DISTANCE, m[8] * CAMERA_DISTANCE];
    const light = isLight(background);
    const faces = this.visibleFaces(m, origin, pose, boxes, dotWidth, dotHeight, light);
    if (faces.length === 0)
      return;
    let minX = dotWidth;
    let minY = dotHeight;
    let maxX = -1;
    let maxY = -1;
    for (const face of faces) {
      minX = Math.min(minX, face.minX);
      minY = Math.min(minY, face.minY);
      maxX = Math.max(maxX, face.maxX);
      maxY = Math.max(maxY, face.maxY);
    }
    if (maxX < minX || maxY < minY)
      return;
    const depth = this.depth;
    const faceIds = this.faceIds;
    for (let faceIndex = 0; faceIndex < faces.length; faceIndex++) {
      const face = faces[faceIndex];
      const a = face.axis;
      const u = (a + 1) % 3;
      const v = (a + 2) % 3;
      const originA = origin[a];
      const originU = origin[u];
      const originV = origin[v];
      const stepA = m[a] / scale;
      const stepU = m[u] / scale;
      const stepV = m[v] / scale;
      const sx = (face.minX + 0.5 - centerX) / scale;
      for (let dotY = face.minY; dotY <= face.maxY; dotY++) {
        const sy = (dotY + 0.5 - centerY) / scale;
        let directionA = m[a] * sx + m[3 + a] * sy - m[6 + a] * CAMERA_DISTANCE;
        let directionU = m[u] * sx + m[3 + u] * sy - m[6 + u] * CAMERA_DISTANCE;
        let directionV = m[v] * sx + m[3 + v] * sy - m[6 + v] * CAMERA_DISTANCE;
        let index = dotY * dotWidth + face.minX;
        for (let dotX = face.minX; dotX <= face.maxX; dotX++, index++) {
          const t = (face.plane - originA) / directionA;
          if (t > 0 && t < depth[index]) {
            const hitU = originU + t * directionU;
            const hitV = originV + t * directionV;
            if (hitU >= face.uMin && hitU <= face.uMax && hitV >= face.vMin && hitV <= face.vMax) {
              depth[index] = t;
              faceIds[index] = faceIndex + 1;
            }
          }
          directionA += stepA;
          directionU += stepU;
          directionV += stepV;
        }
      }
    }
    const bits = this.bits;
    const counts = this.counts;
    const rgb = this.rgb;
    let cellMinX = width;
    let cellMinY = height;
    let cellMaxX = -1;
    let cellMaxY = -1;
    for (let dotY = minY; dotY <= maxY; dotY++) {
      let index = dotY * dotWidth + minX;
      for (let dotX = minX; dotX <= maxX; dotX++, index++) {
        const id = faceIds[index];
        if (id === 0)
          continue;
        const face = faces[id - 1];
        const fog = clamp01(0.15 - CAMERA_DISTANCE * (1 - depth[index]) * 0.12) * (light ? 0.5 : 1);
        faceIds[index] = 0;
        depth[index] = Infinity;
        const cellX = dotX >> 1;
        const cellY = dotY >> 2;
        const cell = cellY * width + cellX;
        bits[cell] |= DOT_BITS[(dotY & 3) * 2 + (dotX & 1)];
        counts[cell]++;
        rgb[cell * 3] += face.red + (background[0] - face.red) * fog;
        rgb[cell * 3 + 1] += face.green + (background[1] - face.green) * fog;
        rgb[cell * 3 + 2] += face.blue + (background[2] - face.blue) * fog;
        if (cellX < cellMinX)
          cellMinX = cellX;
        if (cellX > cellMaxX)
          cellMaxX = cellX;
        if (cellY < cellMinY)
          cellMinY = cellY;
        if (cellY > cellMaxY)
          cellMaxY = cellY;
      }
    }
    if (cellMaxX >= 0)
      this.dirty = { minX: cellMinX, minY: cellMinY, maxX: cellMaxX, maxY: cellMaxY };
    if (haloStrength > 0)
      this.renderHalo(haloStrength);
  }
  /**
   * Blur the logo's coverage and color over neighboring cells with a separable tent filter, so the tint falls
   * off smoothly past the logo's edges.
   */
  renderHalo(strength) {
    const cells = this.dirty;
    if (!cells)
      return;
    const { width, height, counts, rgb } = this;
    const minX = Math.max(0, cells.minX - HALO_RADIUS_X);
    const maxX = Math.min(width - 1, cells.maxX + HALO_RADIUS_X);
    const minY = Math.max(0, cells.minY - HALO_RADIUS_Y);
    const maxY = Math.min(height - 1, cells.maxY + HALO_RADIUS_Y);
    const regionWidth = maxX - minX + 1;
    const tent = /* @__PURE__ */ __name((radius) => {
      const weights = Array.from({ length: radius * 2 + 1 }, (_, i) => radius + 1 - Math.abs(i - radius));
      const total = weights.reduce((sum, weight) => sum + weight, 0);
      return weights.map((weight) => weight / total);
    }, "tent");
    const weightsX = tent(HALO_RADIUS_X);
    const weightsY = tent(HALO_RADIUS_Y);
    const rows = cells.maxY - cells.minY + 1;
    const horizontal = new Float32Array(rows * regionWidth * 4);
    for (let y = cells.minY; y <= cells.maxY; y++) {
      for (let x = minX; x <= maxX; x++) {
        let coverage = 0;
        let red = 0;
        let green = 0;
        let blue = 0;
        for (let dx = -HALO_RADIUS_X; dx <= HALO_RADIUS_X; dx++) {
          const sourceX = x + dx;
          if (sourceX < cells.minX || sourceX > cells.maxX)
            continue;
          const source = y * width + sourceX;
          const count = counts[source];
          if (count === 0)
            continue;
          const weight = weightsX[dx + HALO_RADIUS_X] / 8;
          coverage += count * weight;
          red += rgb[source * 3] * weight;
          green += rgb[source * 3 + 1] * weight;
          blue += rgb[source * 3 + 2] * weight;
        }
        const target = ((y - cells.minY) * regionWidth + (x - minX)) * 4;
        horizontal[target] = coverage;
        horizontal[target + 1] = red;
        horizontal[target + 2] = green;
        horizontal[target + 3] = blue;
      }
    }
    for (let y = minY; y <= maxY; y++) {
      for (let x = minX; x <= maxX; x++) {
        let coverage = 0;
        let red = 0;
        let green = 0;
        let blue = 0;
        for (let dy = -HALO_RADIUS_Y; dy <= HALO_RADIUS_Y; dy++) {
          const sourceY = y + dy;
          if (sourceY < cells.minY || sourceY > cells.maxY)
            continue;
          const weight = weightsY[dy + HALO_RADIUS_Y];
          const source = ((sourceY - cells.minY) * regionWidth + (x - minX)) * 4;
          coverage += horizontal[source] * weight;
          red += horizontal[source + 1] * weight;
          green += horizontal[source + 2] * weight;
          blue += horizontal[source + 3] * weight;
        }
        const target = y * width + x;
        const count = counts[target];
        if (count > 0 && count / 8 > coverage) {
          coverage = count / 8;
          red = rgb[target * 3] / count * coverage;
          green = rgb[target * 3 + 1] / count * coverage;
          blue = rgb[target * 3 + 2] / count * coverage;
        }
        if (coverage <= 0)
          continue;
        const amount = Math.round(strength * Math.min(1, coverage) ** 0.7 * HALO_LEVELS) / HALO_LEVELS;
        if (amount <= 0)
          continue;
        this.haloAmount[target] = amount;
        this.haloRgb[target * 3] = Math.round(red / coverage / LOGO_COLOR_STEP) * LOGO_COLOR_STEP;
        this.haloRgb[target * 3 + 1] = Math.round(green / coverage / LOGO_COLOR_STEP) * LOGO_COLOR_STEP;
        this.haloRgb[target * 3 + 2] = Math.round(blue / coverage / LOGO_COLOR_STEP) * LOGO_COLOR_STEP;
      }
    }
    this.haloDirty = { minX, minY, maxX, maxY };
  }
  visibleFaces(m, origin, pose, boxes, dotWidth, dotHeight, lightBackground) {
    const faces = [];
    for (const box of boxes) {
      for (let a = 0; a < 3; a++) {
        const u = (a + 1) % 3;
        const v = (a + 2) % 3;
        for (const side of [-1, 1]) {
          const plane = side > 0 ? box.max[a] : box.min[a];
          if ((origin[a] - plane) * side <= 0)
            continue;
          const covered = boxes.some((other) => other !== box && (side > 0 ? other.min[a] : other.max[a]) === plane && other.min[u] <= box.min[u] && other.max[u] >= box.max[u] && other.min[v] <= box.min[v] && other.max[v] >= box.max[v]);
          if (covered)
            continue;
          let minX = Infinity;
          let minY = Infinity;
          let maxX = -Infinity;
          let maxY = -Infinity;
          const corner = [0, 0, 0];
          for (const cornerU of [box.min[u], box.max[u]]) {
            for (const cornerV of [box.min[v], box.max[v]]) {
              corner[a] = plane;
              corner[u] = cornerU;
              corner[v] = cornerV;
              const x = m[0] * corner[0] + m[1] * corner[1] + m[2] * corner[2];
              const y = m[3] * corner[0] + m[4] * corner[1] + m[5] * corner[2];
              const z = m[6] * corner[0] + m[7] * corner[1] + m[8] * corner[2];
              const perspective = pose.scale * CAMERA_DISTANCE / (CAMERA_DISTANCE - z);
              const screenX = pose.centerX + x * perspective;
              const screenY = pose.centerY + y * perspective;
              minX = Math.min(minX, screenX);
              minY = Math.min(minY, screenY);
              maxX = Math.max(maxX, screenX);
              maxY = Math.max(maxY, screenY);
            }
          }
          const face = {
            minX: Math.max(0, Math.floor(minX)),
            minY: Math.max(0, Math.floor(minY)),
            maxX: Math.min(dotWidth - 1, Math.ceil(maxX)),
            maxY: Math.min(dotHeight - 1, Math.ceil(maxY))
          };
          if (face.maxX < face.minX || face.maxY < face.minY)
            continue;
          const nx = m[a] * side;
          const ny = m[3 + a] * side;
          const nz = m[6 + a] * side;
          const diffuse = Math.max(0, nx * LIGHT[0] + ny * LIGHT[1] + nz * LIGHT[2]);
          const rim = Math.max(0, nx * 0.8 - nz * 0.3);
          const specular = lightBackground ? 0 : Math.max(0, nx * HALF_VECTOR[0] + ny * HALF_VECTOR[1] + nz * HALF_VECTOR[2]) ** 24 * 0.6 * 255;
          const light = lightBackground ? Math.min(1, 0.55 + diffuse * 0.45 + rim * 0.1) : 0.45 + diffuse * 0.78 + rim * 0.25;
          faces.push({
            axis: a,
            plane,
            uMin: box.min[u],
            uMax: box.max[u],
            vMin: box.min[v],
            vMax: box.max[v],
            ...face,
            red: Math.min(255, box.color[0] * light + specular),
            green: Math.min(255, box.color[1] * light + specular),
            blue: Math.min(255, box.color[2] * light + specular)
          });
        }
      }
    }
    return faces;
  }
};
var playing = false;
async function playPiLogoAnimation(tui, options) {
  if (playing)
    return;
  playing = true;
  const reported = await tui.queryTerminalColors({ timeoutMs: 100 });
  const dark = theme.appearance === "dark";
  const toRgbTuple = /* @__PURE__ */ __name((rgb, fallback) => rgb ? [rgb.r, rgb.g, rgb.b] : fallback, "toRgbTuple");
  const colors = {
    foreground: toRgbTuple(reported.foreground, toRgb(theme.colors.text)),
    background: toRgbTuple(reported.background, dark ? [0, 0, 0] : [255, 255, 255])
  };
  if (tui.hasOverlay()) {
    playing = false;
    return;
  }
  const animation = new PiLogoAnimation(tui, options, colors, () => {
    playing = false;
    overlay.hide();
  });
  const overlay = tui.showOverlay(animation, { anchor: "top-left", width: "100%", maxHeight: "100%" });
}
__name(playPiLogoAnimation, "playPiLogoAnimation");
var PiLogoAnimation = class {
  static {
    __name(this, "PiLogoAnimation");
  }
  tui;
  options;
  foreground;
  background;
  onDone;
  startTime = performance.now();
  lastRender = performance.now();
  timer;
  exit;
  shuffle;
  screenWidth = -1;
  screenHeight = -1;
  /** Star per cell index, or undefined. */
  stars = [];
  cells = [];
  ansiCache = /* @__PURE__ */ new Map();
  logo = new LogoRaster();
  constructor(tui, options, colors, onDone) {
    this.tui = tui;
    this.options = options;
    this.foreground = colors.foreground;
    this.background = colors.background;
    this.onDone = onDone;
    this.timer = setInterval(() => {
      if (this.exit && this.exitProgress() >= 1 || performance.now() - this.lastRender > 1e3)
        this.finish();
      else
        this.tui.requestRender();
    }, FRAME_MS);
    this.timer.unref?.();
  }
  /** Play the exit animation. A second call skips it. */
  close() {
    if (this.exit) {
      this.finish();
      return;
    }
    const time = this.elapsed();
    const yaw = spinAngle(time);
    const turn = Math.PI * 2;
    this.exit = {
      start: performance.now(),
      time,
      yaw,
      targetYaw: Math.ceil(yaw / turn) * turn,
      offsets: this.blockOffsets(time)
    };
  }
  handleInput(data) {
    const keybindings = getKeybindings();
    if (keybindings.matches(data, "tui.select.cancel") || keybindings.matches(data, "app.clear"))
      this.close();
  }
  handleMouse(event) {
    if (event.type === "click")
      this.close();
    return { handled: true, render: false };
  }
  invalidate() {
    this.screenWidth = -1;
    this.screenHeight = -1;
    this.ansiCache.clear();
  }
  render(width) {
    const height = Math.max(1, this.tui.terminal.rows);
    const foreground = this.foreground;
    const background = this.background;
    this.lastRender = performance.now();
    if (width !== this.screenWidth || height !== this.screenHeight) {
      this.screenWidth = width;
      this.screenHeight = height;
      this.cells = this.prepareCells(width, foreground);
      this.stars = this.prepareStars(width, height);
    }
    let dissolveTime;
    let pose;
    let hintAlpha;
    let starAlpha;
    let offsets;
    if (this.exit) {
      const progress = this.exitProgress();
      const landing = clamp01(progress / 0.8);
      const settle = 1 - smooth(landing);
      dissolveTime = Math.min(this.exit.time, DISSOLVE_END) * (1 - progress);
      pose = this.pose(width, height, this.flyProgress(this.exit.time) * settle, this.exit.time);
      pose.yaw = this.exit.yaw + (this.exit.targetYaw - this.exit.yaw) * easeOut(landing);
      pose.pitch *= settle;
      pose.roll *= settle;
      hintAlpha = this.hintAlpha(this.exit.time) * (1 - smooth(progress / 0.2));
      starAlpha = this.starAlpha(this.exit.time) * (1 - smooth(progress / 0.3));
      const gather = 1 - smooth(progress / 0.5);
      offsets = this.exit.offsets.map(([x, y, z]) => [x * gather, y * gather, z * gather]);
    } else {
      const time = this.elapsed();
      dissolveTime = time;
      pose = this.pose(width, height, this.flyProgress(time), time);
      hintAlpha = this.hintAlpha(time);
      starAlpha = this.starAlpha(time);
      offsets = this.blockOffsets(time);
    }
    const boxes = BLOCKS.map((block, index) => {
      const [dx, dy, dz] = offsets[index];
      const x = block.home[0] - 2 + dx;
      const y = block.home[1] - 2 + dy;
      return { min: [x, y, dz - DEPTH / 2], max: [x + 1, y + 1, dz + DEPTH / 2], color: block.color };
    });
    const logo = this.logo;
    logo.render(width, height, pose, boxes, background, isLight(background) ? LIGHT_HALO_STRENGTH : 0);
    const halo = /* @__PURE__ */ __name((index, base) => {
      const amount = logo.haloAmount[index];
      if (amount <= 0)
        return base;
      const color = logo.haloRgb;
      return mix(base ?? background, [color[index * 3], color[index * 3 + 1], color[index * 3 + 2]], amount);
    }, "halo");
    const backgroundFade = smooth(dissolveTime / BACKGROUND_FADE);
    const textFade = smooth(dissolveTime / 0.6) * 0.3;
    const textActive = dissolveTime < DISSOLVE_END || backgroundFade < 1;
    const hint = this.hint(width, height, hintAlpha, background);
    const frame = Math.floor(dissolveTime * 14);
    const now = this.elapsed();
    const starLevel = Math.round(starAlpha * STAR_ALPHA_LEVELS) / STAR_ALPHA_LEVELS;
    const starfield = /* @__PURE__ */ __name((index) => {
      if (starLevel <= 0)
        return void 0;
      const star = this.stars[index];
      if (!star)
        return void 0;
      const flicker = Math.round((0.5 + 0.5 * Math.sin(now * star.speed + star.phase)) * STAR_FLICKER_LEVELS) / STAR_FLICKER_LEVELS;
      return {
        text: star.glyph,
        fg: mix(background, mix(foreground, star.tint, 0.5), star.strength * (0.7 + 0.3 * flicker) * starLevel)
      };
    }, "starfield");
    const lines = [];
    for (let row = 0; row < height; row++) {
      const cells = this.cells[row] ?? [];
      let line = "";
      let currentFg = -1;
      let currentBg = -1;
      const emit = /* @__PURE__ */ __name((text, fg, bg) => {
        const fgKey = fg ? this.pack(fg) : -1;
        const bgKey = bg ? this.pack(bg) : -1;
        if (fgKey !== currentFg && text !== " ") {
          line += fg ? this.ansi(fgKey, false) : "\x1B[39m";
          currentFg = fgKey;
        }
        if (bgKey !== currentBg) {
          line += bg ? this.ansi(bgKey, true) : "\x1B[49m";
          currentBg = bgKey;
        }
        line += text;
      }, "emit");
      for (let column = 0; column < width; column++) {
        const index = row * width + column;
        const cell = textActive ? cells[column] : void 0;
        const cellBg = halo(index, cell?.bg && backgroundFade < 1 ? mix(cell.bg, background, backgroundFade) : void 0);
        if (hint && row === hint.row && column >= hint.start && column < hint.start + hint.text.length) {
          const offset = column - hint.start;
          emit(hint.text[offset], offset < hint.keyLength ? hint.keyColor : hint.color, cellBg);
          continue;
        }
        const dots = logo.bits[index];
        if (dots) {
          const step = LOGO_COLOR_STEP * logo.counts[index];
          const rgb = logo.rgb;
          const color = [
            Math.round(rgb[index * 3] / step) * LOGO_COLOR_STEP,
            Math.round(rgb[index * 3 + 1] / step) * LOGO_COLOR_STEP,
            Math.round(rgb[index * 3 + 2] / step) * LOGO_COLOR_STEP
          ];
          emit(BRAILLE[dots], color, cellBg);
          continue;
        }
        if (!cell) {
          const star = starfield(index);
          emit(star?.text ?? " ", star?.fg, cellBg);
          continue;
        }
        const dust = (dissolveTime - cell.delay) / DUST_DURATION;
        if (dust < 0 && cell.width === 2 && !logo.bits[index + 1]) {
          emit(cell.text, mix(cell.fg, background, textFade), cellBg);
          column++;
          continue;
        }
        if (dust < 0 && cell.width === 1) {
          emit(cell.text, mix(cell.fg, background, textFade), cellBg);
          continue;
        }
        const dotCount = dust < 0 ? cell.ink : Math.round(cell.ink * (1 - clamp01(dust)));
        if (dotCount <= 0) {
          const star = starfield(index);
          emit(star?.text ?? " ", star?.fg, cellBg);
          continue;
        }
        let bits = 0;
        let placed = 0;
        for (let attempt = 0; placed < dotCount && attempt < 32; attempt++) {
          const bit = DOT_BITS[Math.floor(hash(cell.seed * 7919 + frame * 131 + attempt) * 8)];
          if (bits & bit)
            continue;
          bits |= bit;
          placed++;
        }
        const fade = textFade + (1 - textFade) * clamp01(dust) ** 0.8;
        emit(BRAILLE[bits], mix(cell.fg, background, fade), cellBg);
      }
      lines.push(`${line}\x1B[0m`);
    }
    return lines;
  }
  elapsed() {
    return (performance.now() - this.startTime) / 1e3;
  }
  exitProgress() {
    return this.exit ? clamp01((performance.now() - this.exit.start) / 1e3 / EXIT_DURATION) : 0;
  }
  finish() {
    if (!this.timer)
      return;
    clearInterval(this.timer);
    this.timer = void 0;
    this.onDone();
  }
  /** Each block's displacement from its place in the logo at `time`, in grid units. */
  blockOffsets(time) {
    const home = /* @__PURE__ */ __name(() => BLOCKS.map(() => [0, 0, 0]), "home");
    if (time < PUZZLE_START)
      return home();
    const cycle = Math.floor((time - PUZZLE_START) / PUZZLE_CYCLE);
    const local = time - PUZZLE_START - cycle * PUZZLE_CYCLE;
    if (this.shuffle?.cycle !== cycle)
      this.shuffle = { cycle, steps: shuffleSteps(cycle) };
    const steps = this.shuffle.steps;
    const offset = /* @__PURE__ */ __name((position, index) => {
      const block = BLOCKS[index].home;
      return [position[0] - block[0], position[1] - block[1], position[2] - block[2]];
    }, "offset");
    const shuffleEnd = PUZZLE_STEP * PUZZLE_STEPS;
    if (local < shuffleEnd) {
      const step = Math.floor(local / PUZZLE_STEP);
      const progress = smooth((local - step * PUZZLE_STEP) / PUZZLE_STEP);
      return steps[step].map((from, index) => {
        const a = offset(from, index);
        const b = offset(steps[step + 1][index], index);
        return [a[0] + (b[0] - a[0]) * progress, a[1] + (b[1] - a[1]) * progress, a[2] + (b[2] - a[2]) * progress];
      });
    }
    if (local < shuffleEnd + PUZZLE_RETURN) {
      const u = (local - shuffleEnd) / PUZZLE_RETURN;
      const remaining = 1 - smooth(u);
      const arc = Math.sin(Math.PI * clamp01(u)) * 0.8;
      return steps[PUZZLE_STEPS].map((position, index) => {
        const [x, y, z] = offset(position, index);
        return [x * remaining, y * remaining, z * remaining + (index % 2 === 0 ? arc : -arc)];
      });
    }
    return home();
  }
  flyProgress(time) {
    return smooth((time - FLY_START) / FLY_DURATION);
  }
  starAlpha(time) {
    return smooth((time - STARS_START) / STARS_FADE);
  }
  hintAlpha(time) {
    return smooth((time - FLY_START - FLY_DURATION) / HINT_FADE);
  }
  pose(width, height, progress, time) {
    const startScale = 2 * (CAMERA_DISTANCE - DEPTH / 2) / CAMERA_DISTANCE;
    const reach = LOGO_RADIUS * (CAMERA_DISTANCE / (CAMERA_DISTANCE - LOGO_RADIUS));
    const endScale = Math.max(startScale, Math.min(width * 2 * 0.35, (height * 4 - 8) * 0.48) / reach);
    const startX = this.options.logoColumn * 2 + 4;
    const startY = this.options.logoRow * 4 + 4;
    const endX = width;
    const endY = height * 2 - 2;
    return {
      centerX: startX + (endX - startX) * progress,
      centerY: startY + (endY - startY) * progress,
      // Interpolate the zoom geometrically so it feels uniform.
      scale: startScale * (endScale / startScale) ** progress,
      yaw: spinAngle(time),
      // Tilts are zero at whole turns, so the camera looks straight at the front of the reassembled logo.
      pitch: 0.3 * Math.sin(spinPhase(time)) * progress,
      roll: 0.06 * Math.sin(2 * spinPhase(time)) * progress
    };
  }
  hint(width, height, alpha, background) {
    if (alpha <= 0)
      return void 0;
    const key = formatKeyText(getKeybindings().getKeys("tui.select.cancel")[0] ?? "escape");
    const text = `${key} to return`;
    if (text.length > width)
      return void 0;
    return {
      row: height - 2,
      start: Math.floor((width - text.length) / 2),
      text,
      keyLength: key.length,
      keyColor: mix(background, toRgb(theme.colors.muted), alpha),
      color: mix(background, toRgb(theme.colors.dim), alpha)
    };
  }
  /** A sparse, deterministic starfield: one braille dot in about STAR_DENSITY of all cells. */
  prepareStars(width, height) {
    const stars = new Array(width * height);
    for (let index = 0; index < width * height; index++) {
      if (hash(index * 3 + 20973) >= STAR_DENSITY)
        continue;
      const random = /* @__PURE__ */ __name((salt) => hash(index * 7 + salt * 40503), "random");
      stars[index] = {
        glyph: BRAILLE[DOT_BITS[Math.floor(random(1) * 8)]],
        tint: STAR_TINTS[Math.floor(random(2) * STAR_TINTS.length)],
        strength: 0.1 + random(3) * 0.12,
        phase: random(4) * Math.PI * 2,
        speed: 0.4 + random(5) * 0.8
      };
    }
    return stars;
  }
  prepareCells(width, foreground) {
    const cells = parseScreen(this.options.screen, width, foreground, this.background);
    const centerX = this.options.logoColumn + 2;
    const centerY = this.options.logoRow + 1;
    const height = Math.max(1, this.tui.terminal.rows);
    const farthest = Math.hypot(Math.max(centerX, width - centerX), Math.max(centerY, height - centerY) * 2);
    for (let row = 0; row < cells.length; row++) {
      const line = cells[row];
      for (let column = 0; column < line.length; column++) {
        const cell = line[column];
        const owner = cell.width === 0 ? line[column - 1] : cell;
        const seed = hash(row * 65537 + column);
        if (cell.width !== 0) {
          const distance = Math.hypot(column - centerX, (row - centerY) * 2) / farthest;
          cell.delay = FLY_START + distance * WAVE_SPREAD + seed * WAVE_JITTER;
        } else {
          cell.delay = owner.delay;
        }
        cell.seed = row * 65537 + column;
        cell.ink = glyphInk(owner.text, seed);
      }
    }
    for (let row = this.options.logoRow; row < this.options.logoRow + 2; row++) {
      const line = cells[row];
      if (!line)
        continue;
      for (let column = this.options.logoColumn; column < this.options.logoColumn + 4; column++) {
        const cell = line[column];
        if (cell) {
          cell.text = " ";
          cell.width = 1;
          cell.ink = 0;
          cell.bg = void 0;
        }
      }
    }
    return cells;
  }
  pack(color) {
    return Math.round(color[0]) << 16 | Math.round(color[1]) << 8 | Math.round(color[2]);
  }
  ansi(key, isBackground) {
    const cacheKey = key * 2 + (isBackground ? 1 : 0);
    let value = this.ansiCache.get(cacheKey);
    if (value === void 0) {
      const color = rgbColor(key >> 16 & 255, key >> 8 & 255, key & 255);
      const mode = theme.getColorMode();
      value = isBackground ? backgroundAnsi(color, mode) : foregroundAnsi(color, mode);
      this.ansiCache.set(cacheKey, value);
    }
    return value;
  }
};
export {
  PiLogoAnimation,
  playPiLogoAnimation
};
