// HarnessOptions.conversationCreated runs in the creating transaction after the core's built-in creation hook: the same
// proxy, so a document it opens is the one the caller sees, and a throw fails the operation (harness.ts conversationCreated).
import assert from "node:assert/strict"
import { test } from "node:test"
import { CoreClient, TxChannel } from "../src/client.ts"
import { TxProxy } from "../src/facade/tx.ts"

function channelFor(client: CoreClient, ops: string[], record: unknown) {
  const channel = Object.create(TxChannel.prototype) as TxChannel & { client: CoreClient }
  Object.assign(channel, { client, op: async (name: string) => { ops.push(name); return record } })
  return channel
}

test("conversationCreated runs after each creating operation, with that transaction's proxy and the record", async () => {
  const client = Object.create(CoreClient.prototype) as CoreClient
  const ops: string[] = []
  const seen: unknown[] = []
  client.onConversationCreated = async (tx, record) => { ops.push("callback"); seen.push(tx, record) }
  const record = { id: 7 }
  const tx = new TxProxy(channelFor(client, ops, record))
  assert.equal(await tx.createRootConversation(), record)
  await tx.createConversation({ ownership: { kind: "ownerless" } })
  await tx.forkConversation(1, 2, { ownership: { kind: "ownerless" } })
  assert.deepEqual(ops, ["createRootConversation", "callback", "createConversation", "callback", "forkConversation", "callback"])
  assert.ok(seen[0] === tx && seen[1] === record)
})

test("a throwing conversationCreated fails the creating operation", async () => {
  const client = Object.create(CoreClient.prototype) as CoreClient
  client.onConversationCreated = async () => { throw new Error("no") }
  const tx = new TxProxy(channelFor(client, [], { id: 7 }))
  await assert.rejects(tx.createConversation({ ownership: { kind: "ownerless" } }), /no/)
})

test("no callback: the operation answers the record", async () => {
  const tx = new TxProxy(channelFor(Object.create(CoreClient.prototype) as CoreClient, [], { id: 3 }))
  assert.deepEqual(await tx.createRootConversation(), { id: 3 })
})
