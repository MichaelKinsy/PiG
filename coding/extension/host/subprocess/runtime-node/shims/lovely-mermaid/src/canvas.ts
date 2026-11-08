import type { Role, Span } from './types.ts'
import { measured } from './width.ts'

/**
 * Sentinel occupying the trailing column of a wide glyph. Never emitted: the
 * line builder skips it so a CJK character claims two cells of layout but
 * contributes one character of output.
 */
export const CONT = String.fromCharCode(0)

/** Connection direction bits, combined into a box-drawing glyph by `maskChar`. */
export const U = 1
export const D = 2
export const L = 4
export const R = 8

/** Line styles, tracked per cell so crossing edges keep their own stroke. */
export const STY_DOT = 1
export const STY_THICK = 2
export const STY_SOLID = 4

/**
 * A grid of cells. Edges accumulate as direction bits rather than glyphs so
 * that crossings and junctions resolve correctly whatever order they are drawn
 * in; `finalizeMask` turns the accumulated bits into characters at the end.
 *
 * `occupied` marks cells claimed by a box, which edge bits must not overwrite.
 */
export class Canvas {
  readonly w: number
  readonly h: number
  ch: string[]
  role: Role[]
  /** Space-joined author classes per cell, or undefined; see `Span.classes`. */
  tag: (string | undefined)[]
  /** Link target per cell, or undefined; see `Span.href`. */
  href: (string | undefined)[]
  mask: Uint8Array
  style: Uint8Array
  occupied: Uint8Array
  curStyle: number = STY_SOLID
  /** Author classes stamped on cells painted while set, like `curStyle`. */
  curTag: string | undefined
  /** Link target stamped on cells painted while set, like `curTag`. */
  curHref: string | undefined

  constructor(w: number, h: number) {
    const n = w * h
    this.w = w
    this.h = h
    this.ch = new Array(n).fill(' ')
    this.role = new Array(n).fill('none')
    this.tag = new Array(n).fill(undefined)
    this.href = new Array(n).fill(undefined)
    this.mask = new Uint8Array(n)
    this.style = new Uint8Array(n)
    this.occupied = new Uint8Array(n)
  }

  idx(x: number, y: number): number {
    return y * this.w + x
  }

  set(x: number, y: number, c: string, role: Role): void {
    if (x >= this.w || y >= this.h) return
    const i = this.idx(x, y)
    // A literal tab measures one cell here but jumps to the terminal's tab
    // stop there, desyncing every column after it (the source box expands
    // tabs for the same reason). Same width, safe glyph.
    this.ch[i] = c === '\t' ? ' ' : c
    this.role[i] = role
    if (this.curTag !== undefined) this.tag[i] = this.curTag
    if (this.curHref !== undefined) this.href[i] = this.curHref
  }

  /**
   * Accumulate direction bits on a free cell.
   *
   * `role` is the role to claim the cell for; `border` cells are never
   * reclassified, so a connector meeting a box keeps the box's styling.
   */
  addBits(x: number, y: number, bits: number, role: Role = 'edge'): void {
    if (x >= this.w || y >= this.h) return
    const i = this.idx(x, y)
    if (this.occupied[i]) return
    this.mask[i] |= bits
    this.style[i] |= this.curStyle
    if (this.role[i] !== 'border') this.role[i] = role
    if (this.curTag !== undefined) this.tag[i] = this.curTag
    if (this.curHref !== undefined) this.href[i] = this.curHref
  }

  /** Stamp a finished sub-canvas (a subgraph frame's contents) at an offset. */
  blit(sub: Canvas, ox: number, oy: number): void {
    for (let sy = 0; sy < sub.h; sy++) {
      for (let sx = 0; sx < sub.w; sx++) {
        const x = ox + sx
        const y = oy + sy
        if (x >= this.w || y >= this.h) continue
        const si = sub.idx(sx, sy)
        const di = this.idx(x, y)
        this.ch[di] = sub.ch[si]
        this.role[di] = sub.role[si]
        this.tag[di] = sub.tag[si]
        this.href[di] = sub.href[si]
        this.style[di] = sub.style[si]
        this.occupied[di] = 1
      }
    }
  }

  /** Add direction bits even to an occupied cell, so an edge can meet a border. */
  junction(x: number, y: number, bits: number): void {
    if (x >= this.w || y >= this.h) return
    const i = this.idx(x, y)
    this.mask[i] |= bits
    if (this.role[i] !== 'border') this.role[i] = 'edge'
  }

  segV(x: number, y0: number, y1: number): void {
    const a = Math.min(y0, y1)
    const b = Math.max(y0, y1)
    for (let y = a; y <= b; y++) {
      let bits = 0
      if (y > a) bits |= U
      if (y < b) bits |= D
      this.addBits(x, y, bits)
    }
  }

  segH(y: number, x0: number, x1: number): void {
    const a = Math.min(x0, x1)
    const b = Math.max(x0, x1)
    for (let x = a; x <= b; x++) {
      let bits = 0
      if (x > a) bits |= L
      if (x < b) bits |= R
      this.addBits(x, y, bits)
    }
  }

