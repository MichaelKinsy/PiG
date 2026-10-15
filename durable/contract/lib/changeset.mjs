// Decoder for SQLite session changesets (the binary format of sqlite3session_changeset), enough for CONTRACT section 7.1:
// per committed transaction, which rows were inserted, updated (with before and after images) or deleted, by table.
// Values: null, number (integer or real), string (text), Uint8Array (blob).

export function decodeChangeset(bytes) {
  const out = []
  let p = 0
  const varint = () => {
    let v = 0n
    for (let i = 0; i < 9; i++) {
      const b = bytes[p++]
      if (i === 8) { v = (v << 8n) | BigInt(b); break }
      v = (v << 7n) | BigInt(b & 0x7f)
      if (!(b & 0x80)) break
    }
    return Number(v)
  }
  const dv = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const value = () => {
    const type = bytes[p++]
    switch (type) {
      case 0: return undefined // undefined: the column is unchanged in an update
      case 1: { const v = dv.getBigInt64(p); p += 8; return Number(v) }
      case 2: { const v = dv.getFloat64(p); p += 8; return v }
      case 3: { const n = varint(); const s = new TextDecoder().decode(bytes.subarray(p, p + n)); p += n; return s }
      case 4: { const n = varint(); const b = bytes.slice(p, p + n); p += n; return b }
      case 5: return null
      default: throw new Error(`changeset: unknown value type ${type} at ${p}`)
    }
  }
  let table
  while (p < bytes.length) {
    const marker = bytes[p]
    if (marker === 0x54) { // 'T': table header
      p++
      const nCol = varint()
      const pk = Array.from(bytes.subarray(p, p + nCol)); p += nCol
      let end = p; while (bytes[end] !== 0) end++
      table = { name: new TextDecoder().decode(bytes.subarray(p, end)), nCol, pk }
      p = end + 1
      continue
    }
    const op = bytes[p++]
    p++ // indirect flag
    if (op === 0x12) out.push({ table: table.name, op: "insert", after: Array.from({ length: table.nCol }, value) })
    else if (op === 0x09) out.push({ table: table.name, op: "delete", before: Array.from({ length: table.nCol }, value) })
    else if (op === 0x17) {
      const before = Array.from({ length: table.nCol }, value)
      const after = Array.from({ length: table.nCol }, value)
      out.push({ table: table.name, op: "update", before, after: after.map((v, i) => v === undefined ? before[i] : v) })
    } else throw new Error(`changeset: unknown operation ${op} at ${p - 2}`)
    out[out.length - 1].pk = table.pk
  }
  return out
}
