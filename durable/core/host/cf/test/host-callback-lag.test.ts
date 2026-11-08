// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// The ticks from the core's grant of the Session line (the `ready` answer, core/tx.go grant) to the first operation of a host transaction's callback
// reaching the core: hopcost.HostFirstOp = HostBeginResume (`await begin()` in runCommit) + HostOpDispatch (TxChannel.op's `chain.then`). The core
// notes a mint of the host's callback by this offset (session.HostCallbackLag use in Tx.mint), so the constants must read what the facade spends.
// The facade counts the passes itself (TxChannel.elapsed, the `elapsed` of the end operation); this reads that count as the first operation is sent.
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { Harness } from "../src/facade/harness.ts"
import { createRegistry } from "../src/facade/index.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { EVENT } from "../src/abi.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const HOST_BEGIN_RESUME = 1, HOST_OP_DISPATCH = 1

test("the first operation of a host transaction's callback reaches the core hopcost.HostFirstOp passes after the grant", async () => {
  const module = new (WebAssembly.Module as unknown as new (b: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))
  const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
  const models = createModels()
  models.setProvider(faux.provider)
  const harness = await Harness.open(await openNodeSqliteStorage(join(tempDir("host-callback-lag"), "lag.sqlite")), { models, registry: createRegistry(), core: module } as any, context)
  const client = (harness as any).client
  let channel: { elapsed: number } | undefined
  const begin = client.begin.bind(client)
  // Not async: the caller's resumption must stay the begin promise's own.
  client.begin = (...args: unknown[]) => { const ready = begin(...args); ready.then((c: { elapsed: number }) => { channel = c }); return ready }
  const host = client.host
  const send = host.send.bind(host)
  const operations: number[] = []
  host.send = (kind: number, bytes: Uint8Array, ...rest: unknown[]) => { if (kind === EVENT.tx && channel !== undefined) operations.push(channel.elapsed); return send(kind, bytes, ...rest) }
  const root_ = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
  channel = undefined
  operations.length = 0
  await harness.commit(async tx => { await tx.conversation(root_.id) }, context)
  await harness.close(context)
  assert.equal(operations[0], HOST_BEGIN_RESUME + HOST_OP_DISPATCH)
})