  /** Resolve accumulated direction bits into glyphs, honouring line style. */
  finalizeMask(): void {
    for (let i = 0; i < this.ch.length; i++) {
      if (this.mask[i] === 0) continue
      if (this.ch[i] === ' ') {
        const c = maskChar(this.mask[i])
        this.ch[i] =
          this.style[i] === STY_DOT ? dottedChar(c) : this.style[i] === STY_THICK ? thickChar(c) : c
      } else if (this.ch[i] === '═' || this.ch[i] === '║') {
        this.ch[i] = doubleTee(this.ch[i], this.mask[i])
      }
    }
  }

  /**
   * Mirror top-to-bottom for `BT`. Rows reorder but within-row text does not,
   * so labels stay readable; box-drawing glyphs flip to match.
   */
  flipVertical(): void {
    for (let y = 0; y < Math.floor(this.h / 2); y++) {
      const y2 = this.h - 1 - y
      for (let x = 0; x < this.w; x++) {
        const i = this.idx(x, y)
        const j = this.idx(x, y2)
        ;[this.ch[i], this.ch[j]] = [this.ch[j], this.ch[i]]
        ;[this.role[i], this.role[j]] = [this.role[j], this.role[i]]
        ;[this.tag[i], this.tag[j]] = [this.tag[j], this.tag[i]]
        ;[this.href[i], this.href[j]] = [this.href[j], this.href[i]]
      }
    }
    for (let i = 0; i < this.ch.length; i++) {
      if (!textRole(this.role[i])) this.ch[i] = flipGlyphV(this.ch[i])
    }
  }

  /**
   * Mirror left-to-right for `RL`. Mirroring reverses each row, so after
   * flipping glyphs each text/label run is reversed back to reading order.
   */
  flipHorizontal(): void {
    for (let y = 0; y < this.h; y++) {
      for (let x = 0; x < Math.floor(this.w / 2); x++) {
        const x2 = this.w - 1 - x
        const i = this.idx(x, y)
        const j = this.idx(x2, y)
        ;[this.ch[i], this.ch[j]] = [this.ch[j], this.ch[i]]
        ;[this.role[i], this.role[j]] = [this.role[j], this.role[i]]
        ;[this.tag[i], this.tag[j]] = [this.tag[j], this.tag[i]]
        ;[this.href[i], this.href[j]] = [this.href[j], this.href[i]]
      }
    }
    for (let i = 0; i < this.ch.length; i++) {
      if (!textRole(this.role[i])) this.ch[i] = flipGlyphH(this.ch[i])
    }
    for (let y = 0; y < this.h; y++) {
      let x = 0
      while (x < this.w) {
        const role = this.role[this.idx(x, y)]
        if (role === 'text' || role === 'edgeLabel') {
          const start = this.idx(x, y)
          while (x < this.w && this.role[this.idx(x, y)] === role) x++
          const end = this.idx(x, y)
          reverseSlice(this.ch, start, end)
        } else {
          x++
        }
      }
    }
  }

  /** Group each row into runs of one role and tag, dropping continuations. */
  toLines(): { plain: string[]; styled: Span[][]; width: number } {
    const plain: string[] = []
    const styled: Span[][] = []
    let width = 0
    for (let y = 0; y < this.h; y++) {
      // A trailing CONT counts as painted: it is the second cell of a wide
      // glyph, so the row really does reach that column.
      let last = 0
      for (let x = this.w - 1; x >= 0; x--) {
        if (this.ch[this.idx(x, y)] !== ' ') {
          last = x + 1
          break
        }
      }
      width = Math.max(width, last)
      const spans: Span[] = []
      const push = (text: string, role: Role, tag: string | undefined, href?: string): void => {
        if (text === '') return
        const span: Span = { text, role }
        if (tag !== undefined) span.classes = tag.split(' ')
        if (href !== undefined) span.href = href
        spans.push(span)
      }
      let plainRow = ''
      let run = ''
      let runRole: Role = 'none'
      let runTag: string | undefined
      let runHref: string | undefined
      for (let x = 0; x < last; x++) {
        const i = this.idx(x, y)
        const c = this.ch[i]
        if (c === CONT) continue
        plainRow += c
        if (
          (this.role[i] !== runRole || this.tag[i] !== runTag || this.href[i] !== runHref) &&
          run !== ''
        ) {
          push(run, runRole, runTag, runHref)
          run = ''
        }
        runRole = this.role[i]
        runTag = this.tag[i]
        runHref = this.href[i]
        run += c
      }
      push(run, runRole, runTag, runHref)
      styled.push(spans)
      // Only ASCII spaces, which is all a blank cell ever holds. Trimming `\s`
      // would eat a trailing NBSP that `styled` keeps, desyncing the two.
      plain.push(plainRow.replace(/ +$/, ''))
    }
    let first = 0
    while (first < plain.length && plain[first] === '') first++
    let end = plain.length
    while (end > first && plain[end - 1] === '') end--
    return { plain: plain.slice(first, end), styled: styled.slice(first, end), width }
  }
}

