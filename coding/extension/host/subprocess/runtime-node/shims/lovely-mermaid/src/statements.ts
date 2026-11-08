/**
 * The shared statement layer: source text to statements, plus the small
 * string-reading helpers every grammar leans on.
 */

import { asciiLower, srcLines } from './labels.ts'

function flushStatement(cur: string, out: string[]): string {
  const trimmed = cur.trim()
  if (trimmed !== '') out.push(trimmed)
  return ''
}

/**
 * Split one source line into statements on `;`, stopping at a `%%` comment.
 *
 * Quoted spans are opaque, so a label may contain `;` and `%%`.
 */
export function splitStatements(line: string, out: string[]): void {
  const chars = [...line]
  let cur = ''
  let inQuotes = false
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i]
    if (inQuotes) {
      if (c === '"') inQuotes = false
      cur += c
    } else if (c === '"') {
      inQuotes = true
      cur += c
    } else if (c === '%' && chars[i + 1] === '%') {
      break
    } else if (c === ';') {
      cur = flushStatement(cur, out)
    } else {
      cur += c
    }
  }
  flushStatement(cur, out)
}

/**
 * Index just past a leading YAML frontmatter block (`---` … `---`), or 0 when
 * there is none. While the block is still unterminated everything is
 * frontmatter, so a streamed diagram stays blank until it closes.
 */
export function frontmatterEnd(lines: string[]): number {
  let i = 0
  while (i < lines.length && lines[i].trim() === '') i++
  if (lines[i]?.trim() !== '---') return 0
  i++
  while (i < lines.length && lines[i].trim() !== '---') i++
  return i + 1
}

/**
 * All statements in a source block, in order. A leading YAML frontmatter
 * block, part of the mermaid grammar since v10, is skipped.
 */
export function statementsOf(src: string): string[] {
  const lines = srcLines(src)
  const out: string[] = []
  for (const line of lines.slice(frontmatterEnd(lines))) splitStatements(line, out)
  return out
}

/**
 * The `title:` of a leading frontmatter block, or null. The one frontmatter
 * key with terminal meaning — `config` and friends style mermaid's own
 * renderers and are deliberately ignored.
 */
export function frontmatterTitle(src: string): string | null {
  const lines = srcLines(src)
  const end = frontmatterEnd(lines)
  for (const line of lines.slice(0, end)) {
    const kv = splitOnce(line, ':')
    // Untrimmed on the left: an indented `title:` is nested under some other
    // key, not the diagram's.
    if (kv === null || kv[0].trimEnd() !== 'title') continue
    const t = kv[1].trim()
    const quoted =
      t.length > 1 &&
      ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'")))
    const title = (quoted ? t.slice(1, -1) : t).trim()
    return title === '' ? null : title
  }
  return null
}

/** Per-char flags: 1 where the char lies inside a double-quoted span (quotes included). */
export function quoteMask(chars: string[]): Uint8Array {
  const mask = new Uint8Array(chars.length)
  let inQuotes = false
  for (let i = 0; i < chars.length; i++) {
    if (chars[i] === '"') {
      mask[i] = 1
      inQuotes = !inQuotes
    } else if (inQuotes) {
      mask[i] = 1
    }
  }
  return mask
}

/**
 * Split on separator chars sitting outside double quotes and parentheses,
 * dropping empty segments — the one splitter every grammar shares, so quote
 * rules cannot drift between them.
 */
export function splitTop(s: string, isSep: (c: string) => boolean): string[] {
  const out: string[] = []
  let cur = ''
  let inQuotes = false
  let depth = 0
  for (const c of s) {
    if (c === '"') inQuotes = !inQuotes
    else if (!inQuotes && c === '(') depth++
    else if (!inQuotes && c === ')' && depth > 0) depth--
    if (!inQuotes && depth === 0 && isSep(c)) {
      if (cur !== '') out.push(cur)
      cur = ''
    } else {
      cur += c
    }
  }
  if (cur !== '') out.push(cur)
  return out
}

/**
 * Split `head : rest` at the first label colon, skipping `:::` tag runs so
 * `A:::hot : desc` keeps its tag with the id. `null` when there is no colon.
 */
export function splitColon(s: string): [string, string] | null {
  const chars = [...s]
  for (let i = 0; i < chars.length; i++) {
    if (chars[i] !== ':') continue
    let run = i
    while (run < chars.length && chars[run] === ':') run++
    if (run - i >= 3) {
      i = run - 1
      continue
    }
    return [chars.slice(0, i).join(''), chars.slice(i + 1).join('')]
  }
  return null
}

/** Strip trailing `:::name` tags from an id token: `A:::hot` → id `A`, classes `[hot]`. */
export function takeTags(token: string): { id: string; classes: string[] } {
  const parts = token.split(':::')
  if (parts.length === 1 || parts[0] === '') return { id: token, classes: [] }
  return { id: parts[0], classes: parts.slice(1).filter((c) => c !== '') }
}

/**
 * The body of a `class A,B name` statement → `[ids, names]`. The last
 * whitespace-separated token is the name list, everything before it the ids —
 * so a space after a comma (`class A, B warn`) still reads as two ids.
 */
export function parseClassAssign(rest: string): [string[], string[]] | null {
  const body = rest.trim()
  const ws = body.search(/\s\S*$/)
  if (ws === -1) return null
  const split = (s: string): string[] =>
    s
      .split(',')
      .map((t) => t.trim())
      .filter((t) => t !== '')
  return [split(body.slice(0, ws)), split(body.slice(ws))]
}

/**
 * `A "url" [tooltip]` / `A href "url" …` → `[id, url]`. The callback forms
 * (`call`/`callback`) return null — their quoted string is a tooltip.
 */
export function parseHref(rest: string): [string, string] | null {
  const [id, second] = words(rest)
  if (id === undefined || second === 'call' || second === 'callback') return null
  const url = rest.match(/"([^"]+)"/)
  return url === null ? null : [id, url[1]]
}

export const firstWord = (s: string): string => s.split(/\s+/).filter((w) => w !== '')[0] ?? ''
export const words = (s: string): string[] => s.split(/\s+/).filter((w) => w !== '')

/** Split on the first occurrence of `sep`, Rust's `split_once`. */
export function splitOnce(s: string, sep: string): [string, string] | null {
  const i = s.indexOf(sep)
  return i === -1 ? null : [s.slice(0, i), s.slice(i + sep.length)]
}

export const nonEmpty = (s: string): string | null => (s === '' ? null : s)

/** Diagram kind from the header statement, lowercased. */
export function headerKind(statements: string[]): string | null {
  const header = statements[0]
  if (header === undefined) return null
  const kind = firstWord(header)
  return kind === '' ? null : asciiLower(kind)
}

/**
 * Parse the body of a `classDef` statement: `name[,name2] k1:v1,k2:v2`.
 * Values are kept verbatim; malformed pairs are skipped.
 */
export function parseClassDef(
  rest: string,
): { names: string[]; props: Record<string, string> } | null {
  const body = rest.trim()
  const ws = body.search(/\s/)
  if (ws === -1) return null
  const names = body
    .slice(0, ws)
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '')
  const props: Record<string, string> = {}
  // splitTop keeps `rgb(255,0,0)` whole; a bare comma still separates pairs.
  for (const pair of splitTop(body.slice(ws).trim(), (c) => c === ',')) {
    const kv = splitOnce(pair, ':')
    if (kv === null) continue
    const k = kv[0].trim()
    const v = kv[1].trim()
    if (k !== '' && v !== '') props[k] = v
  }
  return names.length === 0 ? null : { names, props }
}
