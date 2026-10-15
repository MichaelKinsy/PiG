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

// A large frame that repeats an earlier body, as the host's catalog publication to each member of a cell does, is read by comparison: the consumer receives the earlier body itself, at every split position, and a frame that differs anywhere is assembled whole in its own shared buffer.
func TestNodeFrameBufferReadsARepeatWithoutCopying(t *testing.T) {
	path := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "frame-buffer.mjs")
	script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {FrameBuffer} = await import(pathToFileURL(process.argv[1]));
const size = 96;
const earlier = Buffer.from(new SharedArrayBuffer(size));
for (let i = 0; i < size; i++) earlier[i] = 97 + (i % 26);
const pristine = Buffer.from(earlier);
const frame = body => {const header = Buffer.alloc(4); header.writeUInt32BE(body.length); return Buffer.concat([header, body]);};
const reader = got => new FrameBuffer(1024, (body, repeated) => got.push({body, repeated}), {sharedMin: 64, repeatOf: wanted => wanted === size ? earlier : undefined});
const small = Buffer.from("small");
for (let boundary = 0; boundary <= size + 4; boundary++) {
 const got = [];
 const frames = reader(got);
 const wire = Buffer.concat([frame(earlier), frame(small)]);
 frames.write(wire.subarray(0, boundary)); frames.write(wire.subarray(boundary));
 assert.equal(got.length, 2, "split at byte " + boundary);
 assert.equal(got[0].body, earlier, "a repeat is delivered as the earlier body, split at byte " + boundary);
 assert.equal(got[0].repeated, true);
 assert.deepEqual(got[1], {body: small, repeated: false});
}
for (let changed = 0; changed < size; changed++) {
 const other = Buffer.from(earlier); other[changed] = 48;
 for (const boundary of [0, 4 + changed, 4 + changed + 1, size + 4]) {
  const got = [];
  const frames = reader(got);
  const wire = frame(other);
  frames.write(wire.subarray(0, boundary)); frames.write(wire.subarray(boundary));
  assert.equal(got.length, 1);
  assert.equal(got[0].repeated, false, "byte " + changed + " differs");
  assert.notEqual(got[0].body, earlier);
  assert.ok(got[0].body.buffer instanceof SharedArrayBuffer && got[0].body.buffer.byteLength === size);
  assert.deepEqual(Buffer.from(got[0].body), other, "byte " + changed + " differs, split at byte " + boundary);
 }
}
assert.deepEqual(Buffer.from(earlier), pristine, "the earlier body is never written");
let asked = 0;
const got = [];
new FrameBuffer(1024, body => got.push(body), {sharedMin: 64, repeatOf: () => { asked++; return earlier; }}).write(frame(small));
assert.equal(asked, 0, "a frame below sharedMin is not compared");
assert.deepEqual(got, [small]);
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("frame repeats: %v\n%s", err, output)
	}
}
