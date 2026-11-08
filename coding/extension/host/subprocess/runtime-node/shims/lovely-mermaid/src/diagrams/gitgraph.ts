/**
 * `gitGraph`: commit lanes, drawn the way `git log --graph` draws them —
 * newest commit on top, one column per branch, connector rows where history
 * splits. Lenient: an unreadable statement is dropped and recorded in
 * `warnings`.
 *
 * Connector rows go through the canvas direction bits, so a merge reaching
 * across an active lane crosses it with a `┼` instead of erasing it.
 */

import { Canvas, D, drawText, U } from '../canvas.ts'
import { MAX_EDGES } from '../graph.ts'
import { cleanLabel, fitLabel, MAX_LABEL } from '../labels.ts'
import type { Diagram } from '../registry.ts'
import { headerKind, statementsOf, words } from '../statements.ts'
import { stringWidth } from '../width.ts'

interface GitCommit {
  lane: number
  id: string
  tag: string | null
  /** Lane this commit merged in, if it is a merge commit. */
  mergeFrom: number | null
}

export const gitgraph: Diagram = {
  kind: 'gitgraph',
  headers: ['gitgraph', 'gitgraph:'],
  render(src) {
    const model = parseGitGraph(src)
    if (model === null) return null
    const { branches, commits, forkAt, warnings } = model
    const laneCount = branches.length

    // The newest commit of each lane wears the branch name.
    const headOf = new Map<number, number>()
    commits.forEach((c, i) => {
      headOf.set(c.lane, i)
    })

    /** Rows, top-down: commit rows interleaved with connector rows.
     * `open` hangs a merged lane off its merge commit, `close` returns a
     * forked lane to its parent at its fork point. Lanes never used (a
     * branch with no commits that nothing merged) simply never open. */
    type GitRow =
      | { kind: 'commit'; at: number }
      | { kind: 'open' | 'close'; parent: number; lane: number }
    const rows: GitRow[] = []
    const used = branches.map(
      (_, lane) => commits.some((c) => c.lane === lane || c.mergeFrom === lane) || lane === 0,
    )
    for (let i = commits.length - 1; i >= 0; i--) {
      const c = commits[i]
      rows.push({ kind: 'commit', at: i })
      if (c.mergeFrom !== null) {
        rows.push({ kind: 'open', parent: c.lane, lane: c.mergeFrom })
      }
      // Close outer lanes first so an inner close still sees them as columns.
      for (let lane = branches.length - 1; lane > 0; lane--) {
        if (forkAt[lane] === i - 1 && used[lane]) {
          rows.push({ kind: 'close', parent: commits[i - 1].lane, lane })
        }
      }
    }

    const laneX = (lane: number): number => lane * 2
    const graphW = laneCount * 2
    const labels = rows.map((row) => {
      if (row.kind !== 'commit') return null
      const c = commits[row.at]
      const parts: [string, 'text' | 'edgeLabel'][] = c.id === '' ? [] : [[c.id, 'text']]
      if (headOf.get(c.lane) === row.at) parts.push([`(${branches[c.lane]})`, 'edgeLabel'])
      if (c.tag !== null) parts.push([`[${c.tag}]`, 'edgeLabel'])
      if (c.mergeFrom !== null) parts.push([`⇐ ${branches[c.mergeFrom]}`, 'edgeLabel'])
      return parts
    })
    const width =
      graphW +
      Math.max(
        1,
        ...labels.map((parts) =>
          parts === null ? 0 : parts.reduce((w, [t]) => w + stringWidth(t) + 1, 0) - 1,
        ),
      )
    const canvas = new Canvas(width, rows.length)

    // Walk top-down with the set of lanes currently drawn as columns: a lane
    // joins at its newest own commit or its `open` connector, and leaves at
    // its `close`. Everything in the set draws `│` through every other row.
    const live = new Set<number>()
    rows.forEach((row, y) => {
      if (row.kind === 'commit') {
        const c = commits[row.at]
        live.add(c.lane)
        for (const lane of live) {
          if (lane !== c.lane) canvas.addBits(laneX(lane), y, U | D)
        }
        canvas.set(laneX(c.lane), y, '●', 'edge')
        let x = graphW
        for (const [text, role] of labels[y] ?? []) {
          drawText(canvas, text, x, y, role)
          x += stringWidth(text) + 1
        }
        return
      }
      // The parent keeps its column, the child lane hooks on toward it, and
      // unrelated live lanes cross the horizontal run as `┼` via the bit merge.
      const { parent, lane } = row
      const rejoins = row.kind === 'open' && live.has(lane)
      if (row.kind === 'open') live.add(lane)
      for (const l of live) {
        if (l !== parent && l !== lane) canvas.addBits(laneX(l), y, U | D)
      }
      // An open hangs off the merge commit directly above; a close bends down
      // into the fork commit below, continuing up only if the parent already
      // had a column here.
      if (row.kind === 'open') canvas.addBits(laneX(parent), y, U | D)
      else canvas.addBits(laneX(parent), y, D | (live.has(parent) ? U : 0))
      live.add(parent)
      canvas.segH(y, laneX(parent), laneX(lane))
      canvas.addBits(laneX(lane), y, (row.kind === 'open' ? D : U) | (rejoins ? U : 0))
      if (row.kind === 'close') live.delete(lane)
    })

    canvas.finalizeMask()
    return { canvas, warnings, classDefs: {} }
  },
}

