/**
 * `timeline`: periods and their events as a vertical list — one row per
 * event, the period named on its first row. Lenient: an unreadable statement
 * is dropped and recorded in `warnings`.
 */

import { Canvas, drawText } from '../canvas.ts'
import { MAX_EDGES } from '../graph.ts'
import { decodeHtmlEntities, fitLabel, MAX_LABEL } from '../labels.ts'
import type { Diagram } from '../registry.ts'
import { headerKind, nonEmpty, statementsOf, words } from '../statements.ts'
import { stringWidth } from '../width.ts'

/** One output row: a period cell (blank on continuations) and an event. */
interface Row {
  period: string
  event: string
  /** Section headers occupy a row of their own. */
  section?: boolean
}

export const timeline: Diagram = {
  kind: 'timeline',
  headers: ['timeline'],
  render(src) {
    const parsed = parseTimeline(src)
    if (parsed === null) return null
    const { title, rows, warnings } = parsed

    const periodW = Math.max(...rows.map((r) => (r.section ? 0 : stringWidth(r.period))))
    const top = title === null ? 0 : 1
    const width = Math.max(
      title === null ? 0 : stringWidth(title) + 6,
      ...rows.map((r) => (r.section ? stringWidth(r.event) : periodW + 3 + stringWidth(r.event))),
    )
    const canvas = new Canvas(width, top + rows.length)

    if (title !== null) {
      const t = ` ${title} `
      const x = Math.max(0, Math.floor((width - stringWidth(t) - 4) / 2))
      drawText(canvas, '──', x, 0, 'edge')
      drawText(canvas, t, x + 2, 0, 'title')
      drawText(canvas, '──', x + 2 + stringWidth(t), 0, 'edge')
    }
    rows.forEach((row, i) => {
      const y = top + i
      if (row.section) {
        drawText(canvas, row.event, 0, y, 'title')
        return
      }
      drawText(canvas, row.period, 0, y, 'text')
      if (row.event === '') return
      drawText(canvas, '─', periodW + 1, y, 'edge')
      drawText(canvas, row.event, periodW + 3, y, 'edgeLabel')
    })
    return { canvas, warnings, classDefs: {} }
  },
}

function parseTimeline(
  src: string,
): { title: string | null; rows: Row[]; warnings: string[] } | null {
  const statements = statementsOf(src)
  if (headerKind(statements) !== 'timeline') return null

  let title: string | null = null
  const rows: Row[] = []
  const warnings: string[] = []
  let truncated = false
  let lastPeriod = false

  for (const st of statements.slice(1)) {
    if (rows.length >= MAX_EDGES) {
      truncated = true
      break
    }
    const first = words(st)[0]?.toLowerCase()
    if (first === 'title') {
      title = nonEmpty(st.slice(st.toLowerCase().indexOf('title') + 5).trim())
      continue
    }
    if (first === 'section') {
      const name = st.slice(st.toLowerCase().indexOf('section') + 7).trim()
      rows.push({ period: '', event: clean(name), section: true })
      lastPeriod = false
      continue
    }
    // `period : event : event`; a statement of only `: event`s continues the
    // previous period (the `;`-split form of mermaid's multi-line events).
    const parts = st.split(':').map((p) => clean(p))
    const period = parts.shift() ?? ''
    if (period === '' && parts.length > 0 && lastPeriod) {
      for (const event of parts) rows.push({ period: '', event })
      continue
    }
    // A bare period renders event-less, as mermaid draws it.
    if (period !== '' && parts.length === 0) {
      rows.push({ period, event: '' })
      lastPeriod = true
      continue
    }
    if (period === '' || parts.some((p) => p === '')) {
      warnings.push(`dropped, unreadable statement: "${st}"`)
      continue
    }
    parts.forEach((event, i) => {
      rows.push({ period: i === 0 ? period : '', event })
    })
    lastPeriod = true
  }
  if (truncated) warnings.push(`diagram truncated: event cap (${MAX_EDGES}) reached`)

  return rows.length === 0 ? null : { title, rows, warnings }
}

const clean = (s: string): string => fitLabel(decodeHtmlEntities(s.trim()), MAX_LABEL)
