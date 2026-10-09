// The workload the vendored core reads (the published durable-bench plan): one definition, lib/workload.mjs.
import { next as nextStep, payload as payloadOf } from "../../lib/workload.mjs"

export type Message = { role: "user" | "assistant" | "tool"; text: string; calls?: number[] }
export type Step = { call: number } | { answer: string }
export type Turn = { id: string; text: string }

export const next = (context: readonly Message[]): Step => nextStep(context)
// W4c: CONTRACT_PAYLOAD_KB sizes every lookup result, as the reference scenario's --payload-kb does.
export const payload = (n: number): string => payloadOf(n, Number(process.env.CONTRACT_PAYLOAD_KB ?? 0))
