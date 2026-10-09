// Wrapper of @earendil-works/pi-ai/models (hooks/resolve.mjs): every Models object the scenario creates records or
// replays its model calls on the tape.
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai/utils/event-stream"
import { wrapModels } from "../lib/tape.mjs"

export const createModels = real => (...args) => wrapModels(real(...args), { createStream: createAssistantMessageEventStream })
