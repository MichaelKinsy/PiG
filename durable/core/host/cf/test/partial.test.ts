// The host side of the partial patch protocol (src/partial.ts): a stream's partial message goes to the core as what grew since the last one.
import assert from "node:assert/strict"
import { test } from "node:test"
import { PartialPatcher } from "../src/partial.ts"

test("a text block goes whole first, then as the text its deltas added since", () => {
  const patcher = new PartialPatcher()
  const block = { type: "text", text: "hel" }
  assert.deepEqual(patcher.patch({ role: "assistant", content: [block] }).content[0], { type: "text", text: "hel" })
  patcher.observe({ type: "text_delta", contentIndex: 0, delta: "lo " })
  patcher.observe({ type: "text_delta", contentIndex: 0, delta: "wor" })
  block.text = "hello wor"
  assert.deepEqual(patcher.patch({ role: "assistant", content: [block] }).content[0], { type: "text", text: "", $a: "lo wor" })
  patcher.observe({ type: "text_delta", contentIndex: 0, delta: "ld" })
  block.text = "hello world"
  assert.deepEqual(patcher.patch({ role: "assistant", content: [block] }).content[0], { type: "text", text: "", $a: "ld" })
})

test("a text whose deltas do not add up to its length goes whole", () => {
  const patcher = new PartialPatcher()
  const block = { type: "thinking", thinking: "a first thought" }
  patcher.patch({ content: [block] })
  patcher.observe({ type: "thinking_delta", contentIndex: 0, delta: "s" })
  block.thinking = "something else entirely"
  assert.deepEqual(patcher.patch({ content: [block] }).content[0], { type: "thinking", thinking: "something else entirely" })
  patcher.observe({ type: "thinking_delta", contentIndex: 0, delta: "!" })
  patcher.observe({ type: "thinking_end", contentIndex: 0 })
  block.thinking = "something else entirely!"
  assert.deepEqual(patcher.patch({ content: [block] }).content[0], { type: "thinking", thinking: "something else entirely!" })
})

test("a block that ended goes whole once, then as nothing added", () => {
  const patcher = new PartialPatcher()
  const block = { type: "text", text: "done" }
  patcher.patch({ content: [block] })
  patcher.observe({ type: "text_end", contentIndex: 0 })
  assert.deepEqual(patcher.patch({ content: [block] }).content[0], { type: "text", text: "done" })
  assert.deepEqual(patcher.patch({ content: [block] }).content[0], { type: "text", text: "", $a: "" })
})

test("a tool call goes as the operations its streamed arguments made, and whole once it ended", () => {
  const patcher = new PartialPatcher()
  const call = { type: "toolCall", id: "c1", name: "write", arguments: {} as Record<string, unknown> }
  patcher.observe({ type: "toolcall_start", contentIndex: 0 })
  patcher.observe({ type: "toolcall_delta", contentIndex: 0, delta: '{"path":"a' })
  call.arguments = { path: "a" }
  const first = patcher.patch({ content: [call] }).content[0]
  assert.equal(first.type, "toolCall")
  assert.deepEqual(first.arguments, {})
  assert.ok(Array.isArray(first.$o) && first.$o.length > 0)
  patcher.observe({ type: "toolcall_delta", contentIndex: 0, delta: '.ts"}' })
  const second = patcher.patch({ content: [call] }).content[0]
  assert.deepEqual(second.$o, [["a", ["path"], ".ts"]])
  patcher.observe({ type: "toolcall_end", contentIndex: 0 })
  call.arguments = { path: "a.ts" }
  assert.deepEqual(patcher.patch({ content: [call] }).content[0], call)
})

test("the whole partial is not copied: blocks the patcher does not know pass through", () => {
  const patcher = new PartialPatcher()
  const image = { type: "image", data: "x" }
  assert.equal(patcher.patch({ content: [image] }).content[0], image)
})
