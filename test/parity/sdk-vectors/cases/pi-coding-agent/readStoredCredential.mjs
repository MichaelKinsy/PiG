// The answer is wrapped so that Pi's undefined is a value of the vector, not a missing result. The credential an auth.json holds for a provider, in the key order of the file: the case writes the file text to a temporary path and passes it.
export const cases = [
  { name: "explicit", args: ["anthropic", "{\"anthropic\":{\"type\":\"api_key\",\"key\":\"explicit-key\",\"env\":{\"B\":\"2\",\"A\":\"1\"},\"10\":\"x\",\"2\":\"y\",\"nested\":{\"z\":1,\"1\":2,\"é\":\"😀\"},\"list\":[3,{\"b\":1,\"a\":2}]},\"other\":5}"] },
  { name: "byte order mark", args: ["anthropic", "﻿{\"anthropic\":{\"type\":\"api_key\",\"key\":\"explicit-key\",\"env\":{\"B\":\"2\",\"A\":\"1\"},\"10\":\"x\",\"2\":\"y\",\"nested\":{\"z\":1,\"1\":2,\"é\":\"😀\"},\"list\":[3,{\"b\":1,\"a\":2}]}}"] },
  { name: "absent provider", args: ["nobody", "{\"anthropic\":{\"type\":\"api_key\",\"key\":\"explicit-key\",\"env\":{\"B\":\"2\",\"A\":\"1\"},\"10\":\"x\",\"2\":\"y\",\"nested\":{\"z\":1,\"1\":2,\"é\":\"😀\"},\"list\":[3,{\"b\":1,\"a\":2}]}}"] },
  { name: "missing file", args: ["anthropic", undefined] },
  { name: "not json", args: ["anthropic", "{not json"] },
  { name: "not an object", args: ["anthropic", "[1,2]"] },
  { name: "null credential", args: ["anthropic", "{\"anthropic\":null}"] },
  { name: "index-like provider id", args: ["2", "{\"2\":{\"type\":\"api_key\",\"key\":\"k\"},\"10\":{\"type\":\"oauth\"}}"] },
];
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
export const call = (fn, providerId, text) => {
  const dir = mkdtempSync(join(tmpdir(), "sdk-vector-"));
  const path = join(dir, "auth.json");
  if (text !== undefined) writeFileSync(path, text);
  return { credential: fn(providerId, path) ?? null };
};
