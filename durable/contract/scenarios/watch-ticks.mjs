// The tick cost of attaching an observer (rows F-ticks-watch-<profile>): how many microtask passes `Conversation.watch` takes to settle on an
// idle conversation, on an empty one, one with a turn, a mounted view, a reopened Harness and a window that starts at an entry. An observer that attaches
// in fewer passes than Pi lands before a submit's commit Pi orders first (F-gate-175: two observers on one conversation, the later one attached
// after the submit in Pi and before it here). The count is
// exact; every wait is a tick or a detenv virtual timer. Run by lib/run.mjs under the hooks.
import { join } from "node:path"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, Harness } from "@earendil-works/pi-durable"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"

const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
const models = createModels()
models.setProvider(faux.provider)
const respond = () => { faux.appendResponses([respond]); return fauxAssistantMessage("answer") }
faux.setResponses([respond])
const harness = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "watch-ticks.sqlite")), { models, registry: createRegistry() }, context)
const root = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })

let passes = 0, counting = false
const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
const measure = async (name, call) => {
	passes = 0; counting = true; queueMicrotask(spin)
	const result = await call()
	counting = false
	console.log(`${name}: ${passes}`)
	return result
}

const window = { min: 1, max: 2 }
const windowed = await measure("watch window (idle)", () => root.watch(context, window))
await windowed.stop()
const view = await measure("watch (idle)", () => root.watch(context))
await view.stop()

// The same attach on a conversation with entries: the bounds read the head marker and activeEntries scans the range (view.ts #build,
// context.ts captureContextBounds/activeEntries). A virtual timer lets the turn's jobs finish before the count starts.
const turn = await root.submit({ type: "input", content: "first" }, context)
await turn.wait(context)
await new Promise(resolve => setTimeout(resolve, 1000))
const later = await measure("watch window (entries)", () => root.watch(context, window))
await later.stop()
const view2 = await measure("watch (entries)", () => root.watch(context))
await view2.stop()

// An observer attaching to a view another observer holds: nothing is built, the line job only creates the observer
// (view.ts attach finds the mount). F-gate-175 is this case.
const holder = await root.watch(context)
const joinedWindow = await measure("watch window (mounted)", () => root.watch(context, window))
await joinedWindow.stop()
const joinedState = await measure("watch (mounted)", () => root.watch(context))
await joinedState.stop()
await holder.stop()

await harness.close(context)

// A reopened Harness (its open jobs finished): its Session has cached no document, so #build finds each mounted document in Storage (findDocument, document()), or
// finds it absent (session.ts #loadDocument).
const reopened = await Harness.open(await openNodeSqliteStorage(join(process.env.TMPDIR ?? "/tmp", "watch-ticks.sqlite")), { models, registry: createRegistry() }, context)
await new Promise(resolve => setTimeout(resolve, 1000))
const found = await reopened.conversation(root.id, context)
const again = await measure("watch window (reopened)", () => found.watch(context, window))
await again.stop()
const view3 = await measure("watch (reopened)", () => found.watch(context))
await view3.stop()
await reopened.close(context)
