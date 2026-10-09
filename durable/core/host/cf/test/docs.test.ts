// The ABI document and the Go tables must agree: the api request table and the import list in ABI.md are checked against
// abi.json, which is generated from the Go source (abi/spec/cmd/abigen).
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import abi from "../../../abi/abi.json" with { type: "json" }
import { root } from "./helpers.ts"

const doc = readFileSync(join(root, "..", "..", "..", "..", "docs", "plan", "durable-core", "ABI.md"), "utf8")

test("ABI.md 8.1 lists exactly the api operations of the Go table", () => {
  const section = doc.slice(doc.indexOf("### 8.1"), doc.indexOf("## 9."))
  const ops = [...section.matchAll(/^\| `([\w.]+)` \|/gm)].map(m => m[1])
  assert.deepEqual(ops, abi.api.map(r => r.op))
})

test("ABI.md 4.2 names every wasi import of the Go table", () => {
  const section = doc.slice(doc.indexOf("### 4.2"), doc.indexOf("### 4.3"))
  for (const i of abi.imports.filter(i => i.name.startsWith("wasi_snapshot_preview1."))) assert.ok(section.includes(`\`${i.name.split(".")[1]}\``), `${i.name} is not in ABI.md 4.2`)
})

test("ABI.md event, effect and notice numbers match the Go tables", () => {
  for (const [title, rows] of [["## 5.", abi.events], ["## 6.", abi.effects], ["## 7.", abi.notices]] as const) {
    const start = doc.indexOf(title)
    const section = doc.slice(start, doc.indexOf("\n## ", start + 4))
    const listed = [...section.matchAll(/^\| (\d+) \| `(\w+)` \|/gm)].map(m => `${m[1]} ${m[2]}`)
    assert.deepEqual(listed, rows.map(r => `${r.number} ${r.name}`), title)
  }
})
