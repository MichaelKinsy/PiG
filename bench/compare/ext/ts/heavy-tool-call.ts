// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
// Benchmark extension: a tool_call handler that does about 2 ms of work per call and counts its calls in $BENCHEXT_COUNTER (proof it ran).
import { createHash } from "node:crypto";
import { appendFileSync } from "node:fs";

export default function (pi: any) {
	pi.on("tool_call", async (event: any) => {
		const h = createHash("sha256");
		const block = Buffer.alloc(65536, 7);
		for (let i = 0; i < 16; i++) h.update(block);
		h.update(JSON.stringify(event.input ?? {}));
		if (process.env.BENCHEXT_COUNTER) appendFileSync(process.env.BENCHEXT_COUNTER, "ts\n");
		return undefined;
	});
}
