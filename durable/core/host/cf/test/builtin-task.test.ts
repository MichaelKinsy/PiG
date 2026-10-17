import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { TxProxy } from "../src/facade/tx.ts"
import { CompactionTask } from "../src/facade/define.ts"
test("a built-in task is created without a host checkpoint: the core builds it (Conversation.compact)", async () => {
  const sent: any[] = []
  const tx = new TxProxy({ op: async (op: string, args: any) => { sent.push([op, args]); return 7 } } as any)
  await tx.createTask(CompactionTask as any, { reason: "manual" }, { ownership: { kind: "conversation" }, conversationId: 1 })
  assert.equal(sent[0][0], "createTask")
  assert.equal("state" in sent[0][1], false)
  void BACKGROUND_CONTEXT
})

test("a custom task still gets its checkpoint from initial(input) in the host", async () => {
  const sent: any[] = []
  const tx = new TxProxy({ op: async (op: string, args: any) => { sent.push([op, args]); return 8 } } as any)
  await tx.createTask({ definition: { name: "mine", version: 2, initial: (input: any) => ({ phase: "start", n: input.n }) } }, { n: 3 }, { ownership: { kind: "conversation" } })
  assert.deepEqual(sent[0][1].state, { phase: "start", n: 3 })
  assert.equal(sent[0][1].version, 2)
})