function reverseSlice(arr: string[], start: number, end: number): void {
  for (let i = start, j = end - 1; i < j; i++, j--) {
    ;[arr[i], arr[j]] = [arr[j], arr[i]]
  }
}

/**
 * Paint `text` at `x, y`, one grapheme cluster per cell.
 *
 * A wide cluster claims a second cell, marked with `CONT` so the line builder
 * emits one character for it rather than a stray space.
 */
export function drawText(canvas: Canvas, text: string, x: number, y: number, role: Role): void {
  let cur = x
  for (const [cluster, cw] of measured(text)) {
    if (cw === 0) continue
    canvas.set(cur, y, cluster, role)
    for (let k = 1; k < cw; k++) canvas.set(cur + k, y, CONT, role)
    cur += cw
  }
}

/**
 * Paint `text` at `x, y`, clearing any edge bits underneath first.
 *
 * Used where text sits on top of a drawn line (sequence messages, dividers,
 * compartment rows) and must win over it.
 */
export function drawTextOverEdges(
  canvas: Canvas,
  text: string,
  x: number,
  y: number,
  role: Role,
): void {
  let cur = x
  for (const [cluster, cw] of measured(text)) {
    if (cw === 0) continue
    for (let k = 0; k < cw; k++) {
      if (cur + k < canvas.w && y < canvas.h) canvas.mask[canvas.idx(cur + k, y)] = 0
      canvas.set(cur + k, y, k === 0 ? cluster : CONT, role)
    }
    cur += cw
  }
}

export function maskChar(mask: number): string {
  switch (mask) {
    case 0:
      return ' '
    case U:
    case D:
    case U | D:
      return '│'
    case L:
    case R:
    case L | R:
      return '─'
    case D | R:
      return '┌'
    case D | L:
      return '┐'
    case U | R:
      return '└'
    case U | L:
      return '┘'
    case U | D | R:
      return '├'
    case U | D | L:
      return '┤'
    case D | L | R:
      return '┬'
    case U | L | R:
      return '┴'
    default:
      return '┼'
  }
}

/** An edge teeing into a double-line border: the mixed single/double glyphs. */
function doubleTee(c: string, mask: number): string {
  if (c === '═') {
    if (mask & U && mask & D) return '╪'
    if (mask & D) return '╤'
    if (mask & U) return '╧'
  } else {
    if (mask & L && mask & R) return '╫'
    if (mask & R) return '╟'
    if (mask & L) return '╢'
  }
  return c
}

const DOTTED: Record<string, string> = { '─': '╌', '│': '╎' }

const THICK: Record<string, string> = {
  '─': '━',
  '│': '┃',
  '┌': '┏',
  '┐': '┓',
  '└': '┗',
  '┘': '┛',
  '├': '┣',
  '┤': '┫',
  '┬': '┳',
  '┴': '┻',
  '┼': '╋',
}

const FLIP_V: Record<string, string> = {
  '╔': '╚',
  '╚': '╔',
  '╗': '╝',
  '╝': '╗',
  '╤': '╧',
  '╧': '╤',
  '┌': '└',
  '└': '┌',
  '┐': '┘',
  '┘': '┐',
  '┏': '┗',
  '┗': '┏',
  '┓': '┛',
  '┛': '┓',
  '╭': '╰',
  '╰': '╭',
  '╮': '╯',
  '╯': '╮',
  '┬': '┴',
  '┴': '┬',
  '┳': '┻',
  '┻': '┳',
  '▼': '▲',
  '▲': '▼',
  '▽': '△',
  '△': '▽',
}

const FLIP_H: Record<string, string> = {
  '╔': '╗',
  '╗': '╔',
  '╚': '╝',
  '╝': '╚',
  '╟': '╢',
  '╢': '╟',
  '┌': '┐',
  '┐': '┌',
  '└': '┘',
  '┘': '└',
  '┏': '┓',
  '┓': '┏',
  '┗': '┛',
  '┛': '┗',
  '╭': '╮',
  '╮': '╭',
  '╰': '╯',
  '╯': '╰',
  '├': '┤',
  '┤': '├',
  '┣': '┫',
  '┫': '┣',
  '▶': '◄',
  '◄': '▶',
  '▷': '◁',
  '◁': '▷',
}

const dottedChar = (c: string): string => DOTTED[c] ?? c
const thickChar = (c: string): string => THICK[c] ?? c
const flipGlyphV = (c: string): string => FLIP_V[c] ?? c
const flipGlyphH = (c: string): string => FLIP_H[c] ?? c

/** User-authored cells: flips reorder them but must not remap their glyphs. */
const textRole = (r: Role): boolean => r === 'text' || r === 'edgeLabel' || r === 'title'
