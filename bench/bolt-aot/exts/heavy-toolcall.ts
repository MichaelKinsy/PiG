// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
// A "heavy" tool_call handler: inspects every tool call's arguments the way a policy/guard extension does
// (serialize, scan for secrets-like patterns, hash). Work is O(args) plus a fixed 200k-iteration scan.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const SECRET = /(?:api[_-]?key|secret|token|password)\s*[:=]\s*["']?([A-Za-z0-9_\-]{16,})/i;
let calls = 0;
let blocked = 0;

function fnv1a(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h;
}

function scan(text: string): number {
  // fixed work so the handler is measurably "heavy" even for small inputs
  let acc = 0;
  const pad = text.length > 0 ? text : "x";
  for (let i = 0; i < 200_000; i++) acc = (acc + pad.charCodeAt(i % pad.length) * (i & 7)) | 0;
  return acc;
}

export default function heavyToolCall(pi: ExtensionAPI) {
  pi.on("tool_call", async (event) => {
    calls++;
    const text = JSON.stringify(event.input ?? {});
    const digest = fnv1a(text) ^ scan(text);
    if (SECRET.test(text)) {
      blocked++;
      return { block: true, reason: `blocked secret-like argument (${digest.toString(16)})` };
    }
    return undefined;
  });
  pi.registerCommand("heavy-stats", {
    description: "tool_call handler stats",
    handler: async (_args, ctx) => { ctx.ui.notify(`calls=${calls} blocked=${blocked}`, "info"); },
  });
}
