#!/usr/bin/env node
// rowdiff a.trace b.trace [--level store,commit,context,output] [--stores-a dir --stores-b dir] [--json]
// Exit status 1 on any difference, 2 on a usage error.
import { readdirSync } from "node:fs"
import { join } from "node:path"
import { diffTraces, LEVELS, readTrace } from "../lib/rowdiff.mjs"

const args = process.argv.slice(2)
const opt = name => { const i = args.indexOf(`--${name}`); return i < 0 ? undefined : args.splice(i, 2)[1] }
const json = args.includes("--json") && args.splice(args.indexOf("--json"), 1)
const levels = opt("level")?.split(",") ?? LEVELS
const dirA = opt("stores-a"), dirB = opt("stores-b")
const [pa, pb] = args
if (!pa || !pb || levels.some(l => !LEVELS.includes(l))) { console.error("usage: rowdiff a.trace b.trace [--level store,commit,context,output] [--stores-a dir --stores-b dir] [--json]"); process.exit(2) }
const files = dir => (dir ? readdirSync(dir).filter(f => f.endsWith(".sqlite")).sort().map(f => join(dir, f)) : [])
const report = diffTraces(readTrace(pa), readTrace(pb), { levels, storesA: files(dirA), storesB: files(dirB) })
console.log(json ? JSON.stringify(report) : report.equal ? "equal" : `DIFFERENT\n${JSON.stringify(report.reports, null, 1)}`)
process.exit(report.equal ? 0 : 1)
