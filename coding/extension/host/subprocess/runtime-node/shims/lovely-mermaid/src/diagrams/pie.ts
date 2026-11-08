/**
 * `pie`: proportions as a labelled bar list. A terminal has no circle worth
 * drawing; bars carry the same information in less space and align with how
 * every TUI shows usage. Lenient: an unreadable statement is dropped and
 * recorded in `warnings`.
 */

import { Canvas, drawText } from '../canvas.ts'
import { MAX_NODES } from '../graph.ts'
import { cleanLabel, fitLabel } from '../labels.ts'
import type { Diagram } from '../registry.ts'
import { headerKind, nonEmpty, quoteMask, statementsOf, words } from '../statements.ts'
import { stringWidth } from '../width.ts'

/** Columns of the full-scale bar; eighth blocks refine below one cell. */
const BAR_W = 20
const EIGHTHS = ['', '▏', '▎', '▍', '▌', '▋', '▊', '▉']

export const pie: Diagram = {
  kind: 'pie',
  headers: ['pie'],
  render(src) {
    const parsed = parsePie(src)
    if (parsed === null) return null
    const { title, slices, showData, warnings } = parsed

    const labelW = Math.max(...slices.map((s) => stringWidth(s.label)))
    const total = slices.reduce((sum, s) => sum + s.value, 0)
    const rows = slices.map((s) => {
      const share = total === 0 ? 0 : s.value / total
      const pct = `${Math.round(share * 100)}%`.padStart(4)
      const data = showData ? `  (${s.value})` : ''
      return { ...s, share, suffix: pct + data }
    })

    const barX = labelW + 2
    const suffixX = barX + BAR_W + 1
    const width = suffixX + Math.max(...rows.map((r) => stringWidth(r.suffix)))
    const top = title === null ? 0 : 1
    const canvas = new Canvas(width, top + rows.length)

    if (title !== null) {
      drawText(canvas, title, Math.max(0, Math.floor((width - stringWidth(title)) / 2)), 0, 'title')
    }
    rows.forEach((r, i) => {
      const y = top + i
      drawText(canvas, r.label, 0, y, 'text')
      const eighths = Math.round(r.share * BAR_W * 8)
      let bar = '█'.repeat(Math.floor(eighths / 8)) + EIGHTHS[eighths % 8]
      // A nonzero slice always shows at least a sliver.
      if (bar === '' && r.value > 0) bar = '▏'
      drawText(canvas, bar, barX, y, 'edge')
      // The unfilled remainder is a track, so every bar shows its full scale.
      drawText(canvas, '░'.repeat(BAR_W - stringWidth(bar)), barX + stringWidth(bar), y, 'border')
      drawText(canvas, r.suffix, suffixX, y, 'edgeLabel')
    })
    return { canvas, warnings, classDefs: {} }
  },
}

interface PieSlice {
  label: string
  value: number
}

function parsePie(src: string): {
  title: string | null
  slices: PieSlice[]
  showData: boolean
  warnings: string[]
} | null {
  const statements = statementsOf(src)
  if (headerKind(statements) !== 'pie') return null

  // The header line may carry `showData` and an inline `title ...`.
  const head = words(statements[0])
  const showData = head.some((w) => w.toLowerCase() === 'showdata')
  const inlineTitle = head.findIndex((w) => w.toLowerCase() === 'title')
  let title = inlineTitle === -1 ? null : nonEmpty(head.slice(inlineTitle + 1).join(' '))

  const slices: PieSlice[] = []
  const warnings: string[] = []
  let truncated = false
  for (const st of statements.slice(1)) {
    const first = words(st)[0]?.toLowerCase()
    if (first === 'title') {
      title = nonEmpty(st.slice(st.toLowerCase().indexOf('title') + 5).trim())
      continue
    }
    const slice = parseSlice(st)
    if (slice === null) {
      warnings.push(`dropped, unreadable statement: "${st}"`)
      continue
    }
    if (slices.length >= MAX_NODES) {
      truncated = true
      break
    }
    slices.push(slice)
  }
  if (truncated) warnings.push(`diagram truncated: slice cap (${MAX_NODES}) reached`)

  return slices.length === 0 ? null : { title, slices, showData, warnings }
}

/** `"Label" : 42.5` — the label may be unquoted as long as it has no colon. */
function parseSlice(st: string): PieSlice | null {
  const chars = [...st]
  const quoted = quoteMask(chars)
  const colon = chars.findIndex((c, i) => c === ':' && !quoted[i])
  if (colon === -1) return null
  const label = fitLabel(cleanLabel(chars.slice(0, colon).join('')), 24)
  const value = Number(
    chars
      .slice(colon + 1)
      .join('')
      .trim(),
  )
  if (label === '' || !Number.isFinite(value) || value < 0) return null
  return { label, value }
}
