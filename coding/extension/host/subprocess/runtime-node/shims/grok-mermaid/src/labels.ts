import { measured, stringWidth } from './width.ts'

/** Node labels wrap to at most this many display columns per line ... */
export const WRAP_WIDTH = 24
/** ... and at most this many lines; overflow is truncated with an ellipsis. */
export const MAX_LINES = 4
/** Edge labels are truncated to this many columns. */
export const MAX_LABEL = 28

/**
 * Identifier-boundary characters preferred as break points when a single word
 * is too wide to fit, so it is not sliced mid-segment.
 *
 * Mirrors `TOKEN_BREAK_CHARS` in grok-build's
 * `third_party/mermaid-to-svg/src/text_wrap.rs`; the two renderers are
 * deliberately independent, so keep these in sync.
 */
export const LABEL_BREAK_CHARS = ['_', '-', '.', '/']

/**
 * ASCII-only case folding, matching Rust's `to_ascii_lowercase`.
 *
 * `String.prototype.toLowerCase` can change a string's length (`İ` becomes two
 * code points), which would desync the byte offsets some parsers slice with.
 */
export const asciiLower = (s: string): string => s.replace(/[A-Z]/g, (c) => c.toLowerCase())
export const asciiUpper = (s: string): string => s.replace(/[a-z]/g, (c) => c.toUpperCase())

/**
 * C0 and C1 controls, less the `\t\n\r` the parsers and `srcLines` read.
 *
 * They measure one column and paint none, so a box sized around one is drawn a
 * column short of its own border; NUL also collides with the `CONT` sentinel
 * and is dropped after layout has already paid for its cell; ESC would inject
 * ANSI into the caller's scrollback. `decodeEntityBody` refuses to decode an
 * entity into one — this closes the same hole for literals.
 */
// biome-ignore lint/suspicious/noControlCharactersInRegex: the point is to match them
const CONTROLS = /[\0-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/g

/** Applied by every public entry point that takes untrusted source. */
export const stripControls = (src: string): string => src.replace(CONTROLS, '')

/**
 * Split source into lines the way Rust's `str::lines()` does: on `\n`, with a
 * trailing `\r` stripped, and *without* a final empty line when the input ends
 * in a newline. `String.split` yields that extra element, which would show up
 * as a spurious blank row inside a source box.
 */
export function srcLines(src: string): string[] {
  const out = src.split('\n').map((l) => (l.endsWith('\r') ? l.slice(0, -1) : l))
  if (out.length > 0 && out[out.length - 1] === '') out.pop()
  return out
}

const ALNUM = /[\p{Alphabetic}\p{N}]/u

/** Matches Rust's `char::is_alphanumeric`. */
export const isAlphanumeric = (c: string): boolean => ALNUM.test(c)

/** Characters allowed in a bare node/state/class identifier. */
export const isIdChar = (c: string): boolean => isAlphanumeric(c) || c === '_'

const ENTITY_LOOKAHEAD = 10

const NAMED_ENTITIES: Record<string, string> = {
  lt: '<',
  gt: '>',
  amp: '&',
  quot: '"',
  apos: "'",
}

function decodeEntityBody(body: string): string | null {
  const named = NAMED_ENTITIES[body]
  if (named !== undefined) return named
  if (!body.startsWith('#')) return null
  const num = body.slice(1)
  const hex = /^[xX]/.test(num)
  const digits = hex ? num.slice(1) : num
  if (!(hex ? /^[0-9a-fA-F]+$/ : /^[0-9]+$/).test(digits)) return null
  const code = Number.parseInt(digits, hex ? 16 : 10)
  // Surrogates and out-of-range values are not characters at all.
  if (code > 0x10ffff || (code >= 0xd800 && code <= 0xdfff)) return null
  // Reject control chars: NUL collides with the CONT sentinel and ESC would
  // inject ANSI into scrollback.
  if (code < 0x20 || (code >= 0x7f && code <= 0x9f)) return null
  return String.fromCodePoint(code)
}

/**
 * Decode HTML entities in label text. Called once per label: via `cleanLabel`
 * for bracketed labels, or explicitly at each direct-push sink.
 */
export function decodeHtmlEntities(s: string): string {
  if (!s.includes('&')) return s
  const chars = [...s]
  let out = ''
  let i = 0
  while (i < chars.length) {
    if (chars[i] !== '&') {
      out += chars[i]
      i++
      continue
    }
    // Scan a bounded window including the terminating `;`, so a stray `&` or an
    // over-long run stays literal.
    const hi = Math.min(i + 1 + ENTITY_LOOKAHEAD, chars.length)
    let semi = -1
    for (let j = i + 1; j < hi; j++) {
      if (chars[j] === ';') {
        semi = j
        break
      }
    }
    const decoded = semi === -1 ? null : decodeEntityBody(chars.slice(i + 1, semi).join(''))
    if (decoded === null) {
      out += '&'
      i++
    } else {
      // Resume past the `;`. The single pass never re-scans emitted text, so
      // `&amp;lt;` decodes to the literal `&lt;` rather than to `<`.
      out += decoded
      i = semi + 1
    }
  }
  return out
}

/** Strip markdown emphasis from a `` `backtick` `` label string. */
export function stripMarkdown(s: string): string {
  const noCode = [...s].filter((c) => c !== '`').join('')
  const noStrong = noCode.replaceAll('**', '').replaceAll('__', '')
  const chars = [...noStrong]
  let out = ''
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i]
    // Keep `*`/`_` only when they sit inside a word, so snake_case survives.
    const inWord =
      i > 0 &&
      isAlphanumeric(chars[i - 1]) &&
      chars[i + 1] !== undefined &&
      isAlphanumeric(chars[i + 1])
    if ((c === '*' || c === '_') && !inWord) continue
    out += c
  }
  return out.trim()
}

