// The SC row's Storage for the native candidate (CONTRACT 3.10): pi-durable's SqliteStorage answers every read from the
// database file, and the core's write encoder (durable/core/contracttest/cmd/scprovider, a Go process on the same file)
// answers commit and mintId. The reads are always the reference's (`?real` bypasses a facade candidate's own storage modules).
// Run by sc.test.ts through CONTRACT_SC_PROVIDER; CONTRACT_SC_EXE names the Go process.
import { spawn } from "node:child_process"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { createInterface } from "node:readline"
import { pathToFileURL } from "node:url"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"

export async function withStorage(use: (storage: any) => Promise<void>): Promise<void> {
	const exe = process.env.CONTRACT_SC_EXE
	if (!exe) throw new Error("CONTRACT_SC_EXE is not set")
	const { openNodeSqliteStorage } = await import(`${pathToFileURL(join(process.env.CONTRACT_PI_SOURCE!, "packages", "durable", "src", "storage", "sqlite", "node.ts")).href}?real`)
	const dir = await mkdtemp(join(tmpdir(), "contract-sc-native-"))
	const path = join(dir, "store.sqlite")
	// pi-durable creates the schema and metadata; the core cold-opens the file it left.
	const storage = await openNodeSqliteStorage(path)
	const child = spawn(exe, [], { stdio: ["pipe", "pipe", "inherit"] })
	const pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>()
	let next = 1
	let exited: Error | undefined
	createInterface({ input: child.stdout }).on("line", line => {
		const m = JSON.parse(line)
		const p = pending.get(m.id)
		if (!p) return
		pending.delete(m.id)
		if ("error" in m) p.reject(Object.assign(new Error(m.error.message), { name: m.error.name }))
		else p.resolve(m.ok)
	})
	child.on("exit", (code, signal) => {
		exited = new Error(`scprovider exited (${code ?? signal})`)
		for (const p of pending.values()) p.reject(exited)
		pending.clear()
	})
	const call = (op: string, args: object = {}) => new Promise<unknown>((resolve, reject) => {
		if (exited) return reject(exited)
		const id = next++
		pending.set(id, { resolve, reject })
		child.stdin.write(JSON.stringify({ id, op, ...args }) + "\n")
	})
	await call("open", { path })
	let closed = false
	const guard = () => { if (closed) throw new Error("storage is closed") }
	const wrapped = new Proxy(storage, {
		get(target, prop) {
			if (prop === "commit") return async (writes: unknown, _context: unknown) => { guard(); return call("commit", { writes }) }
			if (prop === "mintId") return async () => { guard(); return call("mint") }
			if (prop === "close") return async (context: unknown) => {
				if (closed) return
				closed = true
				await call("close").catch(() => undefined)
				await (target as any).close(context)
			}
			const value = (target as any)[prop]
			return typeof value === "function" ? value.bind(target) : value
		},
	})
	try { await use(wrapped) } finally {
		if (!closed) { closed = true; await call("close").catch(() => undefined); await (storage as any).close(BACKGROUND_CONTEXT) }
		child.kill()
		await rm(dir, { recursive: true, force: true })
	}
}