function parseGitGraph(src: string): {
  branches: string[]
  commits: GitCommit[]
  forkAt: (number | null)[]
  warnings: string[]
} | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !gitgraph.headers.includes(kind)) return null

  const branches = ['main']
  const forkAt: (number | null)[] = [null]
  const commits: GitCommit[] = []
  const warnings: string[] = []
  /** Newest commit index per lane — the fork point for branches cut from it. */
  const heads: (number | null)[] = [null]
  let cur = 0
  let auto = 0
  let truncated = false

  for (const st of statements.slice(1)) {
    if (commits.length >= MAX_EDGES) {
      truncated = true
      break
    }
    const first = words(st)[0]?.toLowerCase() ?? ''
    const rest = st.slice(words(st)[0]?.length ?? 0).trim()
    if (first === 'commit') {
      const attrs = commitAttrs(rest)
      heads[cur] = commits.length
      commits.push({ lane: cur, id: attrs.id ?? `c${auto++}`, tag: attrs.tag, mergeFrom: null })
    } else if (first === 'branch') {
      const { name } = nameToken(rest)
      // The fork point is the current branch's head, not the newest commit.
      const fork = heads[cur] ?? forkAt[cur]
      if (name === undefined || branches.includes(name) || fork === null) {
        warnings.push(`dropped, unreadable statement: "${st}"`)
        continue
      }
      branches.push(name)
      forkAt.push(fork)
      heads.push(null)
      cur = branches.length - 1
    } else if (first === 'checkout' || first === 'switch') {
      const lane = branches.indexOf(nameToken(rest).name ?? '')
      if (lane === -1) {
        warnings.push(`dropped, unreadable statement: "${st}"`)
        continue
      }
      cur = lane
    } else if (first === 'merge') {
      const { name, after } = nameToken(rest)
      const lane = branches.indexOf(name ?? '')
      if (lane === -1 || lane === cur) {
        warnings.push(`dropped, unreadable statement: "${st}"`)
        continue
      }
      // An unnamed merge shows no id — the `⇐ branch` marker already says
      // what it is, and an invented id would collide with authored ones.
      const attrs = commitAttrs(after)
      heads[cur] = commits.length
      commits.push({ lane: cur, id: attrs.id ?? '', tag: attrs.tag, mergeFrom: lane })
    } else if (first === 'cherry-pick') {
      const attrs = commitAttrs(rest)
      heads[cur] = commits.length
      commits.push({
        lane: cur,
        id: attrs.id === null ? `c${auto++}` : `⟲ ${attrs.id}`,
        tag: attrs.tag,
        mergeFrom: null,
      })
    } else {
      warnings.push(`dropped, unreadable statement: "${st}"`)
    }
  }
  if (truncated) warnings.push(`diagram truncated: commit cap (${MAX_EDGES}) reached`)

  return commits.length === 0 ? null : { branches, commits, forkAt, warnings }
}

/** First branch-name token; quotes let a name carry spaces or keywords. */
function nameToken(rest: string): { name: string | undefined; after: string } {
  if (rest.startsWith('"')) {
    const close = rest.indexOf('"', 1)
    if (close !== -1) return { name: rest.slice(1, close), after: rest.slice(close + 1) }
  }
  const w = words(rest)[0]
  return { name: w, after: w === undefined ? rest : rest.slice(w.length) }
}

/** `id: "x" tag: "v1" …` key/value pairs trailing a commit or merge. */
function commitAttrs(rest: string): { id: string | null; tag: string | null } {
  const out = { id: null as string | null, tag: null as string | null }
  for (const m of rest.matchAll(/(id|tag)\s*:\s*"([^"]*)"/gi)) {
    out[m[1].toLowerCase() as 'id' | 'tag'] = fitLabel(cleanLabel(m[2]), MAX_LABEL)
  }
  return out
}