/**
 * Inline formatting tags that carry no meaning in a terminal. Anything else
 * that looks like a tag — `Vec<String>`, `<id>` — is left alone.
 */
const HTML_FORMAT_TAGS = new Set([
  'b',
  'strong',
  'i',
  'em',
  'u',
  's',
  'strike',
  'del',
  'ins',
  'mark',
  'small',
  'big',
  'sub',
  'sup',
  'code',
  'kbd',
  'samp',
  'var',
  'tt',
  'span',
  'font',
  'q',
  'abbr',
  'cite',
  'pre',
])

/** Read a tag starting at `start`, returning its name and the index after `>`. */
function htmlTagAt(chars: string[], start: number): { name: string; end: number } | null {
  let i = start + 1
  if (chars[i] === '/') i++
  const nameStart = i
  while (i < chars.length && /^[0-9A-Za-z]$/.test(chars[i])) i++
  if (i === nameStart) return null
  const name = chars.slice(nameStart, i).join('')
  while (i < chars.length && chars[i] !== '>') {
    if (chars[i] === '<') return null
    i++
  }
  return chars[i] === '>' ? { name, end: i + 1 } : null
}

export function stripHtmlTags(s: string): string {
  const chars = [...s]
  let out = ''
  let i = 0
  while (i < chars.length) {
    if (chars[i] === '<') {
      const tag = htmlTagAt(chars, i)
      if (tag) {
        const lower = tag.name.toLowerCase()
        if (lower === 'br') {
          out += ' '
          i = tag.end
          continue
        }
        if (HTML_FORMAT_TAGS.has(lower)) {
          i = tag.end
          continue
        }
      }
    }
    out += chars[i]
    i++
  }
  return out
}

/** Strip one matching pair of wrapping delimiters, if present. */
function unwrap(s: string, open: string, close: string): string | null {
  return s.length >= open.length + close.length && s.startsWith(open) && s.endsWith(close)
    ? s.slice(open.length, s.length - close.length)
    : null
}

/**
 * Normalise raw label text: strip markup, unquote, and decode entities.
 *
 * Decoding happens after tag-stripping so `<b>` is removed as markup while
 * `&lt;b&gt;` survives as the literal text `<b>`.
 */
export function cleanLabel(raw: string): string {
  const trimmed = stripHtmlTags(raw.trim()).trim()
  const unquoted = (unwrap(trimmed, '"', '"') ?? unwrap(trimmed, "'", "'") ?? trimmed).trim()
  const md = unwrap(unquoted, '`', '`')
  return decodeHtmlEntities(md === null ? unquoted : stripMarkdown(md.trim()))
}

/** Index of the last identifier-boundary character, or -1. */
function lastBreak(s: string): number {
  let best = -1
  for (const c of LABEL_BREAK_CHARS) best = Math.max(best, s.lastIndexOf(c))
  return best
}

/**
 * Wrap a label to `width` columns over at most `maxLines` lines, truncating the
 * last line with an ellipsis if it overflows.
 *
 * A word too wide to fit is broken after the last identifier boundary
 * (`_-./`) that fits, falling back to a per-character break when it has none.
 */
export function wrapLabel(label: string, width: number, maxLines: number): string[] {
  width = Math.max(1, width)
  const lines: string[] = []
  let cur = ''
  let curW = 0

  for (const word of label.split(/\s+/).filter((w) => w !== '')) {
    const ww = stringWidth(word)
    if (ww > width) {
      if (cur !== '') {
        lines.push(cur)
        cur = ''
      }
      let chunk = ''
      let chunkW = 0
      for (const [ch, cw] of measured(word)) {
        if (chunkW + cw > width && chunk !== '') {
          const p = lastBreak(chunk)
          const carry = p === -1 ? '' : chunk.slice(p + 1)
          lines.push(p === -1 ? chunk : chunk.slice(0, p + 1))
          chunk = carry
          chunkW = stringWidth(carry)
        }
        chunk += ch
        chunkW += cw
      }
      cur = chunk
      curW = chunkW
    } else if (cur === '') {
      cur = word
      curW = ww
    } else if (curW + 1 + ww <= width) {
      cur += ` ${word}`
      curW += 1 + ww
    } else {
      lines.push(cur)
      cur = word
      curW = ww
    }
  }
  if (cur !== '') lines.push(cur)
  if (lines.length === 0) lines.push('')

  if (lines.length > maxLines) {
    lines.length = maxLines
    const target = Math.max(1, width - 1)
    let s = ''
    let sw = 0
    for (const [ch, cw] of measured(lines[lines.length - 1])) {
      if (sw + cw > target) break
      s += ch
      sw += cw
    }
    lines[lines.length - 1] = `${s}…`
  }
  return lines
}

/** Truncate to `inner` columns, leaving room for the ellipsis. */
export function fitLabel(label: string, inner: number): string {
  if (stringWidth(label) <= inner) return label
  let out = ''
  let used = 0
  for (const [c, cw] of measured(label)) {
    if (used + cw + 1 > inner) break
    out += c
    used += cw
  }
  return `${out}…`
}
