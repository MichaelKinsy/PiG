// Serves one codemode execution over standard input and output, for the Go behavior tests of codemode/workerhost_test.go: the
// real Worker host (serve.ts, the TinyGo module, QuickJS) as a remote whose exchanges are JSON lines.
//
//   stdin  line 1: { request, options }        the CodemodeRemoteRequest and the serve limits
//          later:  { id, reply } | { id, error }  the answer to the exchange with that id
//   stdout: { id, messages }                   an exchange, one line each; then { done: true } when the execution has ended
import { createInterface } from "node:readline"
import { serveCodemodeRemote } from "../../src/codemode/serve.ts"
import type { CodemodeRemoteMessage, CodemodeRemoteReply } from "../../src/codemode/protocol.ts"
import { codemodeModules } from "../codemode-helpers.ts"

const pending = new Map<number, { resolve: (reply: CodemodeRemoteReply) => void; reject: (error: Error) => void }>()
let next = 0
let started = false

const write = (value: unknown) => new Promise<void>(resolve => process.stdout.write(`${JSON.stringify(value)}\n`, () => resolve()))

const exchange = (messages: CodemodeRemoteMessage[]) =>
  new Promise<CodemodeRemoteReply>((resolve, reject) => {
    const id = ++next
    pending.set(id, { resolve, reject })
    void write({ id, messages })
  })

createInterface({ input: process.stdin }).on("line", line => {
  const message = JSON.parse(line) as { request?: never; options?: object; id?: number; reply?: CodemodeRemoteReply; error?: string }
  if (!started) {
    started = true
    void serveCodemodeRemote(message.request!, exchange, { modules: codemodeModules(), ...message.options }).then(async () => {
      await write({ done: true })
      process.exit(0)
    })
    return
  }
  const waiter = pending.get(message.id!)
  pending.delete(message.id!)
  if (message.error !== undefined) waiter?.reject(new Error(message.error))
  else waiter?.resolve(message.reply ?? null)
})
