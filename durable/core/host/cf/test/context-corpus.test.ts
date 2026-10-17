import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { readContextFrom, type ContextRange, type EntryRecord, type HeadMarker } from "../src/context.ts"
import { MemorySource, type Conv } from "./memory-source.ts"

// The corpus pi-durable main wrote (history/oracle/gen_*.mjs): C conversation records, E entries, Q the sha256 of the text of
// the context it derived (or `!hex message` for its error). The TS port must derive the same text, cold and with a kept range.
const corpusDir = join(import.meta.dirname, "../../../history/testdata")

const unhex = (h: string) => Buffer.from(h, "hex").toString("utf8")

const text = (v: { head: HeadMarker | undefined; entries: readonly EntryRecord[]; contributions: readonly (readonly unknown[])[]; messages: readonly unknown[] }) =>
  `{"head":${v.head === undefined ? "null" : v.head.id},"entries":[${v.entries.map((e) => e.id).join(",")}],"contributions":[${v.contributions.map((c) => `[${c.map((m) => JSON.stringify(m)).join(",")}]`).join(",")}],"messages":[${v.messages.map((m) => JSON.stringify(m)).join(",")}]}`

for (const file of ["context.hex", "real.hex", "real104.hex"]) {
  for (const mode of ["cold", "kept range", "kept range extended in place"]) {
    const warm = mode !== "cold", owned = mode === "kept range extended in place"
    test(`${file}: the TS context derivation equals pi-durable main's (${mode})`, () => {
      const source = new MemorySource()
      const kept = new Map<number, ContextRange>()
      let queries = 0
      for (const line of readFileSync(join(corpusDir, file), "utf8").split("\n")) {
        const f = line.split(" ")
        if (f[0] === "C") {
          const rec = JSON.parse(unhex(f[1]!)) as Conv
          source.convs.set(rec.id, rec)
        } else if (f[0] === "E") {
          const rec = JSON.parse(unhex(f[3]!)) as EntryRecord
          const list = source.own.get(Number(f[1])) ?? []
          list.push(rec)
          source.own.set(Number(f[1]), list)
        } else if (f[0] === "Q") {
          queries++
          const conv = Number(f[1]), at = Number(f[2])
          let got: string
          try {
            const { view, range } = readContextFrom(source, conv, at === 0 ? undefined : at, warm ? kept.get(conv) : undefined, owned)
            const prev = kept.get(conv)
            if (warm && range !== undefined && (prev === undefined || prev.bounds.tail <= range.bounds.tail)) kept.set(conv, range)
            got = createHash("sha256").update(text(view)).digest("hex")
          } catch (e) {
            got = `!${Buffer.from((e as Error).message).toString("hex")}`
          }
          assert.equal(got, f[3], `Q ${conv} ${at} (query ${queries})`)
        }
      }
      assert.ok(queries >= 10, `${queries} queries`)
    })
  }
}
