// The messages of the remote codemode protocol, in the shapes of @earendil-works/pi-codemode (runtime/protocol.ts and
// runtime/remote.ts): the codemode Go module speaks the same JSON, so a remote of either side serves a client of the other.

import type { CodemodeDuring, CodemodeError, CodemodeOutputItem } from "./types.ts"
export type { CodemodeError, CodemodeErrorKind, CodemodeOutputItem } from "./types.ts"

export interface VmStartData {
  code: string
  /** `jsName` is the identifier the script uses; `description` is listed in `ALL_TOOLS`. */
  tools: { name: string; jsName: string; description: string }[]
  globals: { name: string; spread: boolean }[]
  memoryLimitBytes: number | undefined
  /** Snapshot for `load()`: key to JSON text. */
  store: Record<string, string>
  /**
   * Run deterministically (`CodemodeSandboxBase.start()`): UTC, a virtual clock starting at `startedAt` (ms since the epoch) that moves only with delivered results,
   * randomness from `seed`, no `WeakRef`, results applied in `seq` order, and `started`/`idle` messages.
   */
  deterministic?: { startedAt: number; seed: number }
}

export type WorkerToHostMessage =
  | { type: "call"; id: number; target: "tool" | "global"; name: string; args: string | undefined }
  | { type: "output"; item: CodemodeOutputItem }
  /** Deterministic mode: `during` is the result the VM was processing when the script finished (0: its first run) and the calls issued by then. */
  | { type: "done"; ok: true; value: string | undefined; writes: string; during?: CodemodeDuring }
  | { type: "done"; ok: false; error: string; during?: CodemodeDuring }
  | { type: "crash"; message: string }
  /** Deterministic mode, before the script runs: what the VM is, so a replay can refuse a different one. */
  | { type: "started"; fingerprint: string }
  /** Deterministic mode, after the script waits again: having processed result `seq` (0: the script's first run), it has issued `issued` calls in total. */
  | { type: "idle"; seq: number; issued: number }

/**
 * `payload` is the JSON result when `ok`, otherwise the error message. Deterministic mode: `seq` numbers the results from 1 in the order the VM applies them, and the VM's clock
 * reads `now` while it processes this one; `properties`, with `ok: false`, is a JSON object whose properties the script's `Error` gets.
 */
export type HostToWorkerMessage = { type: "result"; id: number; ok: boolean; payload: string | undefined; seq?: number; now?: number; properties?: string }

/** What the remote sends to the client: VM messages, or a failure outside the VM bridge. */
export type CodemodeRemoteMessage = WorkerToHostMessage | { type: "failure"; error: CodemodeError }

/** The client's answer to an exchange: the result of the call it carried, `null` when it carried no call, or `stop`. */
export type CodemodeRemoteReply = HostToWorkerMessage | { type: "stop" } | null

export type CodemodeRemoteExchange = (messages: CodemodeRemoteMessage[]) => Promise<CodemodeRemoteReply>

export interface CodemodeRemoteRequest {
  data: VmStartData
  /** Requested interrupt budget. The remote may lower it, see {@link CodemodeServeOptions}. */
  interruptBudget?: number
}

export interface CodemodeRemote {
  execute(request: CodemodeRemoteRequest, exchange: CodemodeRemoteExchange): Promise<void>
}

export function isWorkerToHostMessage(value: unknown): value is WorkerToHostMessage {
  if (typeof value !== "object" || value === null) return false
  const type = (value as { type?: unknown }).type
  return type === "call" || type === "output" || type === "done" || type === "crash" || type === "started" || type === "idle"
}

export function isHostToWorkerMessage(value: unknown): value is HostToWorkerMessage {
  return typeof value === "object" && value !== null && (value as { type?: unknown }).type === "result"
}
