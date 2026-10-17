// A structural scanner over stored JSON record strings: it finds a record's top-level "model" array and returns each
// message's own text, without decoding anything. A request body is then a concatenation of stored text, and equals
// JSON.stringify of the parsed messages because the records were written by JSON.stringify.
const STRUCT = /["{}[\]]/g

function stringEnd(s: string, i: number) { // i is just after an opening quote; returns the index after the closing quote
  for (;;) {
    const j = s.indexOf('"', i)
    if (j < 0) throw new SyntaxError("unterminated string")
    let k = j - 1
    while (k >= i && s.charCodeAt(k) === 92) k--
    if ((j - 1 - k) % 2 === 0) return j + 1
    i = j + 1
  }
}

function skipWs(s: string, i: number) {
  for (;;) { const c = s.charCodeAt(i); if (c === 32 || c === 10 || c === 13 || c === 9) i++; else return i }
}

/** The index after the value that starts at i (no leading whitespace). */
function valueEnd(s: string, i: number): number {
  const c = s.charCodeAt(i)
  if (c === 34) return stringEnd(s, i + 1)
  if (c === 123 || c === 91) {
    let depth = 0
    STRUCT.lastIndex = i
    for (;;) {
      const m = STRUCT.exec(s)
      if (!m) throw new SyntaxError("unterminated value")
      const d = s.charCodeAt(m.index)
      if (d === 34) { STRUCT.lastIndex = stringEnd(s, m.index + 1); continue }
      if (d === 123 || d === 91) depth++
      else if (--depth === 0) return m.index + 1
    }
  }
  let j = i
  for (; j < s.length; j++) { const d = s.charCodeAt(j); if (d === 44 || d === 125 || d === 93 || d === 32 || d === 10 || d === 13 || d === 9) break }
  if (j === i) throw new SyntaxError("expected a value")
  return j
}

/** The raw text of each message in the record's top-level "model" array (the last such key wins, as JSON.parse does). */
export function modelMessages(record: string): string[] {
  let i = skipWs(record, 0)
  if (record.charCodeAt(i) !== 123) throw new SyntaxError("record is not an object")
  i = skipWs(record, i + 1)
  let out: string[] = []
  if (record.charCodeAt(i) === 125) return out
  for (;;) {
    if (record.charCodeAt(i) !== 34) throw new SyntaxError("expected a key")
    const end = stringEnd(record, i + 1)
    const raw = record.slice(i + 1, end - 1)
    const key = raw.includes("\\") ? JSON.parse(record.slice(i, end)) as string : raw
    i = skipWs(record, end)
    if (record.charCodeAt(i) !== 58) throw new SyntaxError("expected a colon")
    i = skipWs(record, i + 1)
    if (key === "model" && record.charCodeAt(i) === 91) {
      out = []
      i = skipWs(record, i + 1)
      if (record.charCodeAt(i) === 93) i++
      else for (;;) {
        const e = valueEnd(record, i)
        out.push(record.slice(i, e))
        i = skipWs(record, e)
        const c = record.charCodeAt(i)
        i++
        if (c === 44) { i = skipWs(record, i); continue }
        if (c === 93) break
        throw new SyntaxError("expected , or ]")
      }
    } else {
      if (key === "model") out = [] // "model" with a non-array value contributes no messages
      i = valueEnd(record, i)
    }
    i = skipWs(record, i)
    const c = record.charCodeAt(i)
    i++
    if (c === 44) { i = skipWs(record, i); continue }
    if (c === 125) return out
    throw new SyntaxError("expected , or }")
  }
}
