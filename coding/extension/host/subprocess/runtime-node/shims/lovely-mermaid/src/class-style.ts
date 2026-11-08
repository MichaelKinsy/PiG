/**
 * Best-effort interpretation of `classDef` styles for a cell grid.
 *
 * A terminal cell can express a foreground, a background and boldness —
 * nothing else. `fill` is the node background, `stroke` its border,
 * `color` its text; every other property is silently ignored.
 */

import { NAMED_COLORS } from './css-colors.ts'

/** The terminal-expressible subset of a classDef; colors as `#rrggbb`. */
export interface ClassStyle {
  fill?: string
  stroke?: string
  color?: string
  bold?: boolean
}

/** `#rgb`, `#rrggbb`, `rgb(r,g,b)` or a CSS color name → `#rrggbb`; else null. */
function normalizeColor(v: string): string | null {
  const s = v.trim().toLowerCase()
  if (/^#[0-9a-f]{6}$/.test(s)) return s
  if (/^#[0-9a-f]{3}$/.test(s)) return `#${s[1]}${s[1]}${s[2]}${s[2]}${s[3]}${s[3]}`
  const rgb = s.match(/^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/)
  if (rgb !== null) {
    const hex = (n: string) => Math.min(255, Number(n)).toString(16).padStart(2, '0')
    return `#${hex(rgb[1])}${hex(rgb[2])}${hex(rgb[3])}`
  }
  return NAMED_COLORS[s] ?? null
}

/**
 * The merged style of a span's classes (later classes win), or null when
 * nothing terminal-expressible was declared.
 */
export function resolveClassStyle(
  classes: string[] | undefined,
  classDefs: Record<string, Record<string, string>>,
): ClassStyle | null {
  if (classes === undefined) return null
  const out: ClassStyle = {}
  for (const name of classes) {
    const props = classDefs[name]
    if (props === undefined) continue
    for (const [k, v] of Object.entries(props)) {
      if (k === 'fill' || k === 'stroke' || k === 'color') {
        const c = normalizeColor(v)
        if (c !== null) out[k] = c
      } else if (k === 'font-weight') {
        out.bold = v.trim() === 'bold' || v.trim() === 'bolder'
      }
    }
  }
  return Object.keys(out).length > 0 ? out : null
}

/**
 * Black or white, whichever reads on the given `#rrggbb` background — the
 * guard that keeps `fill:#eee` legible on a dark terminal theme.
 */
export function contrastOn(fill: string): '#000000' | '#ffffff' {
  const ch = (i: number) => Number.parseInt(fill.slice(i, i + 2), 16)
  const yiq = (ch(1) * 299 + ch(3) * 587 + ch(5) * 114) / 1000
  return yiq >= 128 ? '#000000' : '#ffffff'
}
