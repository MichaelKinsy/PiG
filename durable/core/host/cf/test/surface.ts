// Compile-time check of the facade's surface against pi-durable's declarations (run by `tsc -p .`): every member of the public
// objects exists, except the gaps named here. A gap that gets implemented must leave the list, and a new gap must enter it, so the
// list is the honest record of what the host does not offer yet. Members that exist but throw "not supported yet" are in
// docs/plan/durable-core PROTOCOL.md, "Not supported by the CF host yet".
import type * as P from "@earendil-works/pi-durable"
import type { Conversation, Harness, Submission } from "../src/facade/harness.ts"

type Gaps<Upstream, Facade, Known extends PropertyKey> = [Exclude<keyof Upstream, keyof Facade | Known>, Extract<Known, keyof Facade>] extends [never, never] ? true : never

export const harnessSurface: Gaps<P.Harness, Harness, never> = true
export const conversationSurface: Gaps<P.Conversation, Conversation, never> = true
export const submissionSurface: Gaps<P.Submission, Submission, never> = true
