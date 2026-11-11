// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Pi main 50d9f823 (#11604) packages/chord/test/output.test.ts: the host's sanitizeOutput removes whole ANSI escape sequences,
// not only their ESC bytes, also when a sequence is split across the chunks of an OutputBuffer.
import assert from "node:assert/strict"
import { test } from "node:test"
import { OutputBuffer, sanitizeOutput } from "../src/output.ts"

test("removes whole ANSI escape sequences, not only their ESC bytes", () => {
  assert.equal(sanitizeOutput("\x1b[31mERROR\x1b[0m\n"), "ERROR\n")
  assert.equal(sanitizeOutput("\x1b[1;38;5;208mx\x1b[?25l\x1b[2K\x1b(By\x1b7z"), "xyz")
  assert.equal(sanitizeOutput("\x1b]8;;https://pi.dev\x07link\x1b]8;;\x1b\\ \x1b]0;title\x07ok"), "link ok")
  const output = new OutputBuffer({ retain: "tail", maxBytes: 1024, maxLines: 100 })
  output.push("\x1b[3")
  output.push("1mERROR\x1b[0")
  output.push("m\n")
  assert.equal(output.snapshot().text, "ERROR\n")
})
