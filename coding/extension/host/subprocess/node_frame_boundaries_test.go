package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNodeFrameBufferPreservesEveryFragmentBoundary(t *testing.T) {
	path := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "frame-buffer.mjs")
	script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {FrameBuffer} = await import(pathToFileURL(process.argv[1]));
const bodies = [Buffer.alloc(0), Buffer.from('"λ😀"'), Buffer.alloc(0), Buffer.from("last"), Buffer.alloc(0)];
const wire = Buffer.concat(bodies.flatMap(body => {const header=Buffer.alloc(4);header.writeUInt32BE(body.length);return [header,body]}));
for (let boundary=0;boundary<=wire.length;boundary++) {
 const got=[];const frames=new FrameBuffer(wire.length, body=>got.push(body));
 frames.write(wire.subarray(0,boundary));frames.write(Buffer.alloc(0));frames.write(wire.subarray(boundary));
 assert.deepEqual(got,bodies,"split at byte " + boundary);
}
const got=[];const frames=new FrameBuffer(wire.length, body=>got.push(body));
for (const byte of wire) frames.write(Buffer.from([byte]));
assert.deepEqual(got,bodies);
const exact=[];const header=Buffer.alloc(4);header.writeUInt32BE(16);
const limit=new FrameBuffer(16, body=>exact.push(body));limit.write(header);
assert.equal(exact.length,0,"header alone must not expose unfilled bytes");
limit.write(Buffer.alloc(16,7));assert.deepEqual(exact,[Buffer.alloc(16,7)]);
header.writeUInt32BE(17);
assert.throws(()=>new FrameBuffer(16,()=>assert.fail("oversized body exposed")).write(header),/^Error: frame too large: 17$/);
let calls=0;
const rejected=new FrameBuffer(wire.length,()=>{calls++;throw new Error("invalid JSON")});
assert.throws(()=>rejected.write(wire),/invalid JSON/);
assert.equal(calls,1,"consumer failure stops the current input chunk");
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("frame boundaries: %v\n%s", err, output)
	}
}
