// Wrapper of @earendil-works/pi-ai/utils/uuid (hooks/resolve.mjs): uuidv7() values go through the tape.
import { wrapUuid } from "../lib/tape.mjs"

export const uuidv7 = real => wrapUuid(real)
