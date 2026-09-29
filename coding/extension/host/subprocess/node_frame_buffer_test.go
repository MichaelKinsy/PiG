package subprocess

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The subprocess wire owns one length-prefixed JSON frame at a time. Fragmentation must not change envelopes, heartbeat handling, or make copying quadratic in a frame's size.
func TestNodeProviderSocketFrameCopyIsLinear(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	dir := t.TempDir()
	listener, path, err := ListenExtension(filepath.Join(dir, "s"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	trace := filepath.Join(dir, "copy.mjs")
	countPath := filepath.Join(dir, "copies")
	write(t, trace, `import {writeFileSync} from "node:fs";
let copies = 0;
const concat = Buffer.concat;
Buffer.concat = function(...args) {const result = concat(...args); copies += result.length; return result;};
const copy = Buffer.prototype.copy;
Buffer.prototype.copy = function(...args) {const n = copy.apply(this,args); copies += n; return n;};
process.on("exit", () => writeFileSync(process.env.FRAME_COPY_LOG, String(copies)));
`)
	text := strings.Repeat("λ", 1<<20)
	frame := func(value any) []byte {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result := make([]byte, 4+len(body))
		binary.BigEndian.PutUint32(result, uint32(len(body)))
		copy(result[4:], body)
		return result
	}
	ping := frame(map[string]any{"type": "ping", "pong": nil, "ping": map[string]string{"nonce": "fragmented"}})
	large := frame(map[string]any{"type": "notify", "notify": map[string]any{"method": "large", "args": text}})
	last := frame(map[string]any{"type": "notify", "notify": map[string]any{"method": "last", "args": "done"}})
	wire := bytes.Join([][]byte{ping, large, last}, nil)
	finished := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer func() { _ = conn.Close() }()
		var start [1]byte
		if _, err := io.ReadFull(conn, start[:]); err != nil {
			finished <- err
			return
		}
		for offset := 0; offset < len(wire); offset += 4093 {
			chunk := wire[offset:min(offset+4093, len(wire))]
			if _, err := conn.Write(chunk); err != nil {
				finished <- err
				return
			}
		}
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			finished <- err
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(header[:]))
		_, err = io.ReadFull(conn, body)
		if err == nil && string(body) != `{"type":"pong","pong":{"nonce":"fragmented"}}` {
			err = fmt.Errorf("unexpected heartbeat: %s", body)
		}
		finished <- err
	}()
	script := `import assert from "node:assert/strict";
import {createRequire, syncBuiltinESMExports} from "node:module";
import {pathToFileURL} from "node:url";
import {once} from "node:events";
const threads = createRequire(import.meta.url)("node:worker_threads");
const OriginalWorker = threads.Worker;
threads.Worker = class extends OriginalWorker {
 constructor(url, options) {super(url, {...options,execArgv:["--import",pathToFileURL(process.argv[3]).href]});}
};
syncBuiltinESMExports();
const {ProviderSocket} = await import(pathToFileURL(process.argv[1]));
const socket = await ProviderSocket.connect(process.argv[2]);
const received = [];
// A transferred string avoids structured-cloning the decoded object graph. The main decoder must not use a later extension's replacement of JSON.parse.
JSON.parse = () => { throw new Error("extension replaced JSON.parse"); };
socket.port.on("message", message => {
 if (message.kind !== "envelope") return;
 assert.equal(typeof message.json, "string", "worker structured-cloned an already decoded envelope");
 assert.equal(Object.hasOwn(message,"envelope"), false);
 assert.equal(message.bytes, Buffer.byteLength(message.json) + 4);
});
socket.on("envelope", env => received.push(env));
const exited = once(socket.worker, "exit");
socket.write(Buffer.from("s"));
await exited;
assert.deepEqual(received.map(e => e.notify.method), ["large", "last"]);
assert.equal(received[0].notify.args, "λ".repeat(1<<20));
assert.equal(received[1].notify.args, "done");
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, filepath.Join(root, "provider-socket.mjs"), path, trace)
	cmd.Env = append(os.Environ(), "FRAME_COPY_LOG="+countPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("socket: %v\n%s", err, output)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	copies, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if limit := 2 * len(wire); copies > limit {
		t.Fatalf("frame buffering copied %d bytes for %d wire bytes; limit %d (two linear copies)", copies, len(wire), limit)
	}
}
